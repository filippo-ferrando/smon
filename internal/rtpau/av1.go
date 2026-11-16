package rtpau

// AV1 OBU types (AV1 spec 6.2.2).
const (
	obuSequenceHeader   = 1
	obuTemporalDelim    = 2
	obuHasSizeField     = 0x02
	obuHasExtension     = 0x04
	av1AggZ, av1AggY    = 0x80, 0x40 // first element continues / last one goes on
	av1AggN             = 0x08       // first packet of a coded video sequence
	av1AggWShift, wMask = 4, 3
)

// leb128 reads an unsigned LEB128 value; n is 0 when b holds none.
func leb128(b []byte) (v, n int) {
	for i := 0; i < len(b) && i < 8; i++ {
		v |= int(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

func appendLeb128(b []byte, v int) []byte {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(b, c)
		}
		b = append(b, c|0x80)
	}
}

// depayAV1 reads one packet of AV1's RTP payload format: an aggregation
// header, then OBU elements (length-prefixed except, when W is set, the
// last), the first and last of which may be fragments of an OBU spanning
// packets.
func (a *Assembler) depayAV1(b []byte) {
	if len(b) < 1 {
		a.broken = true
		return
	}
	agg := b[0]
	z, y, w := agg&av1AggZ != 0, agg&av1AggY != 0, int(agg>>av1AggWShift&wMask)
	if agg&av1AggN != 0 {
		a.key = true
	}
	b = b[1:]
	for i := 1; len(b) > 0; i++ {
		var el []byte
		if w == 0 || i < w {
			size, k := leb128(b)
			if k == 0 || len(b) < k+size {
				a.broken = true
				return
			}
			el, b = b[k:k+size], b[k+size:]
		} else {
			el, b = b, nil
		}
		if i == 1 && z { // the rest of the OBU the last packet started
			if !a.inFU {
				a.broken = true
				return
			}
		} else {
			if a.inFU { // that OBU's end was lost
				a.broken = true
			}
			a.obu = a.obu[:0]
			a.inFU = true
		}
		a.obu = append(a.obu, el...)
		if len(b) == 0 && y {
			return // the OBU goes on in the next packet
		}
		a.inFU = false
		a.obuDone(a.obu)
	}
}

// obuDone appends a whole OBU to the access unit in the low-overhead format,
// with obu_size, which RTP leaves out. Temporal delimiters are dropped, as
// the payload format says.
func (a *Assembler) obuDone(o []byte) {
	if len(o) == 0 {
		a.broken = true
		return
	}
	h := o[0]
	hlen := 1
	if h&obuHasExtension != 0 {
		hlen = 2
	}
	if len(o) < hlen {
		a.broken = true
		return
	}
	typ := h >> 3 & 0x0f
	if typ == obuTemporalDelim {
		return
	}
	a.begin()
	if typ == obuSequenceHeader {
		a.key = true
	}
	if h&obuHasSizeField != 0 { // already self-delimiting
		a.au = append(a.au, o...)
		return
	}
	a.au = append(a.au, o[:hlen]...)
	a.au[len(a.au)-hlen] |= obuHasSizeField
	a.au = appendLeb128(a.au, len(o)-hlen)
	a.au = append(a.au, o[hlen:]...)
}
