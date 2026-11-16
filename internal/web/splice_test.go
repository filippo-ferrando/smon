package web

import (
	"encoding/binary"
	"testing"
	"time"
)

func rtpPkt(ssrc uint32, seq uint16, ts uint32) []byte {
	p := make([]byte, 16)
	p[0] = 0x80
	binary.BigEndian.PutUint16(p[2:], seq)
	binary.BigEndian.PutUint32(p[4:], ts)
	binary.BigEndian.PutUint32(p[8:], ssrc)
	return p
}

func seqTS(p []byte) (uint16, uint32) {
	return binary.BigEndian.Uint16(p[2:]), binary.BigEndian.Uint32(p[4:])
}

func TestRTPSplice(t *testing.T) {
	var s rtpSplice
	t0 := time.Unix(1000, 0)
	// The first pipeline passes through untouched, across a wrap.
	for i, c := range []struct {
		seq uint16
		ts  uint32
	}{{65534, 4294967000}, {65535, 4294967000}, {0, 100}} {
		p := rtpPkt(1, c.seq, c.ts)
		if s.fix(p, t0.Add(time.Duration(i)*time.Millisecond)) {
			t.Fatalf("packet %d: joined", i)
		}
		if seq, ts := seqTS(p); seq != c.seq || ts != c.ts {
			t.Fatalf("packet %d: %d/%d, want unchanged %d/%d", i, seq, ts, c.seq, c.ts)
		}
	}
	// A new pipeline 500 ms later: new SSRC and random numbers.
	p := rtpPkt(2, 12345, 777)
	if !s.fix(p, t0.Add(502*time.Millisecond)) {
		t.Fatal("new SSRC: not joined")
	}
	if seq, ts := seqTS(p); seq != 1 || ts != 100+45000 {
		t.Fatalf("first of the new stream: %d/%d, want 1/%d", seq, ts, 100+45000)
	}
	p = rtpPkt(2, 12346, 777+3000)
	if s.fix(p, t0.Add(535*time.Millisecond)) {
		t.Fatal("same SSRC: joined again")
	}
	if seq, ts := seqTS(p); seq != 2 || ts != 100+45000+3000 {
		t.Fatalf("second of the new stream: %d/%d", seq, ts)
	}
	// Not RTP: untouched.
	junk := []byte{1, 2, 3}
	if s.fix(junk, t0) || junk[0] != 1 {
		t.Fatal("touched a non-RTP packet")
	}
}
