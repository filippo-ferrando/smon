package web

import (
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
)

// statsEvery is how often viewers get the server's side of the stats overlay.
const statsEvery = time.Second

// streamStats is what the server knows about the stream; the viewer adds what
// its browser measures (fps, received rate, round trip, loss).
type streamStats struct {
	T       string `json:"t"` // always "stats"
	Encoder string `json:"enc"`
	Codec   string `json:"codec"`
	Kbps    int    `json:"kbps"` // encoder bitrate now
	Max     int    `json:"max"`  // -kbps
	Size    string `json:"size"`
	FPS     int    `json:"fps"`
	Viewers int64  `json:"viewers"`
	Mon     string `json:"mon,omitempty"` // the output's name
}

// statsSource builds the stats message for one stream.
type statsSource struct {
	encoder, codec, size string
	mon                  func() string // the output's name, which GNOME learns late; nil: none
	fps, kbps            int
	abr                  *rateCtl // nil: fixed bitrate
	viewers              atomic.Int64
}

func (s *statsSource) message() []byte {
	m := streamStats{T: "stats", Encoder: s.encoder, Codec: s.codec, Kbps: s.kbps, Max: s.kbps, Size: s.size, FPS: s.fps, Viewers: s.viewers.Load()}
	if s.mon != nil {
		m.Mon = s.mon()
	}
	if s.abr != nil {
		m.Kbps, m.Max = s.abr.current()
	}
	b, _ := json.Marshal(m)
	return b
}

// sendStats sends the stats on a viewer's control channel every statsEvery
// until the channel closes.
func sendStats(dc *webrtc.DataChannel, src *statsSource) {
	t := time.NewTicker(statsEvery)
	defer t.Stop()
	for range t.C {
		if dc.ReadyState() != webrtc.DataChannelStateOpen || dc.SendText(string(src.message())) != nil {
			return
		}
	}
}
