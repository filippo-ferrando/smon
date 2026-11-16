package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

//go:embed index.html
var indexHTML []byte

// webOpts configures serveWeb.
type webOpts struct {
	touch *Touch                             // viewer touches drive this; nil keeps viewers view-only
	audio *Audio                             // sound sent to the smon output device; nil: video only
	ready func(port int, token string) error // runs once the server is up
}

// inputMsg is one viewer pointer event: t is d(own), m(ove) or u(p); x, y are
// normalised to the shared screen.
type inputMsg struct {
	T    string
	ID   int
	X, Y float64
}

var viewers atomic.Int64 // namespaces pointer ids per viewer

func handleInput(t *Touch, owner int, data []byte) {
	var m inputMsg
	if json.Unmarshal(data, &m) != nil || len(m.T) != 1 {
		return
	}
	t.Event(owner, m.T[0], m.ID, m.X, m.Y)
}

// serveWeb streams the display to any browser that opens the printed URL:
// over WebRTC, or over a WebSocket decoded with WebCodecs when the page is
// on localhost (adb reverse, which carries only TCP). HTTP and WebRTC media
// share one port number (TCP + UDP) so a single firewall rule covers both.
func serveWeb(d *Display, addr string, kbps int, opts webOpts) error {
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "video", "smon")
	if err != nil {
		return err
	}

	udp, gst, err := d.RTP(kbps, 1200)
	if err != nil {
		return err
	}
	defer gst.Process.Kill()
	var hub auHub
	tracks := []webrtc.TrackLocal{track}
	if opts.audio != nil {
		// Same stream id as the video, so the browser plays them as one stream.
		atrack, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "smon")
		if err != nil {
			return err
		}
		audp, agst, err := opts.audio.OpusRTP()
		if err != nil {
			log.Printf("audio off: %v", err)
			opts.audio = nil
		} else {
			defer agst.Process.Kill()
			tracks = append(tracks, atrack)
			go func() {
				buf := make([]byte, 1500)
				for {
					n, err := audp.Read(buf)
					if err != nil {
						return
					}
					atrack.Write(buf[:n])
					var p rtp.Packet // WebSocket viewers get bare Opus frames
					if !hub.empty() && p.Unmarshal(buf[:n]) == nil && len(p.Payload) > 0 {
						hub.publishAudio(append([]byte{wsAudio}, p.Payload...))
					}
				}
			}()
		}
	}
	go func() {
		var asm auAssembler
		buf := make([]byte, 1500)
		for {
			n, err := udp.Read(buf)
			if err != nil {
				return
			}
			track.Write(buf[:n])
			if au := asm.push(buf[:n]); au != nil && !hub.empty() {
				key := isKeyframe(au)
				flags := byte(0)
				if key {
					flags = wsKey
				}
				hub.publish(append([]byte{flags}, au...), key)
			}
		}
	}()

	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)
	authed := func(r *http.Request) bool {
		return subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(token)) == 1
	}
	page := indexHTML
	if opts.touch != nil {
		page = bytes.Replace(page, []byte(`data-input="0"`), []byte(`data-input="1"`), 1)
	}
	if opts.audio != nil {
		page = bytes.Replace(page, []byte(`data-audio="0"`), []byte(`data-audio="1"`), 1)
	}

	var api *webrtc.API // set below, before serving
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	mux.HandleFunc("POST /offer", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var offer webrtc.SessionDescription
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&offer); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		answer, err := answerOffer(api, tracks, offer, opts.touch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(answer)
	})
	var upgrader websocket.Upgrader // default origin check: same host only
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		owner := int(viewers.Add(1))
		log.Printf("viewer %d connected (websocket)", owner)
		defer log.Printf("viewer %d disconnected", owner)
		sub := hub.subscribe()
		defer hub.unsubscribe(sub)
		done := make(chan struct{})
		go func() {
			defer close(done)
			c.SetReadLimit(4 << 10)
			for {
				_, msg, err := c.ReadMessage()
				if err != nil {
					return
				}
				if opts.touch != nil {
					handleInput(opts.touch, owner, msg)
				}
			}
		}()
		if opts.touch != nil {
			defer opts.touch.Release(owner)
		}
		for {
			select {
			case m := <-sub.c:
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if c.WriteMessage(websocket.BinaryMessage, m) != nil {
					return
				}
			case m := <-sub.a:
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if c.WriteMessage(websocket.BinaryMessage, m) != nil {
					return
				}
			case <-done:
				return
			}
		}
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ice, err := net.ListenUDP("udp", &net.UDPAddr{IP: ln.Addr().(*net.TCPAddr).IP, Port: port})
	if err != nil {
		return fmt.Errorf("webrtc udp port: %w", err)
	}
	var se webrtc.SettingEngine
	se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, ice))
	api = webrtc.NewAPI(webrtc.WithSettingEngine(se))
	ips := lanIPs()
	if h, _, _ := net.SplitHostPort(addr); h != "" {
		ips = []string{h}
	}
	for _, ip := range ips {
		fmt.Printf("open http://%s:%d/?t=%s\n", ip, port, token)
	}
	errc := make(chan error, 3)
	go func() { errc <- http.Serve(ln, mux) }()
	if opts.ready != nil {
		go func() {
			if err := opts.ready(port, token); err != nil {
				errc <- err
			}
		}()
	}
	go func() { errc <- fmt.Errorf("gstreamer exited: %v", gst.Wait()) }()
	return <-errc
}

func answerOffer(api *webrtc.API, tracks []webrtc.TrackLocal, offer webrtc.SessionDescription, touch *Touch) (*webrtc.SessionDescription, error) {
	pc, err := api.NewPeerConnection(webrtc.Configuration{}) // LAN only, no STUN
	if err != nil {
		return nil, err
	}
	for _, track := range tracks {
		sender, err := pc.AddTrack(track)
		if err != nil {
			pc.Close()
			return nil, err
		}
		go func() { // drain RTCP so interceptors keep working
			buf := make([]byte, 1500)
			for {
				if _, _, err := sender.Read(buf); err != nil {
					return
				}
			}
		}()
	}
	owner := int(viewers.Add(1))
	if touch != nil { // without -input the channel is never read
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			if dc.Label() != "input" {
				return
			}
			dc.OnMessage(func(m webrtc.DataChannelMessage) { handleInput(touch, owner, m.Data) })
			dc.OnClose(func() { touch.Release(owner) })
		})
	}
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("viewer %d %s", owner, s)
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateDisconnected {
			if touch != nil {
				touch.Release(owner)
			}
			pc.Close()
		}
	})
	if err := pc.SetRemoteDescription(offer); err != nil {
		pc.Close()
		return nil, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return nil, err
	}
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		pc.Close()
		return nil, err
	}
	<-done // non-trickle: answer carries all candidates
	return pc.LocalDescription(), nil
}

func lanIPs() []string {
	var ips []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			ips = append(ips, n.IP.String())
		}
	}
	return ips
}
