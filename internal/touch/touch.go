// Package touch provides a virtual uinput touchscreen fed by viewer pointer
// events.
package touch

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux input constants (linux/input-event-codes.h, linux/uinput.h).
const (
	evSyn = 0x00
	evKey = 0x01
	evAbs = 0x03

	synReport = 0x00
	btnTouch  = 0x14a

	absX           = 0x00
	absY           = 0x01
	absMTSlot      = 0x2f
	absMTPositionX = 0x35
	absMTPositionY = 0x36
	absMTTrackID   = 0x39

	inputPropDirect = 0x01
	busVirtual      = 0x06

	uiSetEvBit   = 0x40045564 // _IOW('U', 100, int)
	uiSetKeyBit  = 0x40045565 // _IOW('U', 101, int)
	uiSetAbsBit  = 0x40045567 // _IOW('U', 103, int)
	uiSetPropBit = 0x4004556e // _IOW('U', 110, int)
	uiDevSetup   = 0x405c5503 // _IOW('U', 3, struct uinput_setup)
	uiAbsSetup   = 0x401c5504 // _IOW('U', 4, struct uinput_abs_setup)
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502

	touchSlots = 10
)

// ev is one input_event without its timestamp (the kernel fills it in).
type ev struct {
	typ, code uint16
	val       int32
}

// touchState turns viewer pointer events into multitouch protocol B events.
// It has no I/O so it can be tested on its own.
type touchState struct {
	w, h   int
	ids    [touchSlots]int // viewer pointer id per slot
	active [touchSlots]bool
	nextID int32 // tracking id
	slot   int   // slot the device currently points at (-1: unknown)
}

func newTouchState(w, h int) *touchState { return &touchState{w: w, h: h, slot: -1} }

func (s *touchState) find(id int) int {
	for i := range s.ids {
		if s.active[i] && s.ids[i] == id {
			return i
		}
	}
	return -1
}

func (s *touchState) count() int {
	n := 0
	for _, a := range s.active {
		if a {
			n++
		}
	}
	return n
}

func clampScale(v float64, size int) int32 {
	p := int32(v * float64(size-1))
	return max(0, min(p, int32(size-1)))
}

func (s *touchState) selectSlot(out []ev, i int) []ev {
	if s.slot != i {
		s.slot = i
		out = append(out, ev{evAbs, absMTSlot, int32(i)})
	}
	return out
}

// events handles kind 'd' (down), 'm' (move) or 'u' (up) for viewer pointer
// id at normalised x, y and returns the events to write, ending in SYN_REPORT
// (nil if there is nothing to do).
func (s *touchState) events(kind byte, id int, x, y float64) []ev {
	var out []ev
	before := s.count()
	px, py := clampScale(x, s.w), clampScale(y, s.h)
	i := s.find(id)
	switch kind {
	case 'd':
		if i < 0 {
			for j := range s.active {
				if !s.active[j] {
					i = j
					break
				}
			}
			if i < 0 {
				return nil // out of slots
			}
			s.active[i], s.ids[i] = true, id
			out = s.selectSlot(out, i)
			out = append(out, ev{evAbs, absMTTrackID, s.nextID})
			s.nextID = (s.nextID + 1) & 0xffff
		} else {
			out = s.selectSlot(out, i)
		}
		out = append(out, ev{evAbs, absMTPositionX, px}, ev{evAbs, absMTPositionY, py})
	case 'm':
		if i < 0 {
			return nil
		}
		out = s.selectSlot(out, i)
		out = append(out, ev{evAbs, absMTPositionX, px}, ev{evAbs, absMTPositionY, py})
	case 'u':
		if i < 0 {
			return nil
		}
		out = s.selectSlot(out, i)
		out = append(out, ev{evAbs, absMTTrackID, -1})
		s.active[i] = false
	default:
		return nil
	}
	after := s.count()
	if before == 0 && after > 0 {
		out = append(out, ev{evKey, btnTouch, 1})
	}
	if after > 0 && kind != 'u' && s.first() == i { // single-touch axes follow the first contact
		out = append(out, ev{evAbs, absX, px}, ev{evAbs, absY, py})
	}
	if before > 0 && after == 0 {
		out = append(out, ev{evKey, btnTouch, 0})
	}
	return append(out, ev{evSyn, synReport, 0})
}

func (s *touchState) first() int {
	for i, a := range s.active {
		if a {
			return i
		}
	}
	return -1
}

// release lifts every contact whose pointer id matches (a viewer went away
// mid-gesture); nil match lifts all of them.
func (s *touchState) release(match func(id int) bool) []ev {
	var out []ev
	for i, a := range s.active {
		if a && (match == nil || match(s.ids[i])) {
			out = s.selectSlot(out, i)
			out = append(out, ev{evAbs, absMTTrackID, -1})
			s.active[i] = false
		}
	}
	if out == nil {
		return nil
	}
	if s.count() == 0 {
		out = append(out, ev{evKey, btnTouch, 0})
	}
	return append(out, ev{evSyn, synReport, 0})
}

// The touchscreen's identity, which compositors use to map it to an output.
const (
	Vendor  = 0x1209 // pid.codes test vendor
	Product = 0x5340
	Name    = "smon touchscreen"
)

// Touch is a virtual uinput touchscreen sized w x h. The compositor maps it to
// one output (see compositor.MapInput).
type Touch struct {
	mu sync.Mutex
	w  writer
	st *touchState
}

func ioctlInt(fd uintptr, req uint, v int) error {
	return unix.IoctlSetInt(int(fd), req, v)
}

func ioctlPtr(fd uintptr, req uint, p unsafe.Pointer) error {
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, fd, uintptr(req), uintptr(p)); e != 0 {
		return e
	}
	return nil
}

// New creates the uinput touchscreen, w x h units in size.
func New(w, h int) (*Touch, error) {
	const res = 10 // nominal; only needs to be nonzero
	f, err := create(spec{
		dev:  TouchDevice,
		evs:  []int{evSyn, evKey, evAbs},
		keys: []uint16{btnTouch},
		abs: []absAxis{
			{absX, 0, int32(w - 1), res},
			{absY, 0, int32(h - 1), res},
			{absMTPositionX, 0, int32(w - 1), res},
			{absMTPositionY, 0, int32(h - 1), res},
			{absMTSlot, 0, touchSlots - 1, 0},
			{absMTTrackID, 0, 0xffff, 0},
		},
		props: []int{inputPropDirect},
	})
	if err != nil {
		return nil, err
	}
	return &Touch{w: writer{f: f, name: "touch"}, st: newTouchState(w, h)}, nil
}

// Event injects pointer id's event from viewer owner; x, y are normalised to
// 0..1. Pointer ids are namespaced per viewer so two tablets can't collide.
func (t *Touch) Event(owner int, kind byte, id int, x, y float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.w.write(t.st.events(kind, owner<<16|id&0xffff, x, y))
}

// Release lifts all contacts of viewer owner.
func (t *Touch) Release(owner int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.w.write(t.st.release(func(id int) bool { return id>>16 == owner }))
}

// Close lifts every contact and removes the device.
func (t *Touch) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.w.write(t.st.release(nil))
	t.w.destroy()
}
