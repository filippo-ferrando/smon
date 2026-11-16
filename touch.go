package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
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

// Touch is a virtual uinput touchscreen sized w x h. The compositor maps it to
// an output through input.touch.map_to_output.
type Touch struct {
	mu     sync.Mutex
	f      *os.File
	st     *touchState
	warned bool
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

func newTouch(w, h int) (*Touch, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("touch input needs write access to /dev/uinput: %w", err)
	}
	fd := f.Fd()
	fail := func(what string, err error) (*Touch, error) {
		f.Close()
		return nil, fmt.Errorf("uinput %s: %w", what, err)
	}
	for _, b := range []struct {
		req uint
		v   int
	}{
		{uiSetEvBit, evSyn}, {uiSetEvBit, evKey}, {uiSetEvBit, evAbs},
		{uiSetKeyBit, btnTouch},
		{uiSetAbsBit, absX}, {uiSetAbsBit, absY},
		{uiSetAbsBit, absMTSlot}, {uiSetAbsBit, absMTTrackID},
		{uiSetAbsBit, absMTPositionX}, {uiSetAbsBit, absMTPositionY},
		{uiSetPropBit, inputPropDirect},
	} {
		if err := ioctlInt(fd, b.req, b.v); err != nil {
			return fail("set bit", err)
		}
	}
	// struct uinput_abs_setup { __u16 code; struct input_absinfo { s32 value, min, max, fuzz, flat, res; } }
	abs := func(code uint16, maxv, res int32) error {
		var b [28]byte
		binary.LittleEndian.PutUint16(b[0:], code)
		binary.LittleEndian.PutUint32(b[4+8:], uint32(maxv)) // maximum
		binary.LittleEndian.PutUint32(b[4+20:], uint32(res)) // resolution (units/mm)
		return ioctlPtr(fd, uiAbsSetup, unsafe.Pointer(&b[0]))
	}
	const res = 10 // nominal; only needs to be nonzero
	for _, a := range []struct {
		code uint16
		max  int32
		res  int32
	}{
		{absX, int32(w - 1), res}, {absY, int32(h - 1), res},
		{absMTPositionX, int32(w - 1), res}, {absMTPositionY, int32(h - 1), res},
		{absMTSlot, touchSlots - 1, 0}, {absMTTrackID, 0xffff, 0},
	} {
		if err := abs(a.code, a.max, a.res); err != nil {
			return fail("abs setup", err)
		}
	}
	// struct uinput_setup { struct input_id { u16 bustype, vendor, product, version; } char name[80]; u32 ff_effects_max; }
	var setup [92]byte
	binary.LittleEndian.PutUint16(setup[0:], busVirtual)
	binary.LittleEndian.PutUint16(setup[2:], 0x1209) // pid.codes test vendor
	binary.LittleEndian.PutUint16(setup[4:], 0x5340)
	binary.LittleEndian.PutUint16(setup[6:], 1)
	copy(setup[8:87], "smon touchscreen")
	if err := ioctlPtr(fd, uiDevSetup, unsafe.Pointer(&setup[0])); err != nil {
		return fail("dev setup", err)
	}
	if err := ioctlPtr(fd, uiDevCreate, nil); err != nil {
		return fail("dev create", err)
	}
	return &Touch{f: f, st: newTouchState(w, h)}, nil
}

func (t *Touch) write(evs []ev) {
	if len(evs) == 0 {
		return
	}
	buf := make([]byte, 0, 24*len(evs))
	for _, e := range evs { // struct input_event (64-bit): timeval[16], u16 type, u16 code, s32 value
		buf = append(buf, make([]byte, 16)...)
		buf = binary.LittleEndian.AppendUint16(buf, e.typ)
		buf = binary.LittleEndian.AppendUint16(buf, e.code)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(e.val))
	}
	if _, err := t.f.Write(buf); err != nil && !t.warned {
		t.warned = true
		log.Printf("touch: uinput write: %v", err)
	}
}

// Event injects pointer id's event from viewer owner; x, y are normalised to
// 0..1. Pointer ids are namespaced per viewer so two tablets can't collide.
func (t *Touch) Event(owner int, kind byte, id int, x, y float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.write(t.st.events(kind, owner<<16|id&0xffff, x, y))
}

// Release lifts all contacts of viewer owner.
func (t *Touch) Release(owner int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.write(t.st.release(func(id int) bool { return id>>16 == owner }))
}

func (t *Touch) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.write(t.st.release(nil))
	ioctlPtr(t.f.Fd(), uiDevDestroy, nil)
	t.f.Close()
}
