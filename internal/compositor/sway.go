package compositor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"smon/internal/config"
	"smon/internal/touch"
)

// sway drives Sway through swaymsg. create_output and `output … unplug` need
// Sway 1.8 or newer. Sway names headless outputs itself (HEADLESS-<n>, never
// reused), so smon learns the name by comparing the outputs before and after.
type sway struct{}

// Name implements compositor.
func (*sway) Name() string { return "sway" }

// swayState remembers the output smon created, so a run after a crash can
// remove it: its name differs every time.
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
	if old, err := os.ReadFile(swayState()); err == nil {
		s.DestroyOutput(strings.TrimSpace(string(old))) // stale output from a crash
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
	_ = os.MkdirAll(config.Dir(), 0o700)
	_ = os.WriteFile(swayState(), []byte(name), 0o600) // best effort: only crash cleanup needs it

	cmd := swayOutputCmd(name, spec)
	if spec.Pos != "" {
		w, h := spec.Logical()
		x, y, ok, err := placeXY(after, name, spec.Pos, w, h)
		if err != nil {
			s.DestroyOutput(name)
			return "", err
		}
		if ok {
			cmd += fmt.Sprintf(" pos %d %d", x, y)
		}
	}
	if err := swaymsg(cmd); err != nil {
		s.DestroyOutput(name)
		return "", err
	}
	return name, nil
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

func (*sway) DestroyOutput(name string) {
	if name != "" {
		_ = swaymsg("output " + name + " unplug")
	}
	_ = os.Remove(swayState())
}

func (*sway) WatchLayout(done chan struct{}) {} // no saved layout to defer to

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
