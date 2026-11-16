package compositor

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"smon/internal/touch"
)

// kde drives KDE Plasma 6 (KWin). The virtual output is made by
// xdg-desktop-portal-kde when smon asks for a VIRTUAL screencast source, and
// goes away with the portal session; smon only places it (kscreen-doctor)
// and maps its touchscreen (KWin's InputDevice D-Bus interface).
type kde struct{}

// Name implements compositor.
func (*kde) Name() string { return "kde" }

// CreateOutput returns "": the portal creates the output.
func (*kde) CreateOutput(OutputSpec) (string, error) { return "", nil }

func (*kde) DestroyOutput(string) {} // closing the portal session removes it

func (*kde) WatchLayout(chan struct{}) {} // KWin saves the layout itself

// PlaceOutput implements PortalOutput.
func (k *kde) PlaceOutput(name string, spec OutputSpec) error {
	var args []string
	if spec.Scaled() {
		args = append(args, fmt.Sprintf("output.%s.scale.%s", name, spec.scaleArg()))
	}
	if spec.Pos != "" {
		outs, err := k.Outputs()
		if err != nil {
			return err
		}
		w, h := spec.Logical()
		x, y, ok, err := placeXY(outs, name, spec.Pos, w, h)
		if err != nil {
			return err
		}
		if ok {
			args = append(args, fmt.Sprintf("output.%s.position.%d,%d", name, x, y))
		}
	}
	if len(args) == 0 {
		return nil // as KWin set it up
	}
	if out, err := exec.Command("kscreen-doctor", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("kscreen-doctor %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

const (
	kwinDest       = "org.kde.KWin"
	kwinInputPath  = "/org/kde/KWin/InputDevice"
	kwinInputIface = "org.kde.KWin.InputDevice"
)

// MapInput finds smon's device among KWin's input devices (it shows up
// shortly after uinput creates it) and sets its outputName.
func (*kde) MapInput(dev touch.Device, output string) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		path, err := kwinDevice(conn, dev.Name)
		if err != nil {
			return err
		}
		if path != "" {
			if err := conn.Object(kwinDest, path).SetProperty(kwinInputIface+".outputName", output); err != nil {
				return fmt.Errorf("KWin: map %s to %s: %w", dev.Name, output, err)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("KWin did not list %q among its input devices", dev.Name)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// kwinDevice returns the D-Bus path of the input device called name, or ""
// while KWin does not know it yet.
func kwinDevice(conn *dbus.Conn, name string) (dbus.ObjectPath, error) {
	v, err := conn.Object(kwinDest, kwinInputPath).GetProperty("org.kde.KWin.InputDeviceManager.devicesSysNames")
	if err != nil {
		return "", fmt.Errorf("KWin input devices: %w", err)
	}
	names, _ := v.Value().([]string)
	for _, sys := range names {
		path := dbus.ObjectPath(kwinInputPath + "/" + sys)
		n, err := conn.Object(kwinDest, path).GetProperty(kwinInputIface + ".name")
		if err == nil && n.Value() == name {
			return path, nil
		}
	}
	return "", nil
}

// UnmapInput removes the mapping KWin saved in kcminputrc for smon's device;
// the device itself is already gone.
func (*kde) UnmapInput(dev touch.Device) {
	for _, key := range []string{"OutputUuid", "OutputName"} {
		_ = exec.Command("kwriteconfig6", "--file", "kcminputrc",
			"--group", "Libinput", "--group", strconv.Itoa(int(dev.Vendor)), "--group", strconv.Itoa(int(dev.Product)),
			"--group", dev.Name, "--key", key, "--delete").Run()
	}
}

func (*kde) Outputs() ([]Output, error) {
	out, err := exec.Command("kscreen-doctor", "-j").Output()
	if err != nil {
		return nil, fmt.Errorf("kscreen-doctor -j: %w", err)
	}
	return parseKScreen(out)
}

// parseKScreen converts `kscreen-doctor -j`, whose size is in pixels after
// rotation, to outputs in logical size.
func parseKScreen(b []byte) ([]Output, error) {
	var cfg struct {
		Outputs []struct {
			Name    string
			Enabled bool
			Pos     struct{ X, Y int }
			Size    struct{ Width, Height int }
			Scale   float64
		}
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("kscreen-doctor -j: %w", err)
	}
	outs := make([]Output, 0, len(cfg.Outputs))
	for _, ko := range cfg.Outputs {
		var o Output
		o.Name, o.Enabled = ko.Name, ko.Enabled
		o.Position.X, o.Position.Y = ko.Pos.X, ko.Pos.Y
		w, h := ko.Size.Width, ko.Size.Height
		if ko.Scale > 0 {
			w, h = int(float64(w)/ko.Scale+0.5), int(float64(h)/ko.Scale+0.5)
		}
		o.Logical.Width, o.Logical.Height = w, h
		outs = append(outs, o)
	}
	return outs, nil
}
