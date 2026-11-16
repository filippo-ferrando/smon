package rtpau

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
)

// rtpPackets wraps payloads in RTP packets, marker on the last.
func rtpPackets(t testing.TB, payloads [][]byte) [][]byte {
	t.Helper()
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

// nal265 returns an H.265 NAL unit of type typ with n payload bytes that
// never form a start code.
func nal265(typ byte, n int) []byte {
	b := make([]byte, n+2)
	b[0], b[1] = typ<<1, 1 // layer 0, TID 1
	for i := 2; i < len(b); i++ {
		b[i] = byte(1 + rand.IntN(255))
	}
	return b
}

func TestH265RoundTrip(t *testing.T) {
	vps, sps, pps := nal265(32, 20), nal265(33, 30), nal265(34, 6)
	idr := annexB(vps, sps, pps, nal265(19, 120_000)) // IDR_W_RADL
	delta := annexB(nal265(1, 20_000))                // TRAIL_R
	a := Assembler{Codec: H265, Head: 1}
	for i, want := range [][]byte{idr, delta, annexB(nal265(1, 300))} {
		var pay codecs.H265Payloader
		var au []byte
		var key bool
		for _, p := range rtpPackets(t, pay.Payload(1200, want)) {
			au, key = a.Push(p)
		}
		if au == nil {
			t.Fatalf("au %d missing", i)
		}
		if got, exp := SplitAnnexB(au[1:]), SplitAnnexB(want); !slicesEqual(got, exp) {
			t.Fatalf("au %d: %d NALs, want %d", i, len(got), len(exp))
		}
		if key != (i == 0) {
			t.Fatalf("au %d: key %v", i, key)
		}
	}
}

func TestH265Loss(t *testing.T) {
	var pay codecs.H265Payloader
	a := Assembler{Codec: H265}
	pkts := rtpPackets(t, pay.Payload(1200, annexB(nal265(19, 50_000))))
	for i, p := range pkts {
		if i == 3 {
			continue
		}
		if au, _ := a.Push(p); au != nil {
			t.Fatal("truncated access unit came out")
		}
	}
	if a.Dropped != 1 {
		t.Fatalf("Dropped = %d", a.Dropped)
	}
}

// obu builds a low-overhead OBU (with obu_size) of type typ.
func obu(typ byte, n int) []byte {
	b := []byte{typ<<3 | obuHasSizeField}
	b = appendLeb128(b, n)
	for range n {
		b = append(b, byte(rand.IntN(256)))
	}
	return b
}

func TestAV1RoundTrip(t *testing.T) {
	seqHdr, frame := obu(obuSequenceHeader, 12), obu(6, 40_000) // 6: OBU_FRAME
	small := obu(6, 500)
	a := Assembler{Codec: AV1, Head: 1}
	for i, c := range []struct {
		in, want []byte
		key      bool
	}{
		{append(append(obu(obuTemporalDelim, 0), seqHdr...), frame...), append(append([]byte{}, seqHdr...), frame...), true},
		{append(obu(obuTemporalDelim, 0), small...), small, false},
	} {
		var pay codecs.AV1Payloader
		var au []byte
		var key bool
		for _, p := range rtpPackets(t, pay.Payload(1200, c.in)) {
			au, key = a.Push(p)
		}
		if au == nil {
			t.Fatalf("tu %d missing", i)
		}
		if !bytes.Equal(au[1:], c.want) {
			t.Fatalf("tu %d: %d bytes, want %d (temporal delimiter dropped, sizes restored)", i, len(au)-1, len(c.want))
		}
		if key != c.key {
			t.Fatalf("tu %d: key %v", i, key)
		}
	}
}

func TestAV1Loss(t *testing.T) {
	var pay codecs.AV1Payloader
	a := Assembler{Codec: AV1}
	pkts := rtpPackets(t, pay.Payload(1200, obu(6, 30_000)))
	for i, p := range pkts {
		if i == 2 {
			continue
		}
		if au, _ := a.Push(p); au != nil {
			t.Fatal("truncated temporal unit came out")
		}
	}
	if a.Dropped != 1 {
		t.Fatalf("Dropped = %d", a.Dropped)
	}
	var au []byte
	for _, p := range rtpPackets(t, pay.Payload(1200, obu(6, 800))) {
		au, _ = a.Push(p)
	}
	if au == nil {
		t.Fatal("next temporal unit missing")
	}
}

func TestLeb128(t *testing.T) {
	for _, v := range []int{0, 1, 127, 128, 300, 40_000, 1 << 21} {
		b := appendLeb128(nil, v)
		got, n := leb128(b)
		if got != v || n != len(b) {
			t.Errorf("%d: got %d (%d bytes of %d)", v, got, n, len(b))
		}
	}
}
