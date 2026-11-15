package main

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// The portal's "size" is a D-Bus (ii) struct, which godbus hands back as []any.
func TestMirrorSize(t *testing.T) {
	d := &Display{W: 1920, H: 1080}
	props := map[string]dbus.Variant{"size": dbus.MakeVariant([]any{int32(1921), int32(1200)}), "mapping_id": dbus.MakeVariant("eDP-1")}
	if err := d.checkSource("mirror", props); err != nil || d.W != 1920 || d.H != 1200 {
		t.Fatalf("got %dx%d err=%v", d.W, d.H, err)
	}
	if err := (&Display{}).checkSource("extend", props); err == nil {
		t.Fatal("extend accepted eDP-1")
	}
}
