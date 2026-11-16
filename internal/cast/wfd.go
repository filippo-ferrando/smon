package cast

// Wi-Fi Display (Miracast) source-side RTSP, M1..M7 plus keepalive.
// The sink connects to us on TCP 7236; we drive the capability exchange.

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

// wfdParams is the stream format negotiated with a sink.
type wfdParams struct {
	W, H, FPS int
	Audio     bool // AAC 48kHz stereo
	Port      int  // sink RTP port
}

// wfdConn multiplexes our requests (matched by CSeq) and the sink's requests.
type wfdConn struct {
	c       net.Conn
	wmu     sync.Mutex
	cseq    int
	mu      sync.Mutex
	pending map[string]chan *rtspMsg
	reqs    chan *rtspMsg
	err     error
	closed  chan struct{}
}

func (w *wfdConn) write(s string) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	_ = w.c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := io.WriteString(w.c, s)
	return err
}

// request sends one RTSP request and waits for its 200 answer.
func (w *wfdConn) request(method, uri, extra, body string) (*rtspMsg, error) {
	w.mu.Lock()
	w.cseq++
	cseq := strconv.Itoa(w.cseq)
	ch := make(chan *rtspMsg, 1)
	w.pending[cseq] = ch
	w.mu.Unlock()

	msg := fmt.Sprintf("%s %s RTSP/1.0\r\nCSeq: %s\r\n%s", method, uri, cseq, extra)
	if body != "" {
		msg += fmt.Sprintf("Content-Type: text/parameters\r\nContent-Length: %d\r\n", len(body))
	}
	if err := w.write(msg + "\r\n" + body); err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if !strings.Contains(m.first, " 200") {
			return m, fmt.Errorf("%s: sink answered %q", method, m.first)
		}
		return m, nil
	case <-w.closed:
		return nil, w.err
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("%s: no answer from sink", method)
	}
}

// reply answers a sink request with 200 OK and the extra headers.
func (w *wfdConn) reply(req *rtspMsg, extra string) error {
	return w.write(fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: %s\r\n%s\r\n", req.hdr.Get("CSeq"), extra))
}

// readLoop routes responses to their waiting request and queues the sink's
// own requests, until the connection fails.
func (w *wfdConn) readLoop() {
	r := textproto.NewReader(bufio.NewReader(w.c))
	for {
		m, err := readRTSP(r)
		if err != nil {
			w.err = err
			close(w.closed)
			return
		}
		if m.isResponse() {
			w.mu.Lock()
			ch := w.pending[m.hdr.Get("CSeq")]
			delete(w.pending, m.hdr.Get("CSeq"))
			w.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		w.reqs <- m
	}
}

// nextReq waits for a sink request with the given method, answering the
// harmless ones that may arrive in between.
func (w *wfdConn) nextReq(method string) (*rtspMsg, error) {
	timeout := time.After(30 * time.Second)
	for {
		select {
		case m := <-w.reqs:
			if strings.HasPrefix(m.first, method+" ") {
				return m, nil
			}
			if err := w.reply(m, ""); err != nil {
				return nil, err
			}
		case <-w.closed:
			return nil, w.err
		case <-timeout:
			return nil, fmt.Errorf("sink never sent %s", method)
		}
	}
}

// parseSinkCaps picks the stream format from the sink's M3 answer.
func parseSinkCaps(body string) (wfdParams, string, error) {
	p := wfdParams{W: 1280, H: 720, FPS: 30}
	vals := map[string]string{}
	for l := range strings.SplitSeq(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if ok {
			vals[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	// wfd_client_rtp_ports: RTP/AVP/UDP;unicast 19000 0 mode=play
	f := strings.Fields(vals["wfd_client_rtp_ports"])
	if len(f) < 2 {
		return p, "", fmt.Errorf("sink sent no wfd_client_rtp_ports")
	}
	p.Port, _ = strconv.Atoi(f[1])
	if p.Port <= 0 {
		return p, "", fmt.Errorf("bad rtp port %q", f[1])
	}

	// wfd_video_formats: <native> <pref> <profile> <level> <cea> <vesa> <hh> <latency> <minslice> <slice> <frc> <maxh> <maxv>[, <profile> ...]
	v := vals["wfd_video_formats"]
	if v == "" || v == "none" {
		return p, "", fmt.Errorf("sink sent no wfd_video_formats")
	}
	var best []string
	var bestLevel uint64
	for i, c := range strings.Split(v, ",") {
		t := strings.Fields(c)
		if i == 0 && len(t) >= 2 {
			t = t[2:] // drop native + preferred-display-mode
		}
		if len(t) < 11 {
			continue
		}
		prof, _ := strconv.ParseUint(t[0], 16, 8)
		lvl, _ := strconv.ParseUint(t[1], 16, 8)
		if prof&0x01 != 0 && (best == nil || lvl > bestLevel) { // constrained baseline
			best, bestLevel = t, lvl
		}
	}
	if best == nil {
		return p, "", fmt.Errorf("sink does not support H.264 constrained baseline: %q", v)
	}
	cea, _ := strconv.ParseUint(best[2], 16, 32)
	res := uint32(1 << 5) // 1280x720p30, mandatory for every sink
	if cea&(1<<7) != 0 && bestLevel >= 0x04 {
		res, p.W, p.H = 1<<7, 1920, 1080 // 1920x1080p30 needs level 4
	}
	video := fmt.Sprintf("00 00 01 %02X %08X 00000000 00000000 %s %s 0000 00 none none", bestLevel, res, best[5], best[6])

	// wfd_audio_codecs: LPCM 00000003 00, AAC 00000001 00
	for a := range strings.SplitSeq(vals["wfd_audio_codecs"], ",") {
		t := strings.Fields(a)
		if len(t) >= 2 && t[0] == "AAC" {
			modes, _ := strconv.ParseUint(t[1], 16, 32)
			p.Audio = modes&1 != 0
		}
	}
	return p, video, nil
}

// wfdURL is the presentation URL every source-side request targets.
const wfdURL = "rtsp://localhost/wfd1.0"

// wfdSession is the RTSP session id we hand the sink.
const wfdSession = "6B8B4567"

// runWFD drives one sink connection until teardown. play starts the stream
// and returns a func that stops it. AAC is offered only if sendAudio.
func runWFD(c net.Conn, sendAudio bool, play func(wfdParams) (stop func(), done <-chan error, err error)) error {
	w := &wfdConn{c: c, pending: map[string]chan *rtspMsg{}, reqs: make(chan *rtspMsg, 8), closed: make(chan struct{})}
	defer c.Close()
	go w.readLoop()

	p, err := w.negotiate(sendAudio)
	if err != nil {
		return err
	}
	log.Printf("miracast: streaming %dx%d@%d audio=%v to %s:%d", p.W, p.H, p.FPS, p.Audio, c.RemoteAddr().(*net.TCPAddr).IP, p.Port)
	stop, done, err := play(p)
	if err != nil {
		return err
	}
	defer stop()
	return w.serve(done)
}

// negotiate runs the WFD capability exchange (M1 to M7) and returns the
// stream format both sides agreed on.
func (w *wfdConn) negotiate(sendAudio bool) (wfdParams, error) {
	// M1 + M2: capability handshake.
	if _, err := w.request("OPTIONS", "*", "Require: org.wfa.wfd1.0\r\n", ""); err != nil {
		return wfdParams{}, fmt.Errorf("M1: %w", err)
	}
	m2, err := w.nextReq("OPTIONS")
	if err != nil {
		return wfdParams{}, fmt.Errorf("M2: %w", err)
	}
	if err := w.reply(m2, "Public: org.wfa.wfd1.0, SET_PARAMETER, GET_PARAMETER, SETUP, PLAY, PAUSE, TEARDOWN\r\n"); err != nil {
		return wfdParams{}, fmt.Errorf("M2: %w", err)
	}

	// M3: ask what the sink can do.
	m3, err := w.request("GET_PARAMETER", wfdURL, "",
		"wfd_client_rtp_ports\r\nwfd_audio_codecs\r\nwfd_video_formats\r\n")
	if err != nil {
		return wfdParams{}, fmt.Errorf("M3: %w", err)
	}
	p, video, err := parseSinkCaps(m3.body)
	if err != nil {
		return wfdParams{}, fmt.Errorf("M3: %w", err)
	}
	if !p.Audio && sendAudio {
		log.Print("miracast: the sink takes no AAC audio; video only")
	}
	p.Audio = p.Audio && sendAudio
	audio := "none"
	if p.Audio {
		audio = "AAC 00000001 00"
	}

	// M4: tell it what we will send. M5: ask it to SETUP.
	local := w.c.LocalAddr().(*net.TCPAddr)
	m4 := fmt.Sprintf("wfd_video_formats: %s\r\nwfd_audio_codecs: %s\r\nwfd_presentation_URL: rtsp://%s/wfd1.0/streamid=0 none\r\nwfd_client_rtp_ports: RTP/AVP/UDP;unicast %d 0 mode=play\r\n",
		video, audio, local, p.Port)
	if _, err := w.request("SET_PARAMETER", wfdURL, "", m4); err != nil {
		return wfdParams{}, fmt.Errorf("M4: %w", err)
	}
	if _, err := w.request("SET_PARAMETER", wfdURL, "", "wfd_trigger_method: SETUP\r\n"); err != nil {
		return wfdParams{}, fmt.Errorf("M5: %w", err)
	}

	// M6 SETUP, M7 PLAY.
	m6, err := w.nextReq("SETUP")
	if err != nil {
		return wfdParams{}, fmt.Errorf("M6: %w", err)
	}
	if err := w.reply(m6, fmt.Sprintf("Session: %s;timeout=30\r\nTransport: RTP/AVP/UDP;unicast;client_port=%d;server_port=%d\r\n", wfdSession, p.Port, p.Port)); err != nil {
		return wfdParams{}, fmt.Errorf("M6: %w", err)
	}
	m7, err := w.nextReq("PLAY")
	if err != nil {
		return wfdParams{}, fmt.Errorf("M7: %w", err)
	}
	if err := w.reply(m7, "Session: "+wfdSession+"\r\nRange: npt=now-\r\n"); err != nil {
		return wfdParams{}, fmt.Errorf("M7: %w", err)
	}
	return p, nil
}

// serve keeps a playing session alive until the sink tears it down, the
// connection drops, or the stream stops (done).
//
// M16 keepalive every 25s (sink timeout is 30s); it answers everything the
// sink sends, including wfd_idr_request (the encoder GOP covers IDR).
func (w *wfdConn) serve(done <-chan error) error {
	keep := time.NewTicker(25 * time.Second)
	defer keep.Stop()
	for {
		select {
		case m := <-w.reqs:
			_ = w.reply(m, "") // a dead connection shows up as w.closed
			if strings.HasPrefix(m.first, "TEARDOWN ") || strings.Contains(m.body, "wfd_trigger_method: TEARDOWN") {
				log.Print("miracast: sink tore down the session")
				return nil
			}
		case <-keep.C:
			go func() {
				if _, err := w.request("GET_PARAMETER", wfdURL, "Session: "+wfdSession+"\r\n", ""); err != nil {
					log.Printf("miracast: keepalive: %v", err)
					_ = w.c.Close()
				}
			}()
		case <-w.closed:
			return fmt.Errorf("sink disconnected: %w", w.err)
		case err := <-done:
			return fmt.Errorf("stream stopped: %v", err) // nil after Stop
		}
	}
}
