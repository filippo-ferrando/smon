package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/pion/webrtc/v4"
)

//go:embed index.html
var indexHTML []byte

// serveWeb streams the display to any browser that opens the printed URL.
// HTTP and WebRTC media share one port number (TCP + UDP) so a single
// firewall rule covers both. ready, if set, runs once the server is up.
func serveWeb(d *Display, addr string, kbps int, ready func(port int, token string) error) error {
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "video", "smon")
	if err != nil {
		return err
	}

	udp, gst, err := d.RTP(kbps, 1200)
	if err != nil {
		return err
	}
	defer gst.Process.Kill()
	go func() {
		buf := make([]byte, 1500)
		for {
			n, err := udp.Read(buf)
			if err != nil {
				return
			}
			track.Write(buf[:n])
		}
	}()

	b := make([]byte, 16)
	rand.Read(b)
	token := hex.EncodeToString(b)
	authed := func(r *http.Request) bool {
		return subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(token)) == 1
	}

	var api *webrtc.API // set below, before serving
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if !authed(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
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
		answer, err := answerOffer(api, track, offer)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(answer)
	})

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ice, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
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
	if ready != nil {
		go func() {
			if err := ready(port, token); err != nil {
				errc <- err
			}
		}()
	}
	go func() { errc <- fmt.Errorf("gstreamer exited: %v", gst.Wait()) }()
	return <-errc
}

func answerOffer(api *webrtc.API, track webrtc.TrackLocal, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	pc, err := api.NewPeerConnection(webrtc.Configuration{}) // LAN only, no STUN
	if err != nil {
		return nil, err
	}
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
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Printf("viewer %s", s)
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateDisconnected {
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
