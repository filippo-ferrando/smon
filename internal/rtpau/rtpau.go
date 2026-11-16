// Package rtpau reassembles video access units from RTP: H.264 and H.265 as
// Annex-B, AV1 as a low-overhead OBU stream (what WebCodecs decodes).
package rtpau

import (
	"bytes"
	"encoding/binary"

	"github.com/pion/rtp"
)

var startCode = []byte{0, 0, 0, 1}

// NAL unit types (H.264 table 7-1) and RTP payload structures (RFC 6184).
const (
	nalIDR   = 5
	nalSTAPA = 24
	nalFUA   = 28
)

// Codec is the RTP payload format an Assembler reads.
type Codec int

const (
	H264 Codec = iota // RFC 6184
	H265              // RFC 7798
	AV1               // AOM's RTP payload format for AV1
)

// Assembler turns H.264 RTP packets (one access unit per marker bit) into
// Annex-B access units. It depacketizes straight into the access unit, so an
// access unit costs one allocation however many packets carry it.
type Assembler struct {
	// Codec is the payload format; the zero value is H.264.
	Codec Codec
	// Head is the number of zero bytes Push leaves in front of each access
	// unit, room for a caller's header without copying the access unit again.
	Head int
	// Dropped counts access units dropped for a missing packet.
	Dropped int

	au     []byte
	key    bool   // au holds an IDR slice (H.265: IRAP picture; AV1: sequence header)
	broken bool   // a NAL lost its start: drop au at the marker
	inFU   bool   // inside a fragmented NAL (AV1: OBU)
	obu    []byte // AV1: the OBU being reassembled
	resync bool   // after Skip: the partial access unit isn't a loss
	last   int    // size of the previous access unit, to size the next
	seq    uint16
	hasSeq bool
}

// Push feeds one RTP packet and returns a complete access unit when the
// packet ends one, and whether it holds an IDR slice. The returned slice is
// owned by the caller. An access unit missing a packet (or joined midway,
// after Skip) is dropped rather than returned truncated.
func (a *Assembler) Push(pkt []byte) (au []byte, key bool) {
	var p rtp.Packet
	if p.Unmarshal(pkt) != nil {
		a.broken = true
		return nil, false
	}
	if a.hasSeq && p.SequenceNumber != a.seq+1 { // lost (or reordered) packet
		a.broken = true
	}
	a.seq, a.hasSeq = p.SequenceNumber, true
	switch a.Codec {
	case H265:
		a.depayH265(p.Payload)
	case AV1:
		a.depayAV1(p.Payload)
	default:
		a.depay(p.Payload)
	}
	if !p.Marker {
		return nil, false
	}
	au, key = a.au, a.key
	if a.broken || a.inFU || len(au) <= a.Head {
		if a.broken && !a.resync {
			a.Dropped++
		}
		au, key = nil, false
	} else {
		a.last = len(au)
	}
	a.au, a.key, a.broken, a.inFU, a.resync = nil, false, false, false, false
	return au, key
}

// Skip passes over one RTP packet without assembling it, reading only its
// header, so Push can pick up again at the next access unit: the one after a
// skipped marker packet comes out whole, and one Push joins midway is dropped
// (without counting in Dropped).
func (a *Assembler) Skip(pkt []byte) {
	if len(pkt) < 4 {
		return
	}
	a.au, a.key, a.inFU = nil, false, false
	a.seq, a.hasSeq = binary.BigEndian.Uint16(pkt[2:]), true
	mid := pkt[1]&0x80 == 0 // not the last packet of an access unit
	a.broken, a.resync = mid, mid
}

// begin makes room for the access unit when its first unit arrives.
func (a *Assembler) begin() {
	if a.au == nil {
		a.au = make([]byte, a.Head, a.Head+a.last+a.last/4+64)
	}
}

func (a *Assembler) nal(typ byte) {
	a.begin()
	a.key = a.key || typ == nalIDR
}

func (a *Assembler) depay(b []byte) {
	if len(b) == 0 {
		return
	}
	switch typ := b[0] & 0x1f; typ {
	case nalSTAPA:
		a.inFU = false
		for b = b[1:]; len(b) >= 2; {
			n := int(b[0])<<8 | int(b[1])
			if n == 0 || len(b) < 2+n {
				a.broken = true
				return
			}
			a.nal(b[2] & 0x1f)
			a.au = append(append(a.au, startCode...), b[2:2+n]...)
			b = b[2+n:]
		}
	case nalFUA:
		if len(b) < 2 {
			a.broken = true
			return
		}
		start, end := b[1]&0x80 != 0, b[1]&0x40 != 0
		switch {
		case start:
			if a.inFU { // the previous fragment's end was lost
				a.broken = true
			}
			a.nal(b[1] & 0x1f)
			a.au = append(append(a.au, startCode...), b[0]&0xe0|b[1]&0x1f)
		case !a.inFU: // continuation of a NAL whose start we never saw
			a.broken = true
			return
		}
		a.au = append(a.au, b[2:]...)
		a.inFU = !end
	default:
		if typ == 0 || typ > nalSTAPA { // reserved, STAP-B, MTAP, FU-B
			a.broken = true
			return
		}
		a.inFU = false
		a.nal(typ)
		a.au = append(append(a.au, startCode...), b...)
	}
}

// SplitAnnexB returns the NAL units of an Annex-B byte stream, without their
// start codes.
func SplitAnnexB(b []byte) [][]byte {
	var out [][]byte
	for n := range nals(b) {
		out = append(out, n)
	}
	return out
}

// nals yields the NAL units of an Annex-B byte stream, without start codes.
func nals(b []byte) func(yield func([]byte) bool) {
	return func(yield func([]byte) bool) {
		i := bytes.Index(b, startCode[1:])
		for i >= 0 {
			b = b[i+3:]
			next := bytes.Index(b, startCode[1:])
			n := b
			if next >= 0 {
				n = bytes.TrimRight(b[:next], "\x00")
			}
			if len(n) > 0 && !yield(n) {
				return
			}
			i = next
		}
	}
}
