package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/pion/webrtc/v4"

	"smon/internal/display"
	"smon/internal/gst"
)

// monitor is one of smon's virtual monitors and its video stream.
type monitor struct {
	n     int
	d     *display.Display
	pipe  *gst.Pipeline
	udp   *net.UDPConn
	video *webrtc.TrackLocalStaticRTP
	hub   *auHub
	peer  peerHooks
	input *inputs
}

// startMonitor starts encoding display d for viewers.
func startMonitor(d *display.Display, n, kbps int, vc videoCodec, in *inputs) (*monitor, error) {
	video, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: vc.mime, ClockRate: 90000}, "video", "smon")
	if err != nil {
		return nil, err
	}
	udp, pipe, err := d.RTP(kbps, 1200, webGOP*d.FPS)
	if err != nil {
		return nil, err
	}
	m := &monitor{
		n: n, d: d, pipe: pipe, udp: udp, video: video, input: in,
		hub:  &auHub{needKey: pipe.Keyframe},
		peer: peerHooks{keyframe: pipe.Keyframe},
	}
	if !gst.LiveBitrate(pipe.Encoder) {
		log.Printf("adaptive bitrate off: encoder %s can't change bitrate while it runs", pipe.Encoder)
	} else if abrEnabled() {
		m.peer.abr = newRateCtl(pipe.SetBitrate, kbps)
		go m.peer.abr.run()
	}
	m.peer.stats = &statsSource{
		encoder: pipe.Encoder, codec: vc.name, size: fmt.Sprintf("%dx%d", d.W, d.H),
		fps: d.FPS, kbps: kbps, abr: m.peer.abr, mon: d.Output,
	}
	go forwardVideo(udp, video, m.hub, vc.asm)
	return m, nil
}

// stop ends the stream; the display itself is closed by its owner.
func (m *monitor) stop() {
	if m.peer.abr != nil {
		m.peer.abr.stop()
	}
	m.pipe.Stop()
	_ = m.udp.Close()
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
	log.Printf("creating monitor %d for a new viewer", n)
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
		if err := m.pipe.Wait(); err != nil && s.monitor(n) == m {
			log.Printf("monitor %d: gstreamer exited: %v", n, err)
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
	log.Printf("closing monitor %d (%s): no viewer for %v", n, m.d.Output(), monitorGrace)
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
