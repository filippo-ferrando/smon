package web

import (
	"encoding/json"

	"smon/internal/touch"
)

// inputs are the virtual devices viewer input drives; any may be nil.
type inputs struct {
	touch *touch.Touch
	pen   *touch.Pen
	kbd   *touch.Keyboard
}

func (in *inputs) enabled() bool {
	return in != nil && (in.touch != nil || in.pen != nil || in.kbd != nil)
}

// inputMsg is one viewer input event.
//
//   - Touch: t is d(own), m(ove) or u(p); id is the pointer; x, y are
//     normalised to the shared screen.
//   - Pen (pt "pen"): t is also h(over) or l(eave); p is pressure 0..1, tx, ty
//     tilt in degrees, b the PointerEvent buttons.
//   - Key: t is k; c is KeyboardEvent.code, d 1 for down, 0 for up.
//   - Text from an on-screen keyboard: t is s; s is the text.
type inputMsg struct {
	T      string
	ID     int
	X, Y   float64
	PT     string
	P      float64
	TX, TY float64
	B      int
	C      string
	D      int
	S      string
}

// maxText bounds one text message: an on-screen keyboard sends a word or a
// paste, not a document.
const maxText = 1024

// handle injects one viewer message from owner into the device it is for.
// Messages for a device that is off are dropped.
func (in *inputs) handle(owner int, data []byte) {
	var m inputMsg
	if json.Unmarshal(data, &m) != nil || len(m.T) != 1 {
		return
	}
	switch {
	case m.T == "k":
		if in.kbd != nil {
			in.kbd.Key(owner, m.C, m.D == 1)
		}
	case m.T == "s":
		if in.kbd != nil && len(m.S) <= maxText {
			in.kbd.Text(m.S)
		}
	case m.PT == "pen":
		if in.pen != nil {
			in.pen.Event(owner, touch.PenEvent{Kind: m.T[0], X: m.X, Y: m.Y, Pressure: m.P, TiltX: m.TX, TiltY: m.TY, Buttons: m.B})
		}
	default:
		if in.touch != nil {
			in.touch.Event(owner, m.T[0], m.ID, m.X, m.Y)
		}
	}
}

// release lets go of everything viewer owner was touching or pressing.
func (in *inputs) release(owner int) {
	if in.touch != nil {
		in.touch.Release(owner)
	}
	if in.pen != nil {
		in.pen.Release(owner)
	}
	if in.kbd != nil {
		in.kbd.Release(owner)
	}
}
