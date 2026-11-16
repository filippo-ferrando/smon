package touch

import (
	"os"
	"reflect"
	"testing"
)

func devNull(t *testing.T) *os.File {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestTextEvents(t *testing.T) {
	got, skipped := textEvents("a!é")
	want := []ev{
		{evKey, 30, 1},
		{evSyn, synReport, 0},
		{evKey, 30, 0},
		{evSyn, synReport, 0},
		{evKey, keyLeftShift, 1},
		{evSyn, synReport, 0},
		{evKey, 2, 1},
		{evSyn, synReport, 0},
		{evKey, 2, 0},
		{evSyn, synReport, 0},
		{evKey, keyLeftShift, 0},
		{evSyn, synReport, 0},
	}
	if !reflect.DeepEqual(got, want) || skipped != 1 {
		t.Fatalf("got %v (skipped %d)\nwant %v (skipped 1)", got, skipped, want)
	}
}

func TestTextKeysCoverPrintableASCII(t *testing.T) {
	for r := rune(' '); r <= '~'; r++ {
		if k, ok := textKeys[r]; !ok || k.code == 0 {
			t.Errorf("no key for %q", r)
		}
	}
}

func TestKeyboardReleasesHeldKeys(t *testing.T) {
	k := &Keyboard{w: writer{f: devNull(t), name: "keyboard"}, held: map[int]map[uint16]bool{}}
	k.Key(1, "ControlLeft", true)
	k.Key(1, "KeyC", true)
	k.Key(1, "KeyC", true) // browser auto-repeat: still one press
	k.Key(1, "KeyC", false)
	k.Key(2, "ShiftLeft", true)
	k.Key(1, "NoSuchKey", true)
	if !reflect.DeepEqual(k.held[1], map[uint16]bool{29: true}) {
		t.Fatalf("viewer 1 holds %v, want only ControlLeft", k.held[1])
	}
	if got := releaseEvents(k.held[1]); !reflect.DeepEqual(got, []ev{{evKey, 29, 0}, {evSyn, synReport, 0}}) {
		t.Fatalf("release events %v", got)
	}
	k.Release(1)
	if _, ok := k.held[1]; ok || !k.held[2][42] {
		t.Fatal("release must free viewer 1's keys only")
	}
}

func TestDeviceFor(t *testing.T) {
	if TouchDevice.For(1) != TouchDevice {
		t.Fatal("monitor 1 must keep the plain identity")
	}
	d := PenDevice.For(3)
	if d.Name != "smon pen 3" || d.Product != 0x5361 || !d.Pen || d.Monitor != 3 {
		t.Fatalf("pen for monitor 3: %+v", d)
	}
	if d.Sway() != "4617:21345:smon_pen_3" || d.Hyprland() != "smon-pen-3" {
		t.Fatalf("names: %s %s", d.Sway(), d.Hyprland())
	}
}
