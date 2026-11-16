package h264

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// nal returns a NAL unit of type typ with n payload bytes that never form a
// start code.
func nal(typ byte, n int) []byte {
	b := make([]byte, n+1)
	b[0] = 0x60 | typ
	for i := 1; i < len(b); i++ {
		b[i] = byte(1 + rand.IntN(255))
	}
	return b
}

func annexB(nals ...[]byte) []byte {
	var out []byte
	for _, n := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	return out
}

var seq uint16

// packetize splits an Annex-B access unit into RTP packets, marker on the last.
func packetize(t testing.TB, au []byte) [][]byte {
	t.Helper()
	var pay codecs.H264Payloader
	payloads := pay.Payload(1200, au)
	pkts := make([][]byte, len(payloads))
	for i, p := range payloads {
		seq++
		raw, err := (&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Marker: i == len(payloads)-1}, Payload: p}).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		pkts[i] = raw
	}
	return pkts
}

var (
	sps, pps = nal(7, 20), nal(8, 4)
	idrAU    = annexB(nal(9, 1), sps, pps, nal(5, 150_000))
	deltaAU  = annexB(nal(9, 1), nal(1, 30_000))
)

func TestSplitAnnexB(t *testing.T) {
	in := append(annexB(sps), append([]byte{0, 0, 1}, pps...)...)
	got := SplitAnnexB(in)
	if len(got) != 2 || !bytes.Equal(got[0], sps) || !bytes.Equal(got[1], pps) {
		t.Fatalf("got %d NALs", len(got))
	}
}

func TestAssemblerRoundTrip(t *testing.T) {
	var a Assembler
	for i, want := range [][]byte{idrAU, deltaAU, idrAU} {
		a.Head = i % 2
		var au []byte
		var key bool
		for _, p := range packetize(t, want) {
			au, key = a.Push(p)
		}
		if au == nil {
			t.Fatal("no access unit")
		}
		if got, exp := SplitAnnexB(au[a.Head:]), SplitAnnexB(want)[1:]; !slicesEqual(got, exp) { // the payloader drops the AUD
			t.Fatalf("access unit differs: %d NALs, want %d", len(got), len(exp))
		}
		if key != (i != 1) {
			t.Fatalf("key %v, want %v", key, !key)
		}
	}
}

// After Skip, the next whole access unit comes out (the case of a viewer
// joining as the forced IDR starts); one joined midway is dropped silently.
func TestAssemblerSkip(t *testing.T) {
	var a Assembler
	for _, p := range packetize(t, deltaAU) {
		a.Skip(p)
	}
	var au []byte
	var key bool
	for _, p := range packetize(t, idrAU) {
		au, key = a.Push(p)
	}
	if au == nil || !key {
		t.Fatal("IDR after a skipped frame missing")
	}

	idr := packetize(t, idrAU)
	for i, p := range idr {
		if i < len(idr)/2 {
			a.Skip(p)
		} else if au, _ := a.Push(p); au != nil {
			t.Fatal("got a truncated access unit")
		}
	}
	if a.Dropped != 0 {
		t.Errorf("Dropped = %d after joining midway", a.Dropped)
	}
	for _, p := range packetize(t, deltaAU) {
		au, key = a.Push(p)
	}
	if au == nil || key {
		t.Fatal("access unit after joining midway missing")
	}
}

// A lost packet drops its access unit, counted, and the next comes through.
func TestAssemblerLoss(t *testing.T) {
	var a Assembler
	idr := packetize(t, idrAU)
	for i, p := range idr {
		if i == len(idr)/2 {
			continue
		}
		if au, _ := a.Push(p); au != nil {
			t.Fatal("got a truncated access unit")
		}
	}
	if a.Dropped != 1 {
		t.Errorf("Dropped = %d, want 1", a.Dropped)
	}
	var au []byte
	for _, p := range packetize(t, deltaAU) {
		au, _ = a.Push(p)
	}
	if au == nil {
		t.Fatal("access unit after the loss missing")
	}
}

func slicesEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func BenchmarkSplitAnnexB(b *testing.B) {
	b.SetBytes(int64(len(idrAU)))
	for b.Loop() {
		SplitAnnexB(idrAU)
	}
}

func BenchmarkAssembler(b *testing.B) {
	pkts := packetize(b, deltaAU)
	b.SetBytes(int64(len(deltaAU)))
	var a Assembler
	for b.Loop() {
		for _, p := range pkts {
			seq++ // the same packets again, as a gapless stream
			binary.BigEndian.PutUint16(p[2:], seq)
			a.Push(p)
		}
	}
}
