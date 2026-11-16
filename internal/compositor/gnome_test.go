package compositor

import (
	"testing"

	"github.com/godbus/dbus/v5"

	"smon/internal/touch"
)

// The Go types must match Mutter's D-Bus signatures exactly, or godbus
// cannot store the reply (or Mutter rejects the call).
func TestMutterSignatures(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{
		{[]mutterMonitor{}, "a((ssss)a(siiddada{sv})a{sv})"},
		{[]mutterLogical{}, "a(iiduba(ssss)a{sv})"},
		{[]applyLogical{}, "a(iiduba(ssa{sv}))"},
	} {
		if got := dbus.SignatureOf(c.v).String(); got != c.want {
			t.Errorf("%T: %s, want %s", c.v, got, c.want)
		}
	}
}

func mode(id string, w, h int32, current bool, scales ...float64) mutterMode {
	return mutterMode{
		ID: id, Width: w, Height: h, Refresh: 60, PreferredScale: 1, SupportedScales: scales,
		Props: map[string]dbus.Variant{"is-current": dbus.MakeVariant(current)},
	}
}

// A laptop panel at 2x left of a 1080p screen, plus smon's virtual monitor
// (Meta-0) that Mutter put to the right.
func gnomeState(logical bool) *mutterState {
	st := &mutterState{
		Serial: 7,
		Monitors: []mutterMonitor{
			{Spec: mutterMonitorSpec{"eDP-1", "BOE", "0x0a1c", "0x00000000"}, Modes: []mutterMode{mode("2880x1800@60", 2880, 1800, true, 1, 1.5, 2)}},
			{Spec: mutterMonitorSpec{"DP-1", "DEL", "U2415", "ABC123"}, Modes: []mutterMode{mode("1920x1080@60", 1920, 1080, true, 1, 2), mode("1280x720@60", 1280, 720, false, 1)}},
			{Spec: mutterMonitorSpec{"Meta-0", "MetaVendor", "Virtual remote monitor", "0x1"}, Modes: []mutterMode{mode("1600x2560@60", 1600, 2560, true, 1, 1.25, 1.5, 2)}},
		},
		Logical: []mutterLogical{
			{X: 0, Y: 0, Scale: 2, Primary: true, Monitors: []mutterMonitorSpec{{Connector: "eDP-1"}}},
			{X: 1440, Y: 0, Scale: 1, Monitors: []mutterMonitorSpec{{Connector: "DP-1"}}},
			{X: 3360, Y: 0, Scale: 1, Monitors: []mutterMonitorSpec{{Connector: "Meta-0"}}},
		},
		Props: map[string]dbus.Variant{},
	}
	if logical {
		st.Props["layout-mode"] = dbus.MakeVariant(uint32(layoutLogical))
	}
	return st
}

func TestMutterOutputs(t *testing.T) {
	outs := gnomeState(true).outputs()
	if len(outs) != 3 || outs[0].Logical.Width != 1440 || outs[0].Logical.Height != 900 || outs[1].Position.X != 1440 || outs[2].Name != "Meta-0" {
		t.Fatalf("logical layout: %+v", outs)
	}
	phys := gnomeState(false).outputs()
	if phys[0].Logical.Width != 2880 {
		t.Fatalf("physical layout should not divide by scale: %+v", phys[0])
	}
	st := gnomeState(true)
	st.Logical[1].Transform = 1 // DP-1 rotated 90 degrees
	if o := st.outputs()[1]; o.Logical.Width != 1080 || o.Logical.Height != 1920 {
		t.Fatalf("rotated: %+v", o)
	}
}

func TestMutterApplyConfig(t *testing.T) {
	st := gnomeState(true)
	// Put Meta-0 left of everything at scale 1.4 (nearest supported: 1.5).
	cfg, err := st.applyConfig("Meta-0", true, -1067, 0, 1.4)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg) != 3 {
		t.Fatalf("Mutter needs the whole layout, got %d logical monitors", len(cfg))
	}
	if cfg[0].Scale != 2 || !cfg[0].Primary || cfg[0].Monitors[0].ModeID != "2880x1800@60" {
		t.Errorf("other monitors must stay as they are: %+v", cfg[0])
	}
	if cfg[1].Monitors[0].ModeID != "1920x1080@60" {
		t.Errorf("current mode, not the first: %+v", cfg[1])
	}
	if m := cfg[2]; m.X != -1067 || m.Y != 0 || m.Scale != 1.5 {
		t.Errorf("Meta-0: %+v", m)
	}

	// Scale only: position kept.
	cfg, _ = st.applyConfig("Meta-0", false, 0, 0, 2)
	if m := cfg[2]; m.X != 3360 || m.Scale != 2 {
		t.Errorf("scale only: %+v", m)
	}
	if _, err := st.applyConfig("Meta-9", true, 0, 0, 0); err == nil {
		t.Error("unknown connector should fail")
	}
}

func TestGnomeInputSettings(t *testing.T) {
	if got, want := gsettingsPath(touch.TouchDevice), "org.gnome.desktop.peripherals.touchscreen:/org/gnome/desktop/peripherals/touchscreens/1209:5340/"; got != want {
		t.Errorf("touch: %s", got)
	}
	if got, want := gsettingsPath(touch.PenDevice), "org.gnome.desktop.peripherals.tablet:/org/gnome/desktop/peripherals/tablets/1209:5341/"; got != want {
		t.Errorf("pen: %s", got)
	}
	if got, want := gvariantStrings("MetaVendor", "Virtual remote monitor", `it's`), `['MetaVendor', 'Virtual remote monitor', 'it\'s']`; got != want {
		t.Errorf("gvariant: %s", got)
	}
}

func TestDetectGnome(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("SWAYSOCK", "")
	t.Setenv("KDE_FULL_SESSION", "")
	t.Setenv("XDG_CURRENT_DESKTOP", "ubuntu:GNOME")
	c, err := Detect("")
	if err != nil || c.Name() != "gnome" {
		t.Fatalf("Detect = %v, %v", c, err)
	}
}
