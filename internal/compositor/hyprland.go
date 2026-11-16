package compositor

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"smon/internal/touch"
)

// hyprland drives Hyprland through hyprctl. Named headless outputs need
// Hyprland 0.42 or newer. Runtime config goes through `hyprctl eval` (Lua)
// when the session runs a Lua config (always from 0.57), and through
// `hyprctl keyword` with a hyprlang config (up to 0.56).
type hyprland struct {
	lua    *bool             // nil until probed
	global map[string]string // global mapping options smon fell back to (hyprlang only) -> value before
}

// isLua reports whether the session uses the Lua config provider: eval
// answers "ok" there, and an error under hyprlang or before 0.55.
func (h *hyprland) isLua() bool {
	if h.lua == nil {
		out, err := hyprctl("eval", "return")
		v := err == nil && strings.TrimSpace(out) == "ok"
		h.lua = &v
	}
	return *h.lua
}

// Name implements compositor.
func (*hyprland) Name() string { return "hyprland" }

// setMonitor adds a runtime monitor rule.
func (h *hyprland) setMonitor(name, mode, pos, scale string) error {
	if h.isLua() {
		return hyprOK("eval", monitorLua(name, mode, pos, scale))
	}
	return hyprOK("keyword", "monitor", monitorKeyword(name, mode, pos, scale))
}

func monitorLua(name, mode, pos, scale string) string {
	return fmt.Sprintf("hl.monitor({output = %q, mode = %q, position = %q, scale = %q})", name, mode, pos, scale)
}

func monitorKeyword(name, mode, pos, scale string) string {
	return fmt.Sprintf("%s,%s,%s,%s", name, mode, pos, scale)
}

// hyprctl runs hyprctl and returns its output.
func hyprctl(args ...string) (string, error) {
	out, err := exec.Command("hyprctl", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("hyprctl %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// hyprOK runs a hyprctl command that answers "ok" on success. hyprctl exits 0
// even when the command fails, so the answer is what counts.
func hyprOK(args ...string) error {
	out, err := hyprctl(args...)
	if err != nil {
		return err
	}
	if s := strings.TrimSpace(out); s != "ok" {
		return fmt.Errorf("hyprctl %s: %s", strings.Join(args, " "), s)
	}
	return nil
}

func (h *hyprland) CreateOutput(spec OutputSpec) (string, error) {
	name := OutputName
	_, _ = hyprctl("output", "remove", name) // stale output from a crash
	where := "auto-right"
	if spec.Pos != "" {
		outs, err := h.Outputs()
		if err != nil {
			return "", err
		}
		w, hh := spec.Logical()
		x, y, ok, err := placeXY(outs, name, spec.Pos, w, hh)
		if err != nil {
			return "", err
		}
		if ok {
			where = fmt.Sprintf("%dx%d", x, y)
		}
	}
	// The rule first, so the output comes up at the right mode and place.
	if err := h.setMonitor(name, fmt.Sprintf("%dx%d@%d", spec.W, spec.H, spec.FPS), where, spec.scaleArg()); err != nil {
		return "", err
	}
	return name, hyprOK("output", "create", "headless", name)
}

func (*hyprland) DestroyOutput(name string) { _, _ = hyprctl("output", "remove", name) }

func (*hyprland) WatchLayout(done chan struct{}) {} // no saved layout to defer to

// hyprGlobalOutput is the option that maps every device of dev's kind.
func hyprGlobalOutput(dev touch.Device) string {
	if dev.Pen {
		return "input:tablet:output"
	}
	return "input:touchdevice:output"
}

// MapInput maps only smon's own device with a per-device rule (stored by
// name, so it applies when the device appears). If a hyprlang session rejects
// that, it maps every device of that kind (input:touchdevice:output or
// input:tablet:output) and restores the old value on UnmapInput.
func (h *hyprland) MapInput(dev touch.Device, output string) error {
	name := dev.Hyprland()
	if h.isLua() {
		return hyprOK("eval", fmt.Sprintf("hl.device({name = %q, output = %q})", name, output))
	}
	if hyprOK("keyword", "device["+name+"]:output", output) == nil {
		return nil
	}
	opt := hyprGlobalOutput(dev)
	out, err := hyprctl("-j", "getoption", opt)
	if err != nil {
		return err
	}
	var cur struct{ Str string }
	_ = json.Unmarshal([]byte(out), &cur) // unreadable: UnmapInput restores [[Auto]]
	if err := hyprOK("keyword", opt, output); err != nil {
		return err
	}
	if h.global == nil {
		h.global = map[string]string{}
	}
	if _, ok := h.global[opt]; !ok { // keep the value from before smon
		h.global[opt] = cur.Str
	}
	return nil
}

func (h *hyprland) UnmapInput(dev touch.Device) {
	opt := hyprGlobalOutput(dev)
	prev, ok := h.global[opt]
	if !ok {
		return // the per-device rule names a device that is gone
	}
	if prev == "" || prev == "[[EMPTY]]" {
		prev = "[[Auto]]"
	}
	_, _ = hyprctl("keyword", opt, prev)
	delete(h.global, opt)
}

// hyprMonitor is one entry of `hyprctl -j monitors all`.
type hyprMonitor struct {
	Name          string
	X, Y          int
	Width, Height int // pixels
	Scale         float64
	Transform     int
	Disabled      bool
}

func (*hyprland) Outputs() ([]Output, error) {
	out, err := hyprctl("-j", "monitors", "all")
	if err != nil {
		return nil, err
	}
	return parseHyprMonitors([]byte(out))
}

// parseHyprMonitors converts `hyprctl -j monitors all` to outputs in logical
// (scaled, rotated) size.
func parseHyprMonitors(b []byte) ([]Output, error) {
	var mons []hyprMonitor
	if err := json.Unmarshal(b, &mons); err != nil {
		return nil, fmt.Errorf("hyprctl monitors: %w", err)
	}
	outs := make([]Output, 0, len(mons))
	for _, m := range mons {
		var o Output
		o.Name, o.Enabled = m.Name, !m.Disabled
		o.Position.X, o.Position.Y = m.X, m.Y
		w, h := m.Width, m.Height
		if m.Transform%2 == 1 { // rotated 90/270
			w, h = h, w
		}
		if m.Scale > 0 {
			w, h = int(float64(w)/m.Scale+0.5), int(float64(h)/m.Scale+0.5)
		}
		o.Logical.Width, o.Logical.Height = w, h
		outs = append(outs, o)
	}
	return outs, nil
}
