package compositor

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"smon/internal/logx"
	"smon/internal/touch"
)

// labwc drives labwc, which has no IPC: its VirtualOutputAdd and
// VirtualOutputRemove actions run only from keybinds. The user binds them
// once in rc.xml (see labwcKeybinds), and smon presses those keys on a uinput
// keyboard of its own. Mode, position and scale go through
// wlr-output-management (wlr-randr). Touch and pen mapping exist only in
// rc.xml, so smon checks for the rules and warns when they are missing.
type labwc struct {
	mu   sync.Mutex
	keys *touch.Shortcuts // made on first use; removed when smon exits
}

// Name implements compositor.
func (*labwc) Name() string { return "labwc" }

// labwcMaxMonitor is the last monitor with keybinds: F1 to F8, as many as
// -monitors allows.
const labwcMaxMonitor = 8

// labwcChord is the keybind that adds (Super+Ctrl+F<n>) or removes
// (Super+Alt+F<n>) monitor n, as labwc spells it and as uinput key names.
// Neither chord holds Ctrl+Alt (labwc switches VT on Ctrl+Alt+F<n> first)
// or Shift (a common layout-switch modifier).
func labwcChord(n int, add bool) (bind string, keys []string) {
	f := fmt.Sprintf("F%d", n)
	if add {
		return "W-C-" + f, []string{"MetaLeft", "ControlLeft", f}
	}
	return "W-A-" + f, []string{"MetaLeft", "AltLeft", f}
}

// labwcKeybinds is the rc.xml block that lets smon add and remove output name
// (SMON-n).
func labwcKeybinds(name string, n int) string {
	add, _ := labwcChord(n, true)
	rm, _ := labwcChord(n, false)
	return fmt.Sprintf(`<keybind key=%q><action name="VirtualOutputAdd" output_name=%q /></keybind>
<keybind key=%q><action name="VirtualOutputRemove" output_name=%q /></keybind>`, add, name, rm, name)
}

// labwcMonitor is n for output SMON-n, the outputs smon has keybinds for.
func labwcMonitor(name string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(name, "SMON-"))
	if !strings.HasPrefix(name, "SMON-") || err != nil || n < 1 || n > labwcMaxMonitor {
		return 0, fmt.Errorf("labwc: no keybind for output %q (SMON-1 to SMON-%d)", name, labwcMaxMonitor)
	}
	return n, nil
}

// labwcPresses is how often a chord is pressed before smon gives up, and
// labwcWait how long it waits for the output after each press. The first
// press can be lost while labwc is still picking up the new keyboard; a
// repeat is harmless (labwc refuses a duplicate name, and removing a missing
// output does nothing).
const (
	labwcPresses = 4
	labwcWait    = time.Second
)

// press runs monitor n's add or remove keybind until the output is there
// (add) or gone (remove). l.mu is held.
func (l *labwc) press(name string, n int, add bool) error {
	if l.keys == nil {
		k, err := touch.NewShortcuts()
		if err != nil {
			return fmt.Errorf("labwc: smon presses keybinds to add outputs: %w", err)
		}
		l.keys = k
	}
	_, keys := labwcChord(n, add)
	for range labwcPresses {
		if err := l.keys.Press(keys...); err != nil {
			return err
		}
		for deadline := time.Now().Add(labwcWait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			outs, err := l.Outputs()
			if err != nil {
				return err
			}
			if hasOutput(outs, name) == add {
				return nil
			}
		}
	}
	return nil
}

func hasOutput(outs []Output, name string) bool {
	return slices.ContainsFunc(outs, func(o Output) bool { return o.Name == name })
}

func (l *labwc) CreateOutput(spec OutputSpec) (string, error) {
	name := spec.name()
	n, err := labwcMonitor(name)
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	outs, err := l.Outputs()
	if err != nil {
		return "", err
	}
	if hasOutput(outs, name) { // stale output from a crash
		if err := l.press(name, n, false); err != nil {
			return "", err
		}
	}
	if err := l.press(name, n, true); err != nil {
		return "", err
	}
	if outs, err = l.Outputs(); err != nil {
		return "", err
	}
	if !hasOutput(outs, name) {
		return "", fmt.Errorf("labwc: %s did not appear after pressing its keybind; add to the <keyboard> section of rc.xml and run labwc --reconfigure:\n%s", name, labwcKeybinds(name, n))
	}
	args, err := labwcPlacedArgs(name, spec, outs)
	if err == nil {
		err = wlrRandr(args...)
	}
	if err != nil {
		_ = l.press(name, n, false)
		return "", err
	}
	return name, nil
}

// ResizeOutput implements ResizableOutput: headless outputs take any mode.
// Without -pos the output stays where it is.
func (l *labwc) ResizeOutput(name string, spec OutputSpec) error {
	var outs []Output
	if spec.Pos != "" {
		var err error
		if outs, err = l.Outputs(); err != nil {
			return err
		}
	}
	args, err := labwcPlacedArgs(name, spec, outs)
	if err != nil {
		return err
	}
	return wlrRandr(args...)
}

// labwcPlacedArgs sets output name's mode and scale, and its position by
// spec.Pos among outs.
func labwcPlacedArgs(name string, spec OutputSpec, outs []Output) ([]string, error) {
	args := []string{"--output", name, "--custom-mode", fmt.Sprintf("%dx%d@%dHz", spec.W, spec.H, spec.FPS), "--scale", spec.scaleArg()}
	if spec.Pos != "" {
		w, h := spec.Logical()
		x, y, ok, err := placeXY(outs, name, spec.Pos, w, h)
		if err != nil {
			return nil, err
		}
		if ok {
			args = append(args, "--pos", fmt.Sprintf("%d,%d", x, y))
		}
	}
	return args, nil
}

func (l *labwc) DestroyOutput(name string) {
	n, err := labwcMonitor(name)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.press(name, n, false)
}

func (*labwc) WatchLayout(string, chan struct{}) {} // no saved layout to defer to

// MapInput checks that rc.xml maps dev to output, since labwc maps input only
// from its config, and warns with the rule to add when it does not.
func (*labwc) MapInput(dev touch.Device, output string) error {
	path := labwcRC()
	if path == "" {
		logx.Warnf("labwc", "no rc.xml found; to map %s to %s add to it:\n%s", dev.Name, output, labwcInputRule(dev, output))
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // unreadable: assume the user knows
	}
	ok, err := labwcMapsInput(b, dev, output)
	if err != nil {
		logx.Warnf("labwc", "reading %s: %v", path, err)
		return nil
	}
	if !ok {
		what := "touches"
		if dev.Pen {
			what = "the pen"
		}
		logx.Warnf("labwc", "%s does not map %s to %s, so %s land across the whole desktop; add to it and run labwc --reconfigure:\n%s",
			path, dev.Name, output, what, labwcInputRule(dev, output))
	}
	return nil
}

func (*labwc) UnmapInput(touch.Device) {} // the rule is the user's

// labwcInputRule is the rc.xml rule mapping dev to output. labwc maps touch
// per device, but every tablet with one rule.
func labwcInputRule(dev touch.Device, output string) string {
	if dev.Pen {
		return fmt.Sprintf(`<tablet mapToOutput=%q />`, output)
	}
	return fmt.Sprintf(`<touch deviceName=%q mapToOutput=%q />`, dev.Name, output)
}

// labwcRC is the rc.xml labwc reads (unless started with -c or -C): the first
// in $XDG_CONFIG_HOME, then $XDG_CONFIG_DIRS.
func labwcRC() string {
	home := os.Getenv("XDG_CONFIG_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".config")
		}
	}
	dirs := os.Getenv("XDG_CONFIG_DIRS")
	if dirs == "" {
		dirs = "/etc/xdg"
	}
	for _, d := range append([]string{home}, filepath.SplitList(dirs)...) {
		if d == "" {
			continue
		}
		p := filepath.Join(d, "labwc", "rc.xml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// xmlNode is any element of rc.xml.
type xmlNode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Text    string     `xml:",chardata"`
	Nodes   []xmlNode  `xml:",any"`
}

// field is an element's setting, which labwc takes from an attribute or a
// child element alike, with names in any case.
func (n *xmlNode) field(key string) (string, bool) {
	for _, a := range n.Attrs {
		if strings.EqualFold(a.Name.Local, key) {
			return a.Value, true
		}
	}
	for _, c := range n.Nodes {
		if strings.EqualFold(c.XMLName.Local, key) {
			return strings.TrimSpace(c.Text), true
		}
	}
	return "", false
}

// labwcMapsInput reports whether rc.xml b maps dev to output: a <touch> rule
// with dev's name (any case), or the <tablet> rule for a pen.
func labwcMapsInput(b []byte, dev touch.Device, output string) (bool, error) {
	var root xmlNode
	if err := xml.Unmarshal(b, &root); err != nil {
		return false, err
	}
	found := false
	var walk func(n *xmlNode)
	walk = func(n *xmlNode) {
		el := strings.ToLower(n.XMLName.Local)
		if out, _ := n.field("mapToOutput"); out == output {
			name, _ := n.field("deviceName")
			if dev.Pen && el == "tablet" || !dev.Pen && el == "touch" && strings.EqualFold(name, dev.Name) {
				found = true
			}
		}
		for i := range n.Nodes {
			walk(&n.Nodes[i])
		}
	}
	walk(&root)
	return found, nil
}

// wlrRandr changes outputs through wlr-output-management.
func wlrRandr(args ...string) error {
	out, err := exec.Command("wlr-randr", args...).CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("labwc needs wlr-randr to size and place outputs")
		}
		return fmt.Errorf("wlr-randr %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (*labwc) Outputs() ([]Output, error) {
	out, err := exec.Command("wlr-randr", "--json").Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("labwc needs wlr-randr to size and place outputs")
		}
		return nil, fmt.Errorf("wlr-randr --json: %w", err)
	}
	return parseWlrRandr(out)
}

// parseWlrRandr converts `wlr-randr --json` to outputs in logical (scaled,
// rotated) size. A headless output's custom mode is its only, current mode.
func parseWlrRandr(b []byte) ([]Output, error) {
	var heads []struct {
		Name    string
		Enabled bool
		Modes   []struct {
			Width, Height int
			Current       bool
		}
		Position  struct{ X, Y int }
		Transform string
		Scale     float64
	}
	if err := json.Unmarshal(b, &heads); err != nil {
		return nil, fmt.Errorf("wlr-randr --json: %w", err)
	}
	outs := make([]Output, 0, len(heads))
	for _, hd := range heads {
		var o Output
		o.Name, o.Enabled = hd.Name, hd.Enabled
		o.Position.X, o.Position.Y = hd.Position.X, hd.Position.Y
		var w, h int
		for _, m := range hd.Modes {
			if m.Current {
				w, h = m.Width, m.Height
			}
		}
		if strings.HasSuffix(hd.Transform, "90") || strings.HasSuffix(hd.Transform, "270") {
			w, h = h, w
		}
		if hd.Scale > 0 {
			w, h = int(float64(w)/hd.Scale+0.5), int(float64(h)/hd.Scale+0.5)
		}
		o.Logical.Width, o.Logical.Height = w, h
		outs = append(outs, o)
	}
	return outs, nil
}
