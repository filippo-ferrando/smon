package touch

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Device is the identity of one of smon's virtual input devices. Compositors
// use it to map the device to an output, or to find it.
type Device struct {
	Name            string
	Vendor, Product uint16
	Pen             bool // a pen tablet; otherwise a touchscreen (or keyboard)
	Monitor         int  // the smon monitor it drives; 0 or 1 is the first
}

// For returns the device for smon's monitor n. Monitor 1 keeps the plain
// identity; monitor n gets " n" after the name and its own product id
// (GNOME keys settings by vendor:product), 0x10 apart.
func (d Device) For(n int) Device {
	if n <= 1 {
		return d
	}
	d.Name = fmt.Sprintf("%s %d", d.Name, n)
	d.Product += uint16(0x10 * (n - 1))
	d.Monitor = n
	return d
}

// smon's input devices. They share pid.codes' test vendor id.
var (
	TouchDevice    = Device{Name: Name, Vendor: Vendor, Product: Product}
	PenDevice      = Device{Name: "smon pen", Vendor: Vendor, Product: 0x5341, Pen: true}
	KeyboardDevice = Device{Name: "smon keyboard", Vendor: Vendor, Product: 0x5342}
)

// Sway is the device as Sway identifies inputs: vendor:product:name, decimal,
// spaces as underscores.
func (d Device) Sway() string {
	return fmt.Sprintf("%d:%d:%s", d.Vendor, d.Product, strings.ReplaceAll(d.Name, " ", "_"))
}

// Hyprland is the device name as Hyprland spells it: lowercase, spaces as
// dashes.
func (d Device) Hyprland() string { return strings.ReplaceAll(strings.ToLower(d.Name), " ", "-") }

// absAxis describes one absolute axis of a uinput device.
type absAxis struct {
	code     uint16
	min, max int32
	res      int32
}

// spec is everything uinput needs to create a device.
type spec struct {
	dev   Device
	evs   []int
	keys  []uint16
	abs   []absAxis
	props []int
}

// create opens /dev/uinput and creates the device described by sp.
func create(sp spec) (*os.File, error) {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("input needs write access to /dev/uinput: %w", err)
	}
	fd := f.Fd()
	fail := func(what string, err error) (*os.File, error) {
		_ = f.Close()
		return nil, fmt.Errorf("uinput %s (%s): %w", what, sp.dev.Name, err)
	}
	set := func(req uint, v int) error { return ioctlInt(fd, req, v) }
	for _, e := range sp.evs {
		if err := set(uiSetEvBit, e); err != nil {
			return fail("set ev bit", err)
		}
	}
	for _, k := range sp.keys {
		if err := set(uiSetKeyBit, int(k)); err != nil {
			return fail("set key bit", err)
		}
	}
	for _, a := range sp.abs {
		if err := set(uiSetAbsBit, int(a.code)); err != nil {
			return fail("set abs bit", err)
		}
	}
	for _, p := range sp.props {
		if err := set(uiSetPropBit, p); err != nil {
			return fail("set prop bit", err)
		}
	}
	// struct uinput_abs_setup { __u16 code; struct input_absinfo { s32 value, min, max, fuzz, flat, res; } }
	for _, a := range sp.abs {
		var b [28]byte
		binary.LittleEndian.PutUint16(b[0:], a.code)
		binary.LittleEndian.PutUint32(b[4+4:], uint32(a.min))
		binary.LittleEndian.PutUint32(b[4+8:], uint32(a.max))
		binary.LittleEndian.PutUint32(b[4+20:], uint32(a.res))
		if err := ioctlPtr(fd, uiAbsSetup, unsafe.Pointer(&b[0])); err != nil {
			return fail("abs setup", err)
		}
	}
	// struct uinput_setup { struct input_id { u16 bustype, vendor, product, version; } char name[80]; u32 ff_effects_max; }
	var setup [92]byte
	binary.LittleEndian.PutUint16(setup[0:], busVirtual)
	binary.LittleEndian.PutUint16(setup[2:], sp.dev.Vendor)
	binary.LittleEndian.PutUint16(setup[4:], sp.dev.Product)
	binary.LittleEndian.PutUint16(setup[6:], 1)
	copy(setup[8:87], sp.dev.Name)
	if err := ioctlPtr(fd, uiDevSetup, unsafe.Pointer(&setup[0])); err != nil {
		return fail("dev setup", err)
	}
	if err := ioctlPtr(fd, uiDevCreate, nil); err != nil {
		return fail("dev create", err)
	}
	return f, nil
}

// writer writes input events to a uinput device, logging the first failure.
type writer struct {
	f      *os.File
	name   string
	warned bool
}

func (w *writer) write(evs []ev) {
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
	if _, err := w.f.Write(buf); err != nil && !w.warned {
		w.warned = true
		log.Printf("%s: uinput write: %v", w.name, err)
	}
}

func (w *writer) destroy() {
	_ = ioctlPtr(w.f.Fd(), uiDevDestroy, nil)
	_ = w.f.Close()
}
