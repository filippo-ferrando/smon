package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// hyprland drives Hyprland through hyprctl. Named headless outputs need
// Hyprland 0.42 or newer. Runtime config goes through `hyprctl eval` (Lua)
// when the session runs a Lua config (always from 0.57), and through
// `hyprctl keyword` with a hyprlang config (up to 0.56).
type hyprland struct {
	lua         *bool  // nil until probed
	globalTouch bool   // fell back to input:touchdevice:output (hyprlang only)
	prevTouch   string // its value before smon changed it
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

// setMonitor adds a runtime monitor rule.
func (h *hyprland) setMonitor(name, mode, pos string) error {
	if h.isLua() {
		return hyprOK("eval", fmt.Sprintf("hl.monitor({output = %q, mode = %q, position = %q, scale = \"1\"})", name, mode, pos))
	}
	return hyprOK("keyword", "monitor", fmt.Sprintf("%s,%s,%s,1", name, mode, pos))
}

func (*hyprland) name() string { return "hyprland" }

// touchDevice is the uinput name "smon touchscreen" as Hyprland spells
// device names: lowercase, spaces as dashes.
const touchDevice = "smon-touchscreen"

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

func (h *hyprland) createOutput(name string, w, h2, fps int, pos string) error {
	hyprctl("output", "remove", name) // stale output from a crash
	where := "auto-right"
	if pos != "" {
		outs, err := h.outputs()
		if err != nil {
			return err
		}
		x, y, ok, err := placeXY(outs, name, pos, w, h2)
		if err != nil {
			return err
		}
		if ok {
			where = fmt.Sprintf("%dx%d", x, y)
		}
	}
	// The rule first, so the output comes up at the right mode and place.
	if err := h.setMonitor(name, fmt.Sprintf("%dx%d@%d", w, h2, fps), where); err != nil {
		return err
	}
	if err := hyprOK("output", "create", "headless", name); err != nil {
		return err
	}
	return nil
}

func (*hyprland) destroyOutput(name string) { hyprctl("output", "remove", name) }

func (*hyprland) watchLayout(done chan struct{}) {} // no saved layout to defer to

// mapTouch maps only smon's own touchscreen with a per-device rule (stored by
// name, so it applies when the device appears). If a hyprlang session rejects
// that, it maps every touchscreen (input:touchdevice:output) and restores the
// old value on unmapTouch.
func (h *hyprland) mapTouch(output string) error {
	if h.isLua() {
		return hyprOK("eval", fmt.Sprintf("hl.device({name = %q, output = %q})", touchDevice, output))
	}
	if hyprOK("keyword", "device["+touchDevice+"]:output", output) == nil {
		return nil
	}
	out, err := hyprctl("-j", "getoption", "input:touchdevice:output")
	if err != nil {
		return err
	}
	var opt struct{ Str string }
	json.Unmarshal([]byte(out), &opt)
	if err := hyprOK("keyword", "input:touchdevice:output", output); err != nil {
		return err
	}
	h.globalTouch, h.prevTouch = true, opt.Str
	return nil
}

func (h *hyprland) unmapTouch() {
	if !h.globalTouch {
		return // the per-device rule names a device that is gone
	}
	prev := h.prevTouch
	if prev == "" || prev == "[[EMPTY]]" {
		prev = "[[Auto]]"
	}
	hyprctl("keyword", "input:touchdevice:output", prev)
	h.globalTouch = false
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

func (*hyprland) outputs() ([]outputInfo, error) {
	out, err := hyprctl("-j", "monitors", "all")
	if err != nil {
		return nil, err
	}
	return parseHyprMonitors([]byte(out))
}

func parseHyprMonitors(b []byte) ([]outputInfo, error) {
	var mons []hyprMonitor
	if err := json.Unmarshal(b, &mons); err != nil {
		return nil, fmt.Errorf("hyprctl monitors: %w", err)
	}
	outs := make([]outputInfo, 0, len(mons))
	for _, m := range mons {
		var o outputInfo
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
