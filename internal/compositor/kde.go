package compositor

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
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
func (*kde) CreateOutput(int, int, int, string) (string, error) { return "", nil }

func (*kde) DestroyOutput(string) {} // closing the portal session removes it

func (*kde) WatchLayout(chan struct{}) {} // KWin saves the layout itself

// PlaceOutput implements PortalOutput.
func (k *kde) PlaceOutput(name, pos string, w, h int) error {
	if pos == "" {
		return nil // where KWin put it
	}
	outs, err := k.Outputs()
	if err != nil {
		return err
	}
	x, y, ok, err := placeXY(outs, name, pos, w, h)
	if err != nil || !ok {
		return err
	}
	arg := fmt.Sprintf("output.%s.position.%d,%d", name, x, y)
	if out, err := exec.Command("kscreen-doctor", arg).CombinedOutput(); err != nil {
		return fmt.Errorf("kscreen-doctor %s: %v: %s", arg, err, out)
	}
	return nil
}

const (
	kwinDest       = "org.kde.KWin"
	kwinInputPath  = "/org/kde/KWin/InputDevice"
	kwinInputIface = "org.kde.KWin.InputDevice"
)

// MapTouch finds smon's touchscreen among KWin's input devices (it shows up
// shortly after uinput creates it) and sets its outputName.
func (*kde) MapTouch(output string) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		path, err := kwinTouchDevice(conn)
		if err != nil {
			return err
		}
		if path != "" {
			if err := conn.Object(kwinDest, path).SetProperty(kwinInputIface+".outputName", output); err != nil {
				return fmt.Errorf("KWin: map %s to %s: %w", touch.Name, output, err)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("KWin did not list %q among its input devices", touch.Name)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// kwinTouchDevice returns the D-Bus path of smon's touchscreen, or "" while
// KWin does not know it yet.
func kwinTouchDevice(conn *dbus.Conn) (dbus.ObjectPath, error) {
	v, err := conn.Object(kwinDest, kwinInputPath).GetProperty("org.kde.KWin.InputDeviceManager.devicesSysNames")
	if err != nil {
		return "", fmt.Errorf("KWin input devices: %w", err)
	}
	names, _ := v.Value().([]string)
	for _, sys := range names {
		path := dbus.ObjectPath(kwinInputPath + "/" + sys)
		n, err := conn.Object(kwinDest, path).GetProperty(kwinInputIface + ".name")
		if err == nil && n.Value() == touch.Name {
			return path, nil
		}
	}
	return "", nil
}

// UnmapTouch removes the mapping KWin saved in kcminputrc for smon's
// touchscreen; the device itself is already gone.
func (*kde) UnmapTouch() {
	for _, key := range []string{"OutputUuid", "OutputName"} {
		_ = exec.Command("kwriteconfig6", "--file", "kcminputrc",
			"--group", "Libinput", "--group", strconv.Itoa(touch.Vendor), "--group", strconv.Itoa(touch.Product),
			"--group", touch.Name, "--key", key, "--delete").Run()
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
