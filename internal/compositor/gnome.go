package compositor

import (
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strings"

	"github.com/godbus/dbus/v5"

	"smon/internal/touch"
)

// gnome drives GNOME Shell (Mutter). As on KDE, the virtual monitor is made
// by the ScreenCast portal (xdg-desktop-portal-gnome, source type VIRTUAL)
// and goes away with the portal session. Mutter sizes it from the video
// format the consumer negotiates, so smon asks for -w x -h in the pipeline
// caps (see CapsSized). smon places and scales it through Mutter's
// DisplayConfig D-Bus interface, as a temporary configuration that is never
// saved to monitors.xml, and maps its input devices with the
// org.gnome.desktop.peripherals settings of smon's own device ids.
type gnome struct{}

// Name implements compositor.
func (*gnome) Name() string { return "gnome" }

// CreateOutput returns "": the portal creates the output.
func (*gnome) CreateOutput(OutputSpec) (string, error) { return "", nil }

func (*gnome) DestroyOutput(string) {} // closing the portal session removes it

func (*gnome) WatchLayout(string, chan struct{}) {} // Mutter keeps the layout itself

// CapsSized implements CapsSizedOutput.
func (*gnome) CapsSized() bool { return true }

const (
	mutterDest  = "org.gnome.Mutter.DisplayConfig"
	mutterPath  = "/org/gnome/Mutter/DisplayConfig"
	mutterIface = "org.gnome.Mutter.DisplayConfig"

	applyTemporary = 1 // ApplyMonitorsConfig method: not saved to monitors.xml
	layoutLogical  = 1 // layout-mode: positions in logical (scaled) pixels
)

// mutterMonitorSpec identifies a monitor: (connector, vendor, product, serial).
type mutterMonitorSpec struct {
	Connector, Vendor, Product, Serial string
}

type mutterMode struct {
	ID              string
	Width, Height   int32
	Refresh         float64
	PreferredScale  float64
	SupportedScales []float64
	Props           map[string]dbus.Variant
}

type mutterMonitor struct {
	Spec  mutterMonitorSpec
	Modes []mutterMode
	Props map[string]dbus.Variant
}

type mutterLogical struct {
	X, Y      int32
	Scale     float64
	Transform uint32
	Primary   bool
	Monitors  []mutterMonitorSpec
	Props     map[string]dbus.Variant
}

// mutterState is GetCurrentState's reply.
type mutterState struct {
	Serial   uint32
	Monitors []mutterMonitor
	Logical  []mutterLogical
	Props    map[string]dbus.Variant
}

// Arguments of ApplyMonitorsConfig: a((iiduba(ssa{sv}))).
type (
	applyMonitor struct {
		Connector, ModeID string
		Props             map[string]dbus.Variant
	}
	applyLogical struct {
		X, Y      int32
		Scale     float64
		Transform uint32
		Primary   bool
		Monitors  []applyMonitor
	}
)

func mutterCall() (dbus.BusObject, func(), error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, nil, err
	}
	return conn.Object(mutterDest, mutterPath), func() { conn.Close() }, nil
}

func getMutterState() (*mutterState, error) {
	obj, done, err := mutterCall()
	if err != nil {
		return nil, err
	}
	defer done()
	var st mutterState
	if err := obj.Call(mutterIface+".GetCurrentState", 0).Store(&st.Serial, &st.Monitors, &st.Logical, &st.Props); err != nil {
		return nil, fmt.Errorf("mutter GetCurrentState: %w", err)
	}
	return &st, nil
}

// currentMode returns the mode a monitor is showing, or nil when it is off.
func (m *mutterMonitor) currentMode() *mutterMode {
	for i := range m.Modes {
		if v, ok := m.Modes[i].Props["is-current"]; ok {
			if cur, _ := v.Value().(bool); cur {
				return &m.Modes[i]
			}
		}
	}
	return nil
}

func (st *mutterState) monitor(connector string) *mutterMonitor {
	for i := range st.Monitors {
		if st.Monitors[i].Spec.Connector == connector {
			return &st.Monitors[i]
		}
	}
	return nil
}

// logicalLayout reports whether positions are in logical (scaled) pixels.
// Mutter reports layout-mode only where it can change; physical otherwise.
func (st *mutterState) logicalLayout() bool {
	if v, ok := st.Props["layout-mode"]; ok {
		mode, _ := v.Value().(uint32)
		return mode == layoutLogical
	}
	return false
}

// outputs turns the state into smon's view: one Output per monitor in a
// logical monitor, sized in layout coordinates.
func (st *mutterState) outputs() []Output {
	var outs []Output
	for _, lm := range st.Logical {
		for _, spec := range lm.Monitors {
			m := st.monitor(spec.Connector)
			if m == nil {
				continue
			}
			mode := m.currentMode()
			if mode == nil {
				continue
			}
			w, h := float64(mode.Width), float64(mode.Height)
			if lm.Transform%2 == 1 { // 90 or 270 degrees, flipped or not
				w, h = h, w
			}
			if st.logicalLayout() && lm.Scale > 0 {
				w, h = w/lm.Scale, h/lm.Scale
			}
			var o Output
			o.Name, o.Enabled = spec.Connector, true
			o.Position.X, o.Position.Y = int(lm.X), int(lm.Y)
			o.Logical.Width, o.Logical.Height = int(w+0.5), int(h+0.5)
			outs = append(outs, o)
		}
	}
	return outs
}

func (*gnome) Outputs() ([]Output, error) {
	st, err := getMutterState()
	if err != nil {
		return nil, err
	}
	return st.outputs(), nil
}

// nearestScale picks the supported scale closest to want; Mutter rejects any
// other. With no list, want goes through as is.
func nearestScale(want float64, supported []float64) float64 {
	best := want
	for i, s := range supported {
		if i == 0 || math.Abs(s-want) < math.Abs(best-want) {
			best = s
		}
	}
	return best
}

// applyConfig is the full layout (Mutter replaces it whole) with connector's
// logical monitor moved to x, y and scaled by scale. move says whether to
// move it at all.
func (st *mutterState) applyConfig(connector string, move bool, x, y int32, scale float64) ([]applyLogical, error) {
	var cfg []applyLogical
	found := false
	for _, lm := range st.Logical {
		al := applyLogical{X: lm.X, Y: lm.Y, Scale: lm.Scale, Transform: lm.Transform, Primary: lm.Primary}
		for _, spec := range lm.Monitors {
			m := st.monitor(spec.Connector)
			if m == nil {
				return nil, fmt.Errorf("mutter: logical monitor names unknown monitor %s", spec.Connector)
			}
			mode := m.currentMode()
			if mode == nil {
				return nil, fmt.Errorf("mutter: monitor %s has no current mode", spec.Connector)
			}
			al.Monitors = append(al.Monitors, applyMonitor{Connector: spec.Connector, ModeID: mode.ID, Props: map[string]dbus.Variant{}})
			if spec.Connector == connector {
				found = true
				if move {
					al.X, al.Y = x, y
				}
				if scale > 0 {
					al.Scale = nearestScale(scale, mode.SupportedScales)
				}
			}
		}
		cfg = append(cfg, al)
	}
	if !found {
		return nil, fmt.Errorf("mutter does not list %s among its monitors", connector)
	}
	return cfg, nil
}

// PlaceOutput implements PortalOutput.
func (*gnome) PlaceOutput(name string, spec OutputSpec) error {
	if spec.Pos == "" && !spec.Scaled() {
		return nil // where Mutter put it
	}
	st, err := getMutterState()
	if err != nil {
		return err
	}
	var x, y int
	move := false
	if spec.Pos != "" {
		w, h := spec.Logical()
		if !st.logicalLayout() {
			w, h = spec.W, spec.H
		}
		var ok bool
		if x, y, ok, err = placeXY(st.outputs(), name, spec.Pos, w, h); err != nil {
			return err
		}
		move = ok
	}
	scale := 0.0
	if spec.Scaled() {
		scale = spec.scale()
	}
	cfg, err := st.applyConfig(name, move, int32(x), int32(y), scale)
	if err != nil {
		return err
	}
	obj, done, err := mutterCall()
	if err != nil {
		return err
	}
	defer done()
	if err := obj.Call(mutterIface+".ApplyMonitorsConfig", 0, st.Serial, uint32(applyTemporary), cfg, map[string]dbus.Variant{}).Err; err != nil {
		return fmt.Errorf("mutter ApplyMonitorsConfig: %w", err)
	}
	return nil
}

// gsettingsPath is the relocatable settings path for one of smon's devices:
// GNOME keys touchscreen and tablet settings by vendor:product id.
func gsettingsPath(dev touch.Device) string {
	kind := "touchscreen"
	if dev.Pen {
		kind = "tablet"
	}
	return fmt.Sprintf("org.gnome.desktop.peripherals.%s:/org/gnome/desktop/peripherals/%ss/%04x:%04x/", kind, kind, dev.Vendor, dev.Product)
}

// gvariantStrings formats a GVariant string array, as gsettings takes it.
func gvariantStrings(ss ...string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// MapInput points the device's output setting at the monitor, named the way
// Mutter matches it: [vendor, product, serial].
func (*gnome) MapInput(dev touch.Device, output string) error {
	st, err := getMutterState()
	if err != nil {
		return err
	}
	m := st.monitor(output)
	if m == nil {
		return fmt.Errorf("mutter does not list %s among its monitors", output)
	}
	if _, err := exec.LookPath("gsettings"); err != nil {
		return errors.New("mapping input on GNOME needs gsettings (glib2)")
	}
	val := gvariantStrings(m.Spec.Vendor, m.Spec.Product, m.Spec.Serial)
	if out, err := exec.Command("gsettings", "set", gsettingsPath(dev), "output", val).CombinedOutput(); err != nil {
		return fmt.Errorf("gsettings set %s output: %v: %s", gsettingsPath(dev), err, out)
	}
	return nil
}

// UnmapInput resets the setting: it belongs to smon's device id only.
func (*gnome) UnmapInput(dev touch.Device) {
	_ = exec.Command("gsettings", "reset", gsettingsPath(dev), "output").Run()
}
