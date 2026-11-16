package compositor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"smon/internal/config"
	"smon/internal/touch"
)

// sway drives Sway through swaymsg. create_output and `output … unplug` need
// Sway 1.8 or newer. Sway names headless outputs itself (HEADLESS-<n>, never
// reused), so smon learns the name by comparing the outputs before and after.
type sway struct {
	mu      sync.Mutex // one create at a time: the new output is found by diffing
	cleaned bool       // stale outputs from a crash removed (once per run)
}

// Name implements compositor.
func (*sway) Name() string { return "sway" }

// swayState remembers the outputs smon created, one per line, so a run
// after a crash can remove them: their names differ every time.
func swayState() string { return filepath.Join(config.Dir(), "sway-output") }

// swaymsg runs a sway command and checks every reply: swaymsg reports a
// failed command in its JSON as well as in its exit status.
func swaymsg(cmd string) error {
	out, err := exec.Command("swaymsg", "-r", cmd).Output()
	if rerr := swayReply(out); rerr != nil {
		return fmt.Errorf("swaymsg %s: %w", cmd, rerr)
	}
	if err != nil {
		return fmt.Errorf("swaymsg %s: %w", cmd, err)
	}
	return nil
}

// swayReply returns the first error in a command reply.
func swayReply(b []byte) error {
	var res []struct {
		Success bool
		Error   string
	}
	if json.Unmarshal(b, &res) != nil {
		return nil // no JSON: the exit status tells
	}
	for _, r := range res {
		if !r.Success {
			return errors.New(r.Error)
		}
	}
	return nil
}

func (s *sway) CreateOutput(spec OutputSpec) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cleaned { // outputs left by a crash; later ones in this run are ours
		s.cleaned = true
		for _, old := range readSwayState() {
			_ = swaymsg("output " + old + " unplug")
		}
		_ = os.Remove(swayState())
	}
	before, err := s.Outputs()
	if err != nil {
		return "", err
	}
	if err := swaymsg("create_output"); err != nil {
		return "", err
	}
	after, err := s.Outputs()
	if err != nil {
		return "", err
	}
	name := newOutput(before, after)
	if name == "" {
		return "", errors.New("sway create_output: no new output appeared")
	}
	writeSwayState(append(readSwayState(), name)) // best effort: only crash cleanup needs it

	cmd, err := swayPlacedCmd(name, spec, after)
	if err == nil {
		err = swaymsg(cmd)
	}
	if err != nil {
		s.destroy(name)
		return "", err
	}
	return name, nil
}

// ResizeOutput implements ResizableOutput: headless outputs take any mode.
func (s *sway) ResizeOutput(name string, spec OutputSpec) error {
	var outs []Output
	if spec.Pos != "" {
		var err error
		if outs, err = s.Outputs(); err != nil {
			return err
		}
	}
	cmd, err := swayPlacedCmd(name, spec, outs)
	if err != nil {
		return err
	}
	return swaymsg(cmd)
}

// swayPlacedCmd sets output name's mode and scale, and its position by
// spec.Pos among outs.
func swayPlacedCmd(name string, spec OutputSpec, outs []Output) (string, error) {
	cmd := swayOutputCmd(name, spec)
	if spec.Pos != "" {
		w, h := spec.Logical()
		x, y, ok, err := placeXY(outs, name, spec.Pos, w, h)
		if err != nil {
			return "", err
		}
		if ok {
			cmd += fmt.Sprintf(" pos %d %d", x, y)
		}
	}
	return cmd, nil
}

// swayOutputCmd sets the new output's mode and scale.
func swayOutputCmd(name string, spec OutputSpec) string {
	cmd := fmt.Sprintf("output %s mode %dx%d@%dHz", name, spec.W, spec.H, spec.FPS)
	if spec.Scaled() {
		cmd += " scale " + spec.scaleArg()
	}
	return cmd
}

// newOutput returns the first output in after that is not in before.
func newOutput(before, after []Output) string {
	seen := make(map[string]bool, len(before))
	for _, o := range before {
		seen[o.Name] = true
	}
	for _, o := range after {
		if !seen[o.Name] {
			return o.Name
		}
	}
	return ""
}

func (s *sway) DestroyOutput(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.destroy(name)
}

// destroy unplugs the output and forgets it; s.mu is held.
func (*sway) destroy(name string) {
	if name == "" {
		return
	}
	_ = swaymsg("output " + name + " unplug")
	writeSwayState(slices.DeleteFunc(readSwayState(), func(n string) bool { return n == name }))
}

func readSwayState() []string {
	b, _ := os.ReadFile(swayState())
	return strings.Fields(string(b))
}

// writeSwayState saves names, or removes the file when there are none.
func writeSwayState(names []string) {
	if len(names) == 0 {
		_ = os.Remove(swayState())
		return
	}
	_ = os.MkdirAll(config.Dir(), 0o700)
	_ = os.WriteFile(swayState(), []byte(strings.Join(names, "\n")+"\n"), 0o600)
}

func (*sway) WatchLayout(string, chan struct{}) {} // no saved layout to defer to

// MapInput maps only smon's device, by its identifier.
func (*sway) MapInput(dev touch.Device, output string) error {
	return swaymsg(fmt.Sprintf("input %s map_to_output %s", dev.Sway(), output))
}

// UnmapInput resets the stored rule, so a later device with the same name
// spans the whole layout again.
func (*sway) UnmapInput(dev touch.Device) {
	_ = swaymsg(fmt.Sprintf("input %s map_to_output *", dev.Sway()))
}

func (*sway) Outputs() ([]Output, error) {
	out, err := exec.Command("swaymsg", "-r", "-t", "get_outputs").Output()
	if err != nil {
		return nil, fmt.Errorf("swaymsg get_outputs: %w", err)
	}
	return parseSwayOutputs(out)
}

// parseSwayOutputs converts `swaymsg -t get_outputs`, whose rect is already
// in logical (scaled, rotated) layout coordinates.
func parseSwayOutputs(b []byte) ([]Output, error) {
	var outs []struct {
		Name   string
		Active bool
		Rect   struct{ X, Y, Width, Height int }
	}
	if err := json.Unmarshal(b, &outs); err != nil {
		return nil, fmt.Errorf("swaymsg get_outputs: %w", err)
	}
	res := make([]Output, 0, len(outs))
	for _, so := range outs {
		var o Output
		o.Name, o.Enabled = so.Name, so.Active
		o.Position.X, o.Position.Y = so.Rect.X, so.Rect.Y
		o.Logical.Width, o.Logical.Height = so.Rect.Width, so.Rect.Height
		res = append(res, o)
	}
	return res, nil
}
