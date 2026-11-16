// Package h264 handles H.264 access units in Annex-B form.
package h264

import (
	"bytes"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// Assembler turns H.264 RTP packets (one access unit per marker bit) into
// Annex-B access units.
type Assembler struct {
	depay codecs.H264Packet // emits Annex-B with start codes
	au    []byte
}

// Push feeds one RTP packet and returns a complete access unit when the
// packet ends one. The returned slice is owned by the caller.
func (a *Assembler) Push(pkt []byte) []byte {
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

// SplitAnnexB returns the NAL units of an Annex-B byte stream, without their
// start codes.
func SplitAnnexB(b []byte) [][]byte {
	var out [][]byte
	start := -1
	for i := 0; i+3 <= len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				out = append(out, bytes.TrimRight(b[start:i], "\x00"))
			}
			start = i + 3
			i += 2
		}
	}
	if start >= 0 && start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// IsKeyframe reports whether an Annex-B access unit holds an IDR slice.
func IsKeyframe(au []byte) bool {
	for _, n := range SplitAnnexB(au) {
		if len(n) > 0 && n[0]&0x1f == 5 {
			return true
		}
	}
	return false
}
