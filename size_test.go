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

// xdg-desktop-portal-hyprland lets the user pick a window or region even
// though smon asks for monitors; those streams carry no mapping_id.
func TestRejectNonMonitorSource(t *testing.T) {
	window := map[string]dbus.Variant{"source_type": dbus.MakeVariant(uint32(2))}
	for _, mode := range []string{"extend", "mirror"} {
		if err := (&Display{}).checkSource(mode, window); err == nil {
			t.Errorf("%s accepted a window", mode)
		}
	}
	monitor := map[string]dbus.Variant{"source_type": dbus.MakeVariant(uint32(1)), "mapping_id": dbus.MakeVariant(outputName)}
	d := &Display{}
	if err := d.checkSource("extend", monitor); err != nil || d.output != outputName {
		t.Errorf("extend rejected SMON-1: %v", err)
	}
}
