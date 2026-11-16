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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"smon/internal/audio"
	"smon/internal/config"
	"smon/internal/display"
	"smon/internal/rtpau"
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
	Touch    *touch.Touch                       // viewer touches drive this; nil: no touch
	Pen      *touch.Pen                         // viewer pens drive this; nil: no pen
	Keyboard *touch.Keyboard                    // viewer keys drive this; nil: no keyboard
	Audio    *audio.Audio                       // sound sent to the smon output device; nil: video only
	Ready    func(port int, token string) error // runs once the server is up

	// Monitors caps how many monitors viewers get: above 1, each new viewer
	// gets one of its own, made by NewMonitor (n from 2; portrait: the
	// viewer's screen is upright) and closed by CloseMonitor when idle.
	Monitors     int
	NewMonitor   func(n int, portrait bool) (*display.Display, *touch.Touch, *touch.Pen, error)
	CloseMonitor func(*display.Display)

	TLS      bool // serve HTTPS with smon's own certificate (see tls.go)
	QR       bool // print the viewer URL as a QR code too, on a terminal
	NewToken bool // replace the saved access token with a new one
}

var viewers atomic.Int64 // namespaces pointer ids per viewer

// Serve streams the display to any browser that opens the printed URL:
// over WebRTC, or over a WebSocket decoded with WebCodecs when the page is
// on localhost (adb reverse, which carries only TCP). HTTP and WebRTC media
// share one port number (TCP + UDP) so a single firewall rule covers both.
//
// d is monitor 1. With opts.Monitors above 1, each further viewer gets a
// monitor of its own, made by opts.NewMonitor, until there are that many.
func Serve(d *display.Display, addr string, kbps int, opts Options) error {
	token, err := loadToken(config.Dir(), opts.NewToken)
	if err != nil {
		return fmt.Errorf("access token: %w", err)
	}
	vc := codecFor(d.Codec)
	first, err := startMonitor(d, 1, kbps, vc, &inputs{touch: opts.Touch, pen: opts.Pen, kbd: opts.Keyboard})
	if err != nil {
		return err
	}
	defer first.stop()
	s := &webServer{
		token: token, codec: vc, kbps: kbps, kbd: opts.Keyboard,
		mons:       map[int]*monitor{1: first},
		newMonitor: opts.NewMonitor, closeMonitor: opts.CloseMonitor,
	}
	if s.closeMonitor == nil {
		s.closeMonitor = (*display.Display).Close
	}
	s.alloc = newAllocator(max(opts.Monitors, 1), s.createMonitor, s.destroyMonitor)
	if opts.Monitors > 1 {
		done := make(chan struct{})
		defer close(done)
		go s.reapMonitors(done)
		defer s.closeAll()
	}
	if opts.Audio != nil {
		audio, stop, err := startWebAudio(opts.Audio, first.hub) // WebSocket viewers are all on monitor 1
		if err != nil {
			return err
		}
		if audio != nil {
			defer stop()
			s.audio = audio
		} else {
			opts.Audio = nil
		}
	}
	s.page = renderPage(pageFlags{input: opts.Touch != nil, pen: opts.Pen != nil, keyboard: opts.Keyboard != nil, audio: opts.Audio != nil, codec: vc.name}, token)

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
	if _, s.newPC, err = webAPI(se, first.peer.abr != nil); err != nil {
		return err
	}

	port := ln.Addr().(*net.TCPAddr).Port
	printURLs(scheme, ips, port, s.token, opts)
	if opts.Monitors > 1 {
		fmt.Printf("each device that opens it gets its own monitor, up to %d; add &m=1 to the URL to watch monitor 1 instead\n", opts.Monitors)
	}

	errc := make(chan error, 3)
	go func() { errc <- http.Serve(ln, s.routes()) }()
	if opts.Ready != nil {
		go func() {
			if err := opts.Ready(port, s.token); err != nil {
				errc <- err
			}
		}()
	}
	go func() { errc <- fmt.Errorf("gstreamer exited: %v", first.pipe.Wait()) }()
	return <-errc
}

// closeAll closes every monitor after the first, when Serve ends.
func (s *webServer) closeAll() {
	s.mu.Lock()
	var rest []*monitor
	for n, m := range s.mons {
		if n > 1 {
			rest = append(rest, m)
			delete(s.mons, n)
		}
	}
	s.mu.Unlock()
	for _, m := range rest {
		m.stop()
		s.closeMonitor(m.d)
	}
}

// webServer holds what the HTTP handlers share for one Serve run.
type webServer struct {
	token string
	caDER []byte // smon's CA certificate, served at /ca.crt; nil without TLS
	page  []byte
	codec videoCodec // what the video tracks carry
	kbps  int
	audio webrtc.TrackLocal                                 // shared by every monitor; nil: no sound
	kbd   *touch.Keyboard                                   // shared: keys go to the focused window
	newPC func() (*webrtc.PeerConnection, *bweProbe, error) // set before serving

	alloc        *allocator
	newMonitor   func(n int, portrait bool) (*display.Display, *touch.Touch, *touch.Pen, error)
	closeMonitor func(*display.Display)

	mu       sync.Mutex
	mons     map[int]*monitor
	creating sync.Mutex // held while a monitor is made
}

func (s *webServer) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.authed(s.handleIndex))
	mux.HandleFunc("POST /claim", s.authed(s.handleClaim))
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

func (s *webServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	log.Printf("viewer page opened from %s (%s)", clientIP(r), r.UserAgent())
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
	if !s.codec.offeredIn(offer.SDP) {
		log.Printf("viewer at %s can't receive %s (its browser offers %s): restart smon with -codec h264", clientIP(r), s.codec.label, strings.Join(offeredCodecs(offer.SDP), ", "))
		http.Error(w, s.codec.unsupported(), http.StatusUnsupportedMediaType)
		return
	}
	n := monitorParam(r)
	m := s.monitor(n)
	if m == nil { // closed since the claim: the page claims again
		http.Error(w, fmt.Sprintf("monitor %d is gone", n), http.StatusServiceUnavailable)
		return
	}
	cid := r.URL.Query().Get("c")
	hooks := m.peer
	hooks.join = func() { s.alloc.join(n, cid) }
	hooks.leave = func() { s.alloc.leave(n, cid) }
	answer, err := answerOffer(s.newPC, m.tracks(s.audio), offer, m.input, hooks)
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
	m := s.monitor(1) // USB serves one monitor
	cid := "ws-" + strconv.Itoa(owner)
	s.alloc.join(1, cid)
	defer s.alloc.leave(1, cid)
	sub := m.hub.subscribe()
	defer m.hub.unsubscribe(sub)
	if st := m.peer.stats; st != nil {
		st.viewers.Add(1)
		defer st.viewers.Add(-1)
	}
	tick := time.NewTicker(statsEvery)
	defer tick.Stop()

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
				m.peer.keyframe()
			} else if m.input.enabled() {
				m.input.handle(owner, msg)
			}
		}
	}()
	if m.input.enabled() {
		defer m.input.release(owner)
	}
	for {
		var msg []byte
		kind := websocket.BinaryMessage
		select {
		case msg = <-sub.c:
		case msg = <-sub.a:
		case <-tick.C: // stats go as text, media as binary
			if m.peer.stats == nil {
				continue
			}
			msg, kind = m.peer.stats.message(), websocket.TextMessage
		case <-done:
			return
		}
		_ = c.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		if c.WriteMessage(kind, msg) != nil {
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

// forwardVideo copies video RTP from gstreamer to the WebRTC track, and
// whole access units to the WebSocket viewers, until udp closes. With no
// WebSocket viewer it only forwards packets: no access units to assemble.
func forwardVideo(udp *net.UDPConn, track *webrtc.TrackLocalStaticRTP, hub *auHub, codec rtpau.Codec) {
	asm := rtpau.Assembler{Codec: codec, Head: 1} // room for the flags byte
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

// pageFlags are the viewer features this run turns on, and the codec.
type pageFlags struct {
	input, pen, keyboard, audio bool
	codec                       string
}

// renderPage turns on the page's input and sound support as configured, and
// fills in the access token its manifest link carries.
func renderPage(f pageFlags, token string) []byte {
	page := bytes.ReplaceAll(indexHTML, []byte("{{TOKEN}}"), []byte(token))
	if f.codec != "" {
		page = bytes.Replace(page, []byte(`data-codec="h264"`), []byte(`data-codec="`+f.codec+`"`), 1)
	}
	for _, on := range []struct {
		attr string
		on   bool
	}{{"input", f.input}, {"pen", f.pen}, {"keyboard", f.keyboard}, {"audio", f.audio}} {
		if on.on {
			page = bytes.Replace(page, []byte(`data-`+on.attr+`="0"`), []byte(`data-`+on.attr+`="1"`), 1)
		}
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
	keyframe func()       // the viewer needs an IDR; nil: rely on the periodic one
	abr      *rateCtl     // nil: fixed bitrate
	stats    *statsSource // nil: no stats for the viewer

	join, leave func() // the viewer connected, and went away; nil: no-op
}

// answerOffer creates a peer connection for one viewer, sending tracks, and
// returns its answer with every ICE candidate in it (no trickle).
func answerOffer(newPC func() (*webrtc.PeerConnection, *bweProbe, error), tracks []webrtc.TrackLocal, offer webrtc.SessionDescription, in *inputs, hooks peerHooks) (*webrtc.SessionDescription, error) {
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
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch {
		case dc.Label() == "ctl" && hooks.stats != nil:
			dc.OnOpen(func() { go sendStats(dc, hooks.stats) })
		case dc.Label() == "input" && in.enabled(): // without -input/-keyboard the channel is never read
			dc.OnMessage(func(m webrtc.DataChannelMessage) { in.handle(owner, m.Data) })
			dc.OnClose(func() { in.release(owner) })
		}
	})
	var counted atomic.Bool // in the viewer count while connected
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
			if counted.CompareAndSwap(false, true) {
				if hooks.stats != nil {
					hooks.stats.viewers.Add(1)
				}
				if hooks.join != nil {
					hooks.join()
				}
			}
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			if in.enabled() {
				in.release(owner)
			}
			if hooks.abr != nil && bwe != nil {
				hooks.abr.remove(bwe)
			}
			if counted.CompareAndSwap(true, false) {
				if hooks.stats != nil {
					hooks.stats.viewers.Add(-1)
				}
				if hooks.leave != nil {
					hooks.leave()
				}
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

// clientIP is the address a request came from, for the log.
func clientIP(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}
