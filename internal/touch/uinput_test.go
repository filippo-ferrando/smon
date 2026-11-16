package touch

import (
	"os"
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
	for _, d := range []Device{TouchDevice, PenDevice, KeyboardDevice} {
		if !strings.Contains(string(b), `N: Name="`+d.Name+`"`) {
			t.Errorf("%s not listed", d.Name)
		}
	}
}
