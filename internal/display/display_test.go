package display

import (
	"testing"

	"smon/internal/compositor"

	"github.com/godbus/dbus/v5"
)

// The portal's "size" is a D-Bus (ii) struct, which godbus hands back as []any.
func TestMirrorSize(t *testing.T) {
	d := &Display{W: 1920, H: 1080}
	props := map[string]dbus.Variant{"size": dbus.MakeVariant([]any{int32(1921), int32(1200)}), "mapping_id": dbus.MakeVariant("eDP-1")}
	if err := d.checkSource("mirror", props); err != nil || d.W != 1920 || d.H != 1200 {
		t.Fatalf("got %dx%d err=%v", d.W, d.H, err)
	}
	if err := (&Display{}).checkSource("extend", props); err == nil {
		t.Fatal("extend accepted eDP-1")
	}
}

// xdg-desktop-portal-hyprland lets the user pick a window or region even
// though smon asks for monitors; those streams carry no mapping_id.
func TestRejectNonMonitorSource(t *testing.T) {
	window := map[string]dbus.Variant{"source_type": dbus.MakeVariant(uint32(2))}
	for _, mode := range []string{"extend", "mirror"} {
		if err := (&Display{}).checkSource(mode, window); err == nil {
			t.Errorf("%s accepted a window", mode)
		}
	}
	monitor := map[string]dbus.Variant{"source_type": dbus.MakeVariant(uint32(1)), "mapping_id": dbus.MakeVariant(compositor.OutputName)}
	d := &Display{want: compositor.OutputName}
	if err := d.checkSource("extend", monitor); err != nil || d.output != compositor.OutputName {
		t.Errorf("extend rejected SMON-1: %v", err)
	}
}

// fakeComp is a compositor with fixed outputs; the portal-made variant
// implements compositor.PortalOutput and records where it was placed.
type fakeComp struct {
	compositor.Compositor
	outs   []compositor.Output
	placed string
}

func (f *fakeComp) Outputs() ([]compositor.Output, error) { return f.outs, nil }

type fakePortalComp struct{ fakeComp }

func (f *fakePortalComp) PlaceOutput(name, pos string, _, _ int) error {
	f.placed = name + "@" + pos
	return nil
}

func out(name string, x, y int) compositor.Output {
	var o compositor.Output
	o.Name, o.Enabled = name, true
	o.Position.X, o.Position.Y = x, y
	return o
}

// Plasma 6.8+: a virtual source with mapping_id and the size picked in the dialog.
func TestPortalVirtualSource(t *testing.T) {
	c := &fakePortalComp{}
	d := &Display{W: 1920, H: 1080, comp: c, portalOut: true, pos: "left"}
	props := map[string]dbus.Variant{
		"source_type": dbus.MakeVariant(uint32(4)),
		"mapping_id":  dbus.MakeVariant("Virtual-smon"),
		"size":        dbus.MakeVariant([]any{int32(2560), int32(1440)}),
	}
	if err := d.checkSource("extend", props); err != nil {
		t.Fatal(err)
	}
	if d.output != "Virtual-smon" || d.want != "Virtual-smon" || d.W != 2560 || d.H != 1440 || c.placed != "Virtual-smon@left" {
		t.Errorf("got output=%s want=%s %dx%d placed=%s", d.output, d.want, d.W, d.H, c.placed)
	}
}

// Plasma before 6.8 sends no mapping_id: the output that appeared is the one.
func TestPortalVirtualNamedByDiff(t *testing.T) {
	c := &fakePortalComp{fakeComp{outs: []compositor.Output{out("eDP-1", 0, 0), out("Virtual-xdp", 1920, 0)}}}
	d := &Display{comp: c, portalOut: true, before: []compositor.Output{out("eDP-1", 0, 0)}}
	if err := d.checkSource("extend", map[string]dbus.Variant{"source_type": dbus.MakeVariant(uint32(4))}); err != nil || d.output != "Virtual-xdp" {
		t.Errorf("got %q, err=%v", d.output, err)
	}
}

// Only a compositor whose portal makes the output may return a virtual source.
func TestRejectVirtualSource(t *testing.T) {
	virt := map[string]dbus.Variant{"source_type": dbus.MakeVariant(uint32(4))}
	if err := (&Display{want: "HEADLESS-2"}).checkSource("extend", virt); err == nil {
		t.Error("accepted a virtual source")
	}
}

// xdg-desktop-portal-wlr before 0.9 sends no mapping_id: the position tells.
func TestNameByPosition(t *testing.T) {
	c := &fakeComp{outs: []compositor.Output{out("eDP-1", 0, 0), out("HEADLESS-2", 1920, 0)}}
	at := func(x int) map[string]dbus.Variant {
		return map[string]dbus.Variant{"source_type": dbus.MakeVariant(uint32(1)), "position": dbus.MakeVariant([]any{int32(x), int32(0)})}
	}
	d := &Display{comp: c, want: "HEADLESS-2"}
	if err := d.checkSource("extend", at(1920)); err != nil || d.output != "HEADLESS-2" {
		t.Errorf("headless: got %q, err=%v", d.output, err)
	}
	if err := (&Display{comp: c, want: "HEADLESS-2"}).checkSource("extend", at(0)); err == nil {
		t.Error("extend accepted eDP-1 found by position")
	}
}
