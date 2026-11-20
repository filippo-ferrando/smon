package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"smon/internal/logx"

	"github.com/pion/webrtc/v4"

	"smon/internal/display"
	"smon/internal/gst"
)

// monitor is one of smon's virtual monitors and its video stream.
type monitor struct {
	n     int
	d     *display.Display
	kbps  int // the ceiling, -kbps
	udp   *net.UDPConn
	video *webrtc.TrackLocalStaticRTP
	hub   *auHub
	peer  peerHooks
	input *inputs

	pipeMu sync.Mutex
	pipe   *gst.Pipeline // replaced when the monitor turns

	// turnMu is held while the pipeline is replaced, and by stop.
	turnMu   sync.Mutex
	portrait atomic.Bool // what its viewer last asked for
	upright  atomic.Bool // what it is: taller than wide (d.W, d.H change under turnMu)
	stopped  bool
	failed   error // the stream could not restart after a turn
	noTurn   sync.Once
}

// rtpMTU is the payload size the browsers' RTP gets.
const rtpMTU = 1200

// startMonitor starts encoding display d for viewers.
func startMonitor(d *display.Display, n, kbps int, vc videoCodec, in *inputs) (*monitor, error) {
	video, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: vc.mime, ClockRate: 90000}, "video", "smon")
	if err != nil {
		return nil, err
	}
	udp, pipe, err := d.RTP(kbps, rtpMTU, webGOP*d.FPS)
	if err != nil {
		return nil, err
	}
	m := &monitor{n: n, d: d, kbps: kbps, pipe: pipe, udp: udp, video: video, input: in}
	m.upright.Store(d.H > d.W)
	m.hub = &auHub{needKey: m.keyframe}
	m.peer = peerHooks{keyframe: m.keyframe}
	if !gst.LiveBitrate(pipe.Encoder) {
		logx.Warnf("abr", "adaptive bitrate off: encoder %s can't change bitrate while it runs", pipe.Encoder)
	} else if abrEnabled() {
		m.peer.abr = newRateCtl(m.setBitrate, kbps)
		go m.peer.abr.run()
	}
	m.peer.stats = &statsSource{
		encoder: pipe.Encoder, codec: vc.name, size: fmt.Sprintf("%dx%d", d.W, d.H),
		fps: d.FPS, kbps: kbps, abr: m.peer.abr, mon: d.Output,
	}
	go forwardVideo(udp, video, m.hub, vc.asm)
	return m, nil
}

// pipeline is the pipeline encoding the monitor now.
func (m *monitor) pipeline() *gst.Pipeline {
	m.pipeMu.Lock()
	defer m.pipeMu.Unlock()
	return m.pipe
}

func (m *monitor) keyframe()         { m.pipeline().Keyframe() }
func (m *monitor) setBitrate(kb int) { m.pipeline().SetBitrate(kb) }

// wait blocks until the stream ends, and returns why. A pipeline replaced
// by a turn does not count.
func (m *monitor) wait() error {
	for {
		p := m.pipeline()
		err := p.Wait()
		m.turnMu.Lock() // a turn in progress decides
		cur, failed := m.pipeline(), m.failed
		m.turnMu.Unlock()
		if failed != nil {
			return failed
		}
		if cur == p {
			return err
		}
	}
}

// stop ends the stream; the display itself is closed by its owner.
func (m *monitor) stop() {
	m.turnMu.Lock()
	m.stopped = true
	m.turnMu.Unlock()
	if m.peer.abr != nil {
		m.peer.abr.stop()
	}
	m.pipeline().Stop()
	_ = m.udp.Close()
}

// orient asks for the monitor to be portrait or landscape, like the screen
// of the device showing it. Only a viewer that has the monitor to itself
// asks (see webServer.orient).
func (m *monitor) orient(portrait bool) {
	m.portrait.Store(portrait)
	if !m.d.CanResize() { // its size never changes: d.W and d.H are safe to read
		if m.d.W != m.d.H && portrait != m.upright.Load() {
			m.noTurn.Do(func() {
				logx.Warnf(monitorScope(m.n), "the viewer turned, but this compositor can't change the monitor's size while it runs: restart smon (with -portrait for an upright one)")
			})
		}
		return
	}
	go m.turn()
}

// turn makes the monitor portrait or landscape, as its viewer last asked:
// the output changes size and the stream restarts at it. Viewers stay
// connected (rtpSplice keeps their RTP continuous); their next frame is a
// keyframe at the new size.
func (m *monitor) turn() {
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	for !m.stopped && m.failed == nil {
		want := m.portrait.Load()
		if m.d.W == m.d.H || want == (m.d.H > m.d.W) { // square: either way
			return
		}
		w, h := m.d.H, m.d.W
		kbps := m.kbps
		if m.peer.abr != nil {
			kbps, _ = m.peer.abr.current() // where adaptive bitrate is now
		}
		m.pipeline().Stop() // the viewers keep the last frame meanwhile
		err := m.d.Resize(w, h)
		if err != nil {
			logx.Warnf(monitorScope(m.n), "can't turn it %s: %v", orientation(want), err)
		}
		p, perr := m.d.RTPTo(m.udp, kbps, rtpMTU, webGOP*m.d.FPS)
		if perr != nil && err == nil { // the new size won't encode: back to the old one
			logx.Errorf(monitorScope(m.n), "no stream at %dx%d: %v", w, h, perr)
			err = perr
			if m.d.Resize(h, w) == nil {
				p, perr = m.d.RTPTo(m.udp, kbps, rtpMTU, webGOP*m.d.FPS)
			}
		}
		if perr != nil {
			m.failed = fmt.Errorf("the stream did not restart after turning: %w", perr)
			return
		}
		m.pipeMu.Lock()
		m.pipe = p
		m.pipeMu.Unlock()
		m.upright.Store(m.d.H > m.d.W)
		m.peer.stats.setStream(p.Encoder, fmt.Sprintf("%dx%d", m.d.W, m.d.H))
		if err != nil {
			return // stays as it was: don't try again until the viewer turns again
		}
		logx.Infof(monitorScope(m.n), "%s turned %s: %dx%d", m.d.Output(), orientation(want), m.d.W, m.d.H)
	}
}

func orientation(portrait bool) string {
	if portrait {
		return "portrait"
	}
	return "landscape"
}

// tracks are what a viewer of this monitor receives: its video, and the
// shared audio when there is any.
func (m *monitor) tracks(audio webrtc.TrackLocal) []webrtc.TrackLocal {
	if audio == nil {
		return []webrtc.TrackLocal{m.video}
	}
	return []webrtc.TrackLocal{m.video, audio}
}

// monitor returns monitor n, or nil when it is gone.
func (s *webServer) monitor(n int) *monitor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mons[n]
}

// createMonitor makes monitor n for a viewer (the share dialog may show on
// the desktop) and starts streaming it.
func (s *webServer) createMonitor(n int, portrait bool) error {
	if s.newMonitor == nil {
		return errors.New("this smon serves one monitor")
	}
	// One at a time: one share dialog on the desktop, and backends that find
	// the new output by what appeared since must not see two appear.
	s.creating.Lock()
	defer s.creating.Unlock()
	logx.Infof(monitorScope(n), "creating it for a new viewer")
	d, tch, pen, err := s.newMonitor(n, portrait)
	if err != nil {
		return err
	}
	m, err := startMonitor(d, n, s.kbps, s.codec, &inputs{touch: tch, pen: pen, kbd: s.kbd})
	if err != nil {
		s.closeMonitor(d)
		return err
	}
	s.mu.Lock()
	s.mons[n] = m
	s.mu.Unlock()
	go func() {
		if err := m.wait(); err != nil && s.monitor(n) == m {
			logx.Errorf(monitorScope(n), "gstreamer exited: %v", err)
		}
	}()
	return nil
}

// destroyMonitor closes monitor n once nobody has used it for a while.
func (s *webServer) destroyMonitor(n int) {
	s.mu.Lock()
	m := s.mons[n]
	delete(s.mons, n)
	s.mu.Unlock()
	if m == nil {
		return
	}
	logx.Infof(monitorScope(n), "closing %s: no viewer for %v", m.d.Output(), monitorGrace)
	m.stop()
	s.closeMonitor(m.d)
}

// reapMonitors closes idle monitors until done.
func (s *webServer) reapMonitors(done <-chan struct{}) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.alloc.reap()
		case <-done:
			return
		}
	}
}

// claimReq is what a viewer page sends before connecting: its client id, the
// monitor it asks for (0: any) and its orientation, for a new monitor.
type claimReq struct {
	CID      string `json:"cid"`
	M        int    `json:"m"`
	Portrait bool   `json:"portrait"`
}

// handleClaim gives a viewer its monitor, creating one if needed. It can take
// a while: the desktop may show the share dialog.
func (s *webServer) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req claimReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.CID == "" || len(req.CID) > 64 {
		http.Error(w, "bad claim", http.StatusBadRequest)
		return
	}
	n, err := s.alloc.claim(req.CID, req.M, req.Portrait)
	if err != nil {
		logx.Errorf("viewer", "%s: no monitor: %v", clientIP(r), err)
	}
	switch {
	case errors.Is(err, errNoMonitor):
		http.Error(w, fmt.Sprintf("monitor %d does not exist (any more): open the page without &m=", req.M), http.StatusNotFound)
		return
	case errors.Is(err, errFull):
		http.Error(w, fmt.Sprintf("all %d monitors are in use: add &m=1 to the URL to watch monitor 1, or start smon with a higher -monitors", s.alloc.max), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "could not create a monitor: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"m": n})
}

// monitorParam reads the m query parameter: the monitor a viewer was given.
func monitorParam(r *http.Request) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("m")); err == nil && n > 0 {
		return n
	}
	return 1
}

// orientMsg is what a viewer sends when its screen turns, and on connecting:
// {"t":"orient","portrait":true}.
type orientMsg struct {
	T        string `json:"t"`
	Portrait bool   `json:"portrait"`
}

// parseOrient reads an orientMsg; ok is false for any other message.
func parseOrient(b []byte) (portrait, ok bool) {
	var m orientMsg
	if !bytes.Contains(b, []byte(`"orient"`)) || json.Unmarshal(b, &m) != nil || m.T != "orient" {
		return false, false
	}
	return m.Portrait, true
}

// orient turns monitor n to match the screen of viewer cid, when cid is its
// only viewer: two devices showing one monitor would fight over it.
func (s *webServer) orient(n int, cid string, portrait bool) {
	m := s.monitor(n)
	if m == nil || !s.rotate {
		return
	}
	if v := s.alloc.viewers(n); v > 1 {
		if up := m.upright.Load(); portrait != up {
			logx.Infof(monitorScope(n), "a viewer turned, but %d devices show it: it stays %s", v, orientation(up))
		}
		return
	}
	m.orient(portrait)
}

// monitorScope names monitor n in log lines.
func monitorScope(n int) string { return fmt.Sprint("monitor ", n) }
