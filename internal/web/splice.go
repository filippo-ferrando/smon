package web

import (
	"encoding/binary"
	"time"
)

// rtpSplice joins the RTP of successive pipelines into one stream. A monitor
// that turns (see monitor.turn) restarts its pipeline at the new size, and
// the new payloader starts its own random sequence numbers and timestamps
// under a new SSRC: a browser would take the jump for loss or reordering.
// fix renumbers every packet to follow on from the last one sent.
type rtpSplice struct {
	started bool
	ssrc    uint32 // the current pipeline's
	seqOff  uint16
	tsOff   uint32
	lastSeq uint16 // last sent, renumbered
	lastTS  uint32
	lastAt  time.Time
}

// rtpVideoClock is the RTP clock rate of every video codec smon sends.
const rtpVideoClock = 90000

// fix renumbers pkt in place, and reports whether it is the first packet of
// a new pipeline's stream. Anything that is not RTP is left alone.
func (s *rtpSplice) fix(pkt []byte, now time.Time) (joined bool) {
	if len(pkt) < 12 || pkt[0]>>6 != 2 {
		return false
	}
	seq := binary.BigEndian.Uint16(pkt[2:])
	ts := binary.BigEndian.Uint32(pkt[4:])
	ssrc := binary.BigEndian.Uint32(pkt[8:])
	if s.started && ssrc != s.ssrc {
		gap := uint32(max(now.Sub(s.lastAt).Microseconds()*rtpVideoClock/1e6, 1)) // the time it took
		s.seqOff = s.lastSeq + 1 - seq
		s.tsOff = s.lastTS + gap - ts
		joined = true
	}
	s.started, s.ssrc = true, ssrc
	seq += s.seqOff
	ts += s.tsOff
	binary.BigEndian.PutUint16(pkt[2:], seq)
	binary.BigEndian.PutUint32(pkt[4:], ts)
	s.lastSeq, s.lastTS, s.lastAt = seq, ts, now
	return joined
}
