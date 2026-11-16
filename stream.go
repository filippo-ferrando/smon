package main

import (
	"sync"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// auAssembler turns H.264 RTP packets (one access unit per marker bit) into
// Annex-B access units.
type auAssembler struct {
	depay codecs.H264Packet // emits Annex-B with start codes
	au    []byte
}

// push feeds one RTP packet and returns a complete access unit when the
// packet ends one. The returned slice is owned by the caller.
func (a *auAssembler) push(pkt []byte) []byte {
	var p rtp.Packet
	if p.Unmarshal(pkt) != nil {
		return nil
	}
	nal, err := a.depay.Unmarshal(p.Payload)
	if err != nil {
		return nil
	}
	a.au = append(a.au, nal...)
	if !p.Marker {
		return nil
	}
	au := a.au
	a.au = nil
	return au
}

// isKeyframe reports whether an Annex-B access unit holds an IDR slice.
func isKeyframe(au []byte) bool {
	for _, n := range splitAnnexB(au) {
		if len(n) > 0 && n[0]&0x1f == 5 {
			return true
		}
	}
	return false
}

// auHub fans access units out to WebSocket viewers. A viewer that falls
// behind skips to the next keyframe instead of queueing latency.
type auHub struct {
	mu   sync.Mutex
	subs map[*auSub]struct{}
}

type auSub struct {
	c       chan []byte // video access units
	a       chan []byte // Opus frames: own queue, so audio never forces a keyframe wait
	waitKey bool
}

func (h *auHub) subscribe() *auSub {
	s := &auSub{c: make(chan []byte, 4), a: make(chan []byte, 16), waitKey: true}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[*auSub]struct{}{}
	}
	h.subs[s] = struct{}{}
	return s
}

func (h *auHub) unsubscribe(s *auSub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, s)
}

// WebSocket message flags (first byte).
const (
	wsKey   = 1 // video keyframe
	wsAudio = 2 // Opus frame, not video
)

// publish sends msg (flags byte + access unit) to every viewer.
func (h *auHub) publish(msg []byte, key bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.waitKey && !key {
			continue
		}
		select {
		case s.c <- msg:
			s.waitKey = false
		default: // viewer is behind: drop until the next keyframe
			s.waitKey = true
		}
	}
}

// publishAudio sends msg (flags byte + Opus frame) to every viewer; a viewer
// that is behind loses the frame.
func (h *auHub) publishAudio(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		select {
		case s.a <- msg:
		default:
		}
	}
}

func (h *auHub) empty() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) == 0
}
