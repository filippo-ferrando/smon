package web

import (
	"sync"
)

// auHub fans access units out to WebSocket viewers. A viewer that falls
// behind skips to the next keyframe instead of queueing latency, and needKey
// (when set) asks the encoder for that keyframe now.
type auHub struct {
	mu      sync.Mutex
	subs    map[*auSub]struct{}
	needKey func()
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
	if h.needKey != nil {
		h.needKey()
	}
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
			if !s.waitKey && h.needKey != nil {
				h.needKey()
			}
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
