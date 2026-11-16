package touch

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestDevicesAppear creates each device for real and checks the kernel lists
// it with the identity compositors map by. It needs write access to
// /dev/uinput, so it only runs with SMON_UINPUT_TEST=1. Nothing is injected.
func TestDevicesAppear(t *testing.T) {
	if os.Getenv("SMON_UINPUT_TEST") != "1" {
		t.Skip("set SMON_UINPUT_TEST=1 to create real uinput devices")
	}
	tch, err := New(TouchDevice, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	defer tch.Close()
	pen, err := NewPen(PenDevice, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	defer pen.Close()
	kbd, err := NewKeyboard()
	if err != nil {
		t.Fatal(err)
	}
	defer kbd.Close()
	time.Sleep(200 * time.Millisecond)
	b, err := os.ReadFile("/proc/bus/input/devices")
	if err != nil {
		t.Fatal(err)
	}
	// udev must class each device the way compositors (libinput) need: the
	// pen mapping only applies to a tablet.
	for _, c := range []struct {
		dev  Device
		prop string
	}{
		{TouchDevice, "ID_INPUT_TOUCHSCREEN=1"},
		{PenDevice, "ID_INPUT_TABLET=1"},
		{KeyboardDevice, "ID_INPUT_KEYBOARD=1"},
	} {
		node := eventNode(string(b), c.dev.Name)
		if node == "" {
			t.Errorf("%s not listed", c.dev.Name)
			continue
		}
		out, err := exec.Command("udevadm", "info", "--query=property", "--name=/dev/input/"+node).Output()
		if err != nil {
			t.Logf("udevadm: %v (skipping the class check)", err)
			continue
		}
		if !strings.Contains(string(out), c.prop) {
			t.Errorf("%s (%s): udev lacks %s:\n%s", c.dev.Name, node, c.prop, out)
		}
	}
}

// eventNode finds the evdev node (event<n>) of the device called name in
// /proc/bus/input/devices.
func eventNode(devices, name string) string {
	for block := range strings.SplitSeq(devices, "\n\n") {
		if !strings.Contains(block, `N: Name="`+name+`"`) {
			continue
		}
		for _, f := range strings.Fields(strings.ReplaceAll(block, "Handlers=", "Handlers= ")) {
			if strings.HasPrefix(f, "event") {
				return f
			}
		}
	}
	return ""
}
