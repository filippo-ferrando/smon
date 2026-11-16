// Package web streams a display to browsers: over WebRTC on the LAN, or over
// a WebSocket decoded with WebCodecs when the page is on localhost (USB).
package web

import (
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"smon/internal/audio"
	"smon/internal/config"
	"smon/internal/display"
	"smon/internal/h264"
	"smon/internal/touch"

	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// wsKeyRequest is what a WebSocket viewer sends when it needs a keyframe.
var wsKeyRequest = []byte(`{"t":"key"}`)

// webGOP is the periodic IDR interval for browsers, in seconds. Viewers get a
// keyframe on demand (join, loss, falling behind), so the periodic one is only
// a safety net, and each one is a bitrate spike that costs latency on WiFi.
const webGOP = 5

// wsWriteTimeout drops a WebSocket viewer that stops reading.
const wsWriteTimeout = 5 * time.Second

//go:embed index.html
var indexHTML []byte

// Options configures Serve.
type Options struct {
	Touch *touch.Touch                       // viewer touches drive this; nil keeps viewers view-only
	Audio *audio.Audio                       // sound sent to the smon output device; nil: video only
	Ready func(port int, token string) error // runs once the server is up

	TLS      bool // serve HTTPS with smon's own certificate (see tls.go)
	QR       bool // print the viewer URL as a QR code too, on a terminal
	NewToken bool // replace the saved access token with a new one
}

// inputMsg is one viewer pointer event: t is d(own), m(ove) or u(p); x, y are
// normalised to the shared screen.
type inputMsg struct {
	T    string
	ID   int
	X, Y float64
}

var viewers atomic.Int64 // namespaces pointer ids per viewer

func handleInput(t *touch.Touch, owner int, data []byte) {
	var m inputMsg
	if json.Unmarshal(data, &m) != nil || len(m.T) != 1 {
		return
	}
	t.Event(owner, m.T[0], m.ID, m.X, m.Y)
}

// Serve streams the display to any browser that opens the printed URL:
// over WebRTC, or over a WebSocket decoded with WebCodecs when the page is
// on localhost (adb reverse, which carries only TCP). HTTP and WebRTC media
// share one port number (TCP + UDP) so a single firewall rule covers both.
func Serve(d *display.Display, addr string, kbps int, opts Options) error {
	video, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "video", "smon")
	if err != nil {
		return err
	}
	udp, gst, err := d.RTP(kbps, 1200, webGOP*d.FPS)
	if err != nil {
		return err
	}
	defer gst.Stop()

	token, err := loadToken(config.Dir(), opts.NewToken)
	if err != nil {
		return fmt.Errorf("access token: %w", err)
	}
	s := &webServer{
		token: token,
		touch: opts.Touch,
		hub:   &auHub{needKey: gst.Keyframe},
		peer:  peerHooks{keyframe: gst.Keyframe},
	}
	if abrEnabled() {
		s.peer.abr = newRateCtl(gst.SetBitrate, kbps)
		go s.peer.abr.run()
	}
	s.tracks = []webrtc.TrackLocal{video}
	if opts.Audio != nil {
		audio, stop, err := startWebAudio(opts.Audio, s.hub)
		if err != nil {
			return err
		}
		if audio != nil {
			defer stop()
			s.tracks = append(s.tracks, audio)
		} else {
			opts.Audio = nil
		}
	}
	go forwardVideo(udp, video, s.hub)
	s.page = renderPage(opts.Touch != nil, opts.Audio != nil, token)

	ln, ice, err := listenTCPAndUDP(addr)
	if err != nil {
		return err
	}
	ips := lanIPs()
	if h, _, _ := net.SplitHostPort(addr); h != "" {
		ips = []string{h}
	}
	scheme := "http"
	if opts.TLS {
		c, err := loadCerts(filepath.Join(config.Dir(), "tls"), certHosts(ips))
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("tls: %w", err)
		}
		s.caDER = c.caDER
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{c.leaf}, MinVersion: tls.VersionTLS12})
		scheme = "https"
	}
	var se webrtc.SettingEngine
	se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, ice))
	if _, s.newPC, err = webAPI(se, s.peer.abr != nil); err != nil {
		return err
	}

	port := ln.Addr().(*net.TCPAddr).Port
	printURLs(scheme, ips, port, s.token, opts)

	errc := make(chan error, 3)
	go func() { errc <- http.Serve(ln, s.routes()) }()
	if opts.Ready != nil {
		go func() {
			if err := opts.Ready(port, s.token); err != nil {
				errc <- err
			}
		}()
	}
	go func() { errc <- fmt.Errorf("gstreamer exited: %v", gst.Wait()) }()
	return <-errc
}

// webServer holds what the HTTP handlers share for one Serve run.
type webServer struct {
	token  string
	caDER  []byte // smon's CA certificate, served at /ca.crt; nil without TLS
	page   []byte
	tracks []webrtc.TrackLocal
	touch  *touch.Touch
	hub    *auHub
	peer   peerHooks
	newPC  func() (*webrtc.PeerConnection, *bweProbe, error) // set before serving
}

func (s *webServer) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.authed(s.handleIndex))
	mux.HandleFunc("POST /offer", s.authed(s.handleOffer))
	mux.HandleFunc("GET /ws", s.authed(s.handleWS))
	mux.HandleFunc("GET /manifest.webmanifest", s.authed(s.handleManifest))
	mux.HandleFunc("GET /sw.js", s.authed(s.handleSW))
	for _, n := range iconSizes { // public, like any favicon
		mux.HandleFunc("GET /icon-"+strconv.Itoa(n)+".png", handleIcon(n))
	}
	if s.caDER != nil {
		mux.HandleFunc("GET /ca.crt", s.handleCA) // public: a device installs it before it trusts us
	}
	return mux
}

// authed rejects requests without the access token printed in the URL.
func (s *webServer) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(s.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// handleCA serves smon's CA certificate in the DER form Android and iOS
// install from a download.
func (s *webServer) handleCA(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="smon-ca.crt"`)
	_, _ = w.Write(s.caDER)
}

// printURLs prints the viewer URL for each address, the first one also as a
// QR code when someone is watching the terminal.
func printURLs(scheme string, ips []string, port int, token string, opts Options) {
	for i, ip := range ips {
		url := fmt.Sprintf("%s://%s/?t=%s", scheme, net.JoinHostPort(ip, strconv.Itoa(port)), token)
		fmt.Printf("open %s\n", url)
		if i == 0 && opts.QR && isTerminal(os.Stdout) {
			if q, err := renderQR(url); err == nil {
				fmt.Print(q)
			}
		}
	}
	if scheme == "https" && len(ips) > 0 {
		fmt.Printf("the browser warns about the certificate the first time; to trust it for good (and install the viewer as an app), open https://%s/ca.crt on the device and install it\n",
			net.JoinHostPort(ips[0], strconv.Itoa(port)))
	}
}

func (s *webServer) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.page)
}

// handleOffer answers a WebRTC viewer's SDP offer.
func (s *webServer) handleOffer(w http.ResponseWriter, r *http.Request) {
	var offer webrtc.SessionDescription
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&offer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := answerOffer(s.newPC, s.tracks, offer, s.touch, s.peer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(answer)
}

var upgrader websocket.Upgrader // default origin check: same host only

// handleWS streams access units and Opus frames to a WebCodecs viewer, and
// reads its keyframe requests and touch events.
func (s *webServer) handleWS(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	owner := int(viewers.Add(1))
	log.Printf("viewer %d connected (websocket)", owner)
	defer log.Printf("viewer %d disconnected", owner)
	sub := s.hub.subscribe()
	defer s.hub.unsubscribe(sub)

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.SetReadLimit(4 << 10)
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			if bytes.Equal(msg, wsKeyRequest) { // the client's decoder lost sync
				s.peer.keyframe()
			} else if s.touch != nil {
				handleInput(s.touch, owner, msg)
			}
		}
	}()
	if s.touch != nil {
		defer s.touch.Release(owner)
	}
	for {
		var m []byte
		select {
		case m = <-sub.c:
		case m = <-sub.a:
		case <-done:
			return
		}
		_ = c.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		if c.WriteMessage(websocket.BinaryMessage, m) != nil {
			return
		}
	}
}

// startWebAudio captures the smon output device as Opus and feeds it to a
// WebRTC track and the WebSocket viewers. It returns a nil track, after
// logging why, when audio cannot start; video goes on without it. stop ends
// the capture.
func startWebAudio(a *audio.Audio, hub *auHub) (track *webrtc.TrackLocalStaticRTP, stop func(), err error) {
	// Own stream id: tracks sharing one are lip-synced, which holds video
	// back to match the (deeper) audio jitter buffer. A mirrored desktop
	// wants the lowest video latency, not sync.
	track, err = webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "smon-audio")
	if err != nil {
		return nil, nil, err
	}
	udp, cmd, err := a.OpusRTP()
	if err != nil {
		log.Printf("audio off: %v", err)
		return nil, nil, nil
	}
	go forwardAudio(udp, track, hub)
	return track, func() { _ = cmd.Process.Kill() }, nil
}

// forwardAudio copies Opus RTP from gstreamer to the WebRTC track, and bare
// Opus frames to the WebSocket viewers, until udp closes.
func forwardAudio(udp *net.UDPConn, track *webrtc.TrackLocalStaticRTP, hub *auHub) {
	buf := make([]byte, 1500)
	for {
		n, err := udp.Read(buf)
		if err != nil {
			return
		}
		_, _ = track.Write(buf[:n])
		var p rtp.Packet
		if !hub.empty() && p.Unmarshal(buf[:n]) == nil && len(p.Payload) > 0 {
			hub.publishAudio(append([]byte{wsAudio}, p.Payload...))
		}
	}
}

// forwardVideo copies H.264 RTP from gstreamer to the WebRTC track, and
// whole access units to the WebSocket viewers, until udp closes. With no
// WebSocket viewer it only forwards packets: no access units to assemble.
func forwardVideo(udp *net.UDPConn, track *webrtc.TrackLocalStaticRTP, hub *auHub) {
	asm := h264.Assembler{Head: 1} // room for the flags byte
	buf := make([]byte, 1500)
	for {
		n, err := udp.Read(buf)
		if err != nil {
			return
		}
		_, _ = track.Write(buf[:n])
		if hub.empty() {
			asm.Skip(buf[:n]) // stays in step, so a joining viewer's IDR comes out whole
			continue
		}
		dropped := asm.Dropped
		au, key := asm.Push(buf[:n])
		if asm.Dropped != dropped { // a lost packet: the viewers' next frame must be an IDR
			hub.lost()
		}
		if au == nil {
			continue
		}
		if key {
			au[0] = wsKey
		}
		hub.publish(au, key)
	}
}

// renderPage turns on the page's touch and sound support as configured, and
// fills in the access token its manifest link carries.
func renderPage(input, audio bool, token string) []byte {
	page := bytes.ReplaceAll(indexHTML, []byte("{{TOKEN}}"), []byte(token))
	if input {
		page = bytes.Replace(page, []byte(`data-input="0"`), []byte(`data-input="1"`), 1)
	}
	if audio {
		page = bytes.Replace(page, []byte(`data-audio="0"`), []byte(`data-audio="1"`), 1)
	}
	return page
}

// listenTCPAndUDP opens addr for HTTP (TCP) and WebRTC media (UDP) on the
// same port number.
func listenTCPAndUDP(addr string) (net.Listener, *net.UDPConn, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	tcp := ln.Addr().(*net.TCPAddr)
	ice, err := net.ListenUDP("udp", &net.UDPAddr{IP: tcp.IP, Port: tcp.Port})
	if err != nil {
		_ = ln.Close()
		return nil, nil, fmt.Errorf("webrtc udp port: %w", err)
	}
	return ln, ice, nil
}

// peerHooks tie a viewer's peer connection to the shared encoder.
type peerHooks struct {
	keyframe func()   // the viewer needs an IDR; nil: rely on the periodic one
	abr      *rateCtl // nil: fixed bitrate
}

// answerOffer creates a peer connection for one viewer, sending tracks, and
// returns its answer with every ICE candidate in it (no trickle).
func answerOffer(newPC func() (*webrtc.PeerConnection, *bweProbe, error), tracks []webrtc.TrackLocal, offer webrtc.SessionDescription, tch *touch.Touch, hooks peerHooks) (*webrtc.SessionDescription, error) {
	pc, bwe, err := newPC()
	if err != nil {
		return nil, err
	}
	for _, track := range tracks {
		sender, err := pc.AddTrack(track)
		if err != nil {
			_ = pc.Close()
			return nil, err
		}
		var keyframe func()
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			keyframe = hooks.keyframe
		}
		go readRTCP(sender, keyframe)
	}
	owner := int(viewers.Add(1))
	if tch != nil { // without -input the channel is never read
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			if dc.Label() != "input" {
				return
			}
			dc.OnMessage(func(m webrtc.DataChannelMessage) { handleInput(tch, owner, m.Data) })
			dc.OnClose(func() { tch.Release(owner) })
		})
	}
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Printf("viewer %d %s", owner, st)
		switch st {
		case webrtc.PeerConnectionStateConnected:
			if hooks.keyframe != nil { // the periodic IDR may be seconds away
				hooks.keyframe()
			}
			if hooks.abr != nil && bwe != nil {
				hooks.abr.add(bwe)
				time.AfterFunc(5*time.Second, func() { logABR(owner, bwe) })
			}
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			if tch != nil {
				tch.Release(owner)
			}
			if hooks.abr != nil && bwe != nil {
				hooks.abr.remove(bwe)
			}
			_ = pc.Close()
		}
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		_ = pc.Close()
		return nil, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		return nil, err
	}
	<-gathered
	return pc.LocalDescription(), nil
}

// readRTCP reads a sender's RTCP until the connection closes: reading drives
// the interceptors (NACK, GCC). PLI and FIR call keyframe, when not nil.
func readRTCP(sender *webrtc.RTPSender, keyframe func()) {
	buf := make([]byte, 1500)
	for {
		n, _, err := sender.Read(buf)
		if err != nil {
			return
		}
		if keyframe == nil {
			continue
		}
		pkts, _ := rtcp.Unmarshal(buf[:n])
		for _, p := range pkts {
			switch p.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				keyframe()
			}
		}
	}
}

// logABR says whether the viewer's browser sends the transport-cc feedback
// adaptive bitrate needs.
func logABR(owner int, bwe *bweProbe) {
	if bwe.last.Load() != 0 {
		log.Printf("viewer %d: adaptive bitrate on (transport-cc feedback arriving)", owner)
	} else {
		log.Printf("viewer %d: adaptive bitrate off (no transport-cc feedback from the browser)", owner)
	}
}

// lanIPs lists this machine's non-loopback IPv4 addresses.
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
