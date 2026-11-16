package touch

import "sync"

// Linux codes a pen tablet uses (linux/input-event-codes.h).
const (
	btnToolPen    = 0x140
	btnToolRubber = 0x141
	btnStylus     = 0x14b
	btnStylus2    = 0x14c

	absPressure = 0x18
	absTiltX    = 0x1a
	absTiltY    = 0x1b

	penPressureMax = 4095
)

// Pointer Events button bits the pen reports (PointerEvent.buttons).
const (
	PenBarrel = 2  // the side button
	PenAux    = 4  // a second side button
	PenEraser = 32 // the eraser end
)

// PenEvent is one viewer pen sample: Kind is h(over), d(own), m(ove while
// down), u(p, still near the screen) or l(eave, out of range). X, Y are
// normalised to the shared screen; Pressure is 0..1; TiltX, TiltY are degrees
// (-90..90); Buttons are PointerEvent.buttons.
type PenEvent struct {
	Kind         byte
	X, Y         float64
	Pressure     float64
	TiltX, TiltY float64
	Buttons      int
}

// penState turns viewer pen samples into tablet tool events. It has no I/O so
// it can be tested on its own.
type penState struct {
	w, h     int
	tool     uint16 // btnToolPen, btnToolRubber, or 0 when out of range
	touching bool
	barrel   bool
	aux      bool
}

func clampTilt(v float64) int32 { return int32(max(-90, min(90, v))) }

// events returns the input events for one pen sample, ending in SYN_REPORT
// (nil if there is nothing to do).
func (s *penState) events(e PenEvent) []ev {
	var out []ev
	if e.Kind == 'l' {
		out = s.leave(out)
		if out == nil {
			return nil
		}
		return append(out, ev{evSyn, synReport, 0})
	}
	if e.Kind != 'h' && e.Kind != 'd' && e.Kind != 'm' && e.Kind != 'u' {
		return nil
	}
	want := uint16(btnToolPen)
	if e.Buttons&PenEraser != 0 {
		want = btnToolRubber
	}
	if s.tool != want { // tool change (or coming into range): libinput wants proximity first
		out = s.leave(out)
		s.tool = want
		out = append(out, ev{evKey, want, 1})
	}
	out = append(out,
		ev{evAbs, absX, clampScale(e.X, s.w)},
		ev{evAbs, absY, clampScale(e.Y, s.h)},
		ev{evAbs, absTiltX, clampTilt(e.TiltX)},
		ev{evAbs, absTiltY, clampTilt(e.TiltY)},
	)
	touching := e.Kind == 'd' || e.Kind == 'm' && s.touching
	pressure := int32(0)
	if touching {
		pressure = max(1, int32(max(0, min(1, e.Pressure))*penPressureMax+0.5)) // never 0 while down
	}
	out = append(out, ev{evAbs, absPressure, pressure})
	if barrel := e.Buttons&PenBarrel != 0; barrel != s.barrel {
		s.barrel = barrel
		out = append(out, ev{evKey, btnStylus, b2i(barrel)})
	}
	if aux := e.Buttons&PenAux != 0; aux != s.aux {
		s.aux = aux
		out = append(out, ev{evKey, btnStylus2, b2i(aux)})
	}
	if touching != s.touching {
		s.touching = touching
		out = append(out, ev{evKey, btnTouch, b2i(touching)})
	}
	return append(out, ev{evSyn, synReport, 0})
}

// leave lifts the pen and takes the tool out of range, if it is in range.
func (s *penState) leave(out []ev) []ev {
	if s.tool == 0 {
		return out
	}
	if s.touching {
		out = append(out, ev{evAbs, absPressure, 0}, ev{evKey, btnTouch, 0})
		s.touching = false
	}
	if s.barrel {
		out = append(out, ev{evKey, btnStylus, 0})
		s.barrel = false
	}
	if s.aux {
		out = append(out, ev{evKey, btnStylus2, 0})
		s.aux = false
	}
	out = append(out, ev{evKey, s.tool, 0})
	s.tool = 0
	return out
}

func b2i(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// Pen is a virtual uinput pen tablet (a screen tablet, like a stylus on the
// tablet's display), w x h units in size. One viewer at a time holds it: the
// first to bring a pen in range, until that pen leaves.
type Pen struct {
	mu    sync.Mutex
	w     writer
	st    penState
	owner int // viewer holding the pen; 0: none
}

// NewPen creates the uinput pen tablet dev (PenDevice, or PenDevice.For a
// later monitor) for an output w x h in size.
func NewPen(dev Device, w, h int) (*Pen, error) {
	const res = 10
	f, err := create(spec{
		dev:  dev,
		evs:  []int{evSyn, evKey, evAbs},
		keys: []uint16{btnToolPen, btnToolRubber, btnTouch, btnStylus, btnStylus2},
		abs: []absAxis{
			{absX, 0, int32(w - 1), res},
			{absY, 0, int32(h - 1), res},
			{absPressure, 0, penPressureMax, 0},
			{absTiltX, -90, 90, 0},
			{absTiltY, -90, 90, 0},
		},
		props: []int{inputPropDirect},
	})
	if err != nil {
		return nil, err
	}
	return &Pen{w: writer{f: f, name: "pen"}, st: penState{w: w, h: h}}, nil
}

// Event injects a pen sample from viewer owner.
func (p *Pen) Event(owner int, e PenEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owner != 0 && p.owner != owner {
		return // another viewer's pen is in range
	}
	p.w.write(p.st.events(e))
	if p.st.tool == 0 {
		p.owner = 0
	} else {
		p.owner = owner
	}
}

// Release takes viewer owner's pen out of range, if it holds the pen.
func (p *Pen) Release(owner int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owner == owner {
		p.w.write(p.st.events(PenEvent{Kind: 'l'}))
		p.owner = 0
	}
}

// Close takes the pen out of range and removes the device.
func (p *Pen) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.w.write(p.st.events(PenEvent{Kind: 'l'}))
	p.w.destroy()
}
