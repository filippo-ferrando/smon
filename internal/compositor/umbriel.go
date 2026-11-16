package compositor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"smon/internal/touch"
)

// umbriel drives the umbriel compositor through its CLI.
type umbriel struct{}

func (umbriel) Name() string { return "umbriel" }

func posKey(name string) string   { return "output." + name + ".position" }
func scaleKey(name string) string { return "output." + name + ".scale" }

// inputKey is the setting that maps dev's kind of device to one output.
// umbriel has no per-device mapping, so this also moves any physical
// touchscreen (or pen tablet) while smon runs with -input.
func inputKey(dev touch.Device) string {
	if dev.Pen {
		return "input.tablet.map_to_output"
	}
	return "input.touch.map_to_output"
}

func (u umbriel) CreateOutput(spec OutputSpec) (string, error) {
	name := spec.name()
	_ = exec.Command("umbriel", "output-destroy", name).Run() // stale output from a crash
	if out, err := exec.Command("umbriel", "output-create", name, fmt.Sprintf("%dx%d@%d", spec.W, spec.H, spec.FPS)).CombinedOutput(); err != nil {
		return "", fmt.Errorf("umbriel output-create: %v: %s", err, out)
	}
	if spec.Scaled() {
		if out, err := exec.Command("umbriel", "config", "set", scaleKey(name), spec.scaleArg()).CombinedOutput(); err != nil {
			u.DestroyOutput(name)
			return "", fmt.Errorf("umbriel config set %s: %v: %s", scaleKey(name), err, out)
		}
	}
	w, h := spec.Logical()
	if err := u.layout(name, spec.Pos, w, h); err != nil {
		u.DestroyOutput(name)
		return "", err
	}
	return name, nil
}

func (umbriel) DestroyOutput(name string) {
	_ = exec.Command("umbriel", "output-destroy", name).Run()
	unsetPos(name) // leave no override behind to fight the saved layout
	_ = exec.Command("umbriel", "config", "unset", scaleKey(name)).Run()
}

func (umbriel) MapInput(dev touch.Device, output string) error {
	if dev.Monitor > 1 {
		return fmt.Errorf("umbriel maps every touchscreen and tablet to one output: touch and pen work on %s only", OutputName)
	}
	key := inputKey(dev)
	if out, err := exec.Command("umbriel", "config", "set", key, strconv.Quote(output)).CombinedOutput(); err != nil {
		return fmt.Errorf("umbriel config set %s: %v: %s", key, err, out)
	}
	return nil
}

func (umbriel) UnmapInput(dev touch.Device) {
	if dev.Monitor > 1 {
		return // never mapped
	}
	_ = exec.Command("umbriel", "config", "unset", inputKey(dev)).Run()
}

// unsetPos drops smon's runtime position override for output name.
func unsetPos(name string) { _ = exec.Command("umbriel", "config", "unset", posKey(name)).Run() }

// layout leaves SMON-1 to the saved display layout (outputs.toml, owned by
// the displays plugin; umbriel itself keeps outputs from overlapping), unless
// pos asks smon to place it with a runtime override.
func (u umbriel) layout(name, pos string, w, h int) error {
	if pos == "" {
		unsetPos(name)
		return nil
	}
	outs, err := u.Outputs()
	if err != nil {
		return err
	}
	x, y, ok, err := placeXY(outs, name, pos, w, h)
	if err != nil || !ok {
		return err // no other outputs: leave it where umbriel put it
	}
	return exec.Command("umbriel", "config", "set", posKey(name), fmt.Sprintf("[%d,%d]", x, y)).Run()
}

// WatchLayout hands placement back to the saved layout whenever the user
// rearranges displays (the displays plugin rewrites outputs.toml): a runtime
// override would otherwise pin SMON-1 away from where the plugin shows it.
// ponytail: 1s mtime poll instead of inotify; fine for a human dragging monitors.
func (umbriel) WatchLayout(name string, done chan struct{}) {
	dir, _ := os.UserConfigDir()
	file := filepath.Join(dir, "umbriel", "outputs.toml")
	mtime := func() time.Time {
		st, err := os.Stat(file)
		if err != nil {
			return time.Time{}
		}
		return st.ModTime()
	}
	last := mtime()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			if m := mtime(); !m.Equal(last) {
				last = m
				unsetPos(name)
			}
		}
	}
}

func (umbriel) Outputs() ([]Output, error) {
	out, err := exec.Command("umbriel", "outputs", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("umbriel outputs: %w", err)
	}
	var outs []Output
	return outs, json.Unmarshal(out, &outs)
}
