package main

import "testing"

const hyprMonitorsJSON = `[
 {"id":0,"name":"eDP-1","x":0,"y":0,"width":2880,"height":1800,"scale":2.0,"transform":0,"disabled":false},
 {"id":1,"name":"DP-1","x":1440,"y":0,"width":1080,"height":1920,"scale":1.0,"transform":1,"disabled":false},
 {"id":2,"name":"HDMI-A-1","x":5000,"y":0,"width":1920,"height":1080,"scale":1.0,"transform":0,"disabled":true}
]`

func TestParseHyprMonitors(t *testing.T) {
	outs, err := parseHyprMonitors([]byte(hyprMonitorsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 3 {
		t.Fatalf("got %d outputs", len(outs))
	}
	if o := outs[0]; o.Name != "eDP-1" || !o.Enabled || o.Logical.Width != 1440 || o.Logical.Height != 900 {
		t.Errorf("scaled output: %+v", o)
	}
	if o := outs[1]; o.Position.X != 1440 || o.Logical.Width != 1920 || o.Logical.Height != 1080 {
		t.Errorf("rotated output: %+v", o)
	}
	if outs[2].Enabled {
		t.Errorf("disabled output reported enabled")
	}
}

func TestPlaceXY(t *testing.T) {
	outs, _ := parseHyprMonitors([]byte(hyprMonitorsJSON))
	// Enabled bounding box: x 0..3360, y 0..1080; HDMI-A-1 is disabled and ignored.
	for _, c := range []struct {
		pos  string
		x, y int
	}{
		{"right", 3360, 0},
		{"left", -1920, 0},
		{"above", 0, -1080},
		{"below", 0, 1080},
	} {
		x, y, ok, err := placeXY(outs, outputName, c.pos, 1920, 1080)
		if err != nil || !ok || x != c.x || y != c.y {
			t.Errorf("%s: got %d,%d ok=%v err=%v, want %d,%d", c.pos, x, y, ok, err, c.x, c.y)
		}
	}
	if _, _, _, err := placeXY(outs, outputName, "diagonal", 1, 1); err == nil {
		t.Error("bad pos accepted")
	}
	if _, _, ok, _ := placeXY(nil, outputName, "right", 1, 1); ok {
		t.Error("ok with no outputs")
	}
}

func TestDetectCompositor(t *testing.T) {
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "x")
	if c, err := detectCompositor(""); err != nil || c.name() != "hyprland" {
		t.Errorf("hyprland env: %v %v", c, err)
	}
	if c, err := detectCompositor("umbriel"); err != nil || c.name() != "umbriel" {
		t.Errorf("flag override: %v %v", c, err)
	}
	if _, err := detectCompositor("niri"); err == nil {
		t.Error("unknown compositor accepted")
	}
}
