package rtpau

// H.265 NAL unit types (H.265 table 7-1) and RTP payload structures
// (RFC 7798).
const (
	h265IRAPFirst = 16 // BLA_W_LP .. CRA_NUT: random access points
	h265IRAPLast  = 21
	h265AP        = 48 // aggregation packet
	h265FU        = 49 // fragmentation unit
)

func h265Type(b0 byte) byte { return b0 >> 1 & 0x3f }

func (a *Assembler) nalH265(typ byte) {
	a.begin()
	a.key = a.key || typ >= h265IRAPFirst && typ <= h265IRAPLast
}

// depayH265 appends one RFC 7798 payload (without DONL: smon's payloader
// never interleaves) to the access unit.
func (a *Assembler) depayH265(b []byte) {
	if len(b) < 3 {
		a.broken = true
		return
	}
	switch typ := h265Type(b[0]); typ {
	case h265AP:
		a.inFU = false
		for b = b[2:]; len(b) >= 2; {
			n := int(b[0])<<8 | int(b[1])
			if n < 2 || len(b) < 2+n {
				a.broken = true
				return
			}
			a.nalH265(h265Type(b[2]))
			a.au = append(append(a.au, startCode...), b[2:2+n]...)
			b = b[2+n:]
		}
	case h265FU:
		fu := b[2]
		start, end, fuType := fu&0x80 != 0, fu&0x40 != 0, fu&0x3f
		switch {
		case start:
			if a.inFU {
				a.broken = true
			}
			a.nalH265(fuType)
			// The NAL header: F and layer id from the payload header, type
			// from the FU header, then the TID byte.
			a.au = append(append(a.au, startCode...), b[0]&0x81|fuType<<1, b[1])
		case !a.inFU:
			a.broken = true
			return
		}
		a.au = append(a.au, b[3:]...)
		a.inFU = !end
	default:
		if typ > h265FU { // PACI and reserved types
			a.broken = true
			return
		}
		a.inFU = false
		a.nalH265(typ)
		a.au = append(append(a.au, startCode...), b...)
	}
}
