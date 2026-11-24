package compositor

import (
	"reflect"
	"strings"
	"testing"

	"smon/internal/touch"
)

// `wlr-randr --json` on labwc: a scaled laptop panel, a disabled output, and
// a rotated headless output whose custom mode is its only mode.
const wlrRandrJSON = `[
  {
    "name": "eDP-1", "description": "x", "make": "x", "model": "x", "serial": null,
    "physical_size": {"width": 300, "height": 190},
    "enabled": true,
    "modes": [
      {"width": 2880, "height": 1800, "refresh": 60.000000, "preferred": true, "current": true},
      {"width": 1920, "height": 1200, "refresh": 60.000000, "preferred": false, "current": false}
    ],
    "position": {"x": 0, "y": 0}, "transform": "normal", "scale": 2.000000, "adaptive_sync": false
  },
  {
    "name": "HDMI-A-1", "description": null, "make": null, "model": null, "serial": null,
    "physical_size": {"width": 0, "height": 0},
    "enabled": false,
    "modes": []
  },
  {
    "name": "SMON-1", "description": "Headless output 1", "make": "headless", "model": "headless", "serial": null,
    "physical_size": {"width": 0, "height": 0},
    "enabled": true,
    "modes": [{"width": 2560, "height": 1600, "refresh": 60.000000, "preferred": false, "current": true}],
    "position": {"x": 1440, "y": 0}, "transform": "90", "scale": 1.000000, "adaptive_sync": null
  }
]`

func TestParseWlrRandr(t *testing.T) {
	outs, err := parseWlrRandr([]byte(wlrRandrJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 3 {
		t.Fatalf("got %d outputs", len(outs))
	}
	if o := outs[0]; o.Name != "eDP-1" || !o.Enabled || o.Logical.Width != 1440 || o.Logical.Height != 900 {
		t.Errorf("scaled output: %+v", o)
	}
	if outs[1].Enabled {
		t.Errorf("disabled output reported enabled")
	}
	if o := outs[2]; o.Position.X != 1440 || o.Logical.Width != 1600 || o.Logical.Height != 2560 {
		t.Errorf("rotated headless output: %+v", o)
	}
}

func TestLabwcChords(t *testing.T) {
	bind, keys := labwcChord(3, true)
	if bind != "W-C-F3" || !reflect.DeepEqual(keys, []string{"MetaLeft", "ControlLeft", "F3"}) {
		t.Errorf("add: %q %v", bind, keys)
	}
	bind, keys = labwcChord(8, false)
	if bind != "W-A-F8" || !reflect.DeepEqual(keys, []string{"MetaLeft", "AltLeft", "F8"}) {
		t.Errorf("remove: %q %v", bind, keys)
	}
	want := `<keybind key="W-C-F2"><action name="VirtualOutputAdd" output_name="SMON-2" /></keybind>
<keybind key="W-A-F2"><action name="VirtualOutputRemove" output_name="SMON-2" /></keybind>`
	if got := labwcKeybinds("SMON-2", 2); got != want {
		t.Errorf("keybinds:\n%s\nwant\n%s", got, want)
	}
}

func TestLabwcMonitor(t *testing.T) {
	if n, err := labwcMonitor("SMON-8"); n != 8 || err != nil {
		t.Errorf("SMON-8: %d %v", n, err)
	}
	for _, bad := range []string{"SMON-0", "SMON-9", "HEADLESS-1", "SMON-x", "1"} {
		if _, err := labwcMonitor(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestLabwcPlacedArgs(t *testing.T) {
	var o Output
	o.Name, o.Enabled = "DP-1", true
	o.Logical.Width, o.Logical.Height = 1920, 1080
	spec := OutputSpec{W: 2560, H: 1600, FPS: 60, Scale: 2, Pos: "left"}
	got, err := labwcPlacedArgs("SMON-1", spec, []Output{o})
	want := "--output SMON-1 --custom-mode 2560x1600@60Hz --scale 2 --pos -1280,0"
	if err != nil || strings.Join(got, " ") != want {
		t.Errorf("placed: %q %v, want %q", got, err, want)
	}
	spec.Pos, spec.Scale = "", 0
	got, err = labwcPlacedArgs("SMON-1", spec, nil)
	if want := "--output SMON-1 --custom-mode 2560x1600@60Hz --scale 1"; err != nil || strings.Join(got, " ") != want {
		t.Errorf("unplaced: %q %v", got, err)
	}
	if _, err := labwcPlacedArgs("SMON-1", OutputSpec{W: 1, H: 1, Pos: "behind"}, []Output{o}); err == nil {
		t.Error("bad -pos accepted")
	}
}

func TestLabwcMapsInput(t *testing.T) {
	rc := `<?xml version="1.0"?>
<labwc_config>
  <touch deviceName="SMON TOUCHSCREEN" mapToOutput="SMON-1" />
  <touch>
    <deviceName>smon touchscreen 2</deviceName>
    <mapToOutput>SMON-2</mapToOutput>
  </touch>
  <tablet mapToOutput="SMON-1"><area top="0" left="0" width="0" height="0" /></tablet>
</labwc_config>`
	for _, c := range []struct {
		dev    touch.Device
		output string
		want   bool
	}{
		{touch.TouchDevice, "SMON-1", true},         // attributes, name in any case
		{touch.TouchDevice.For(2), "SMON-2", true},  // child elements
		{touch.TouchDevice, "SMON-2", false},        // wrong output
		{touch.TouchDevice.For(3), "SMON-3", false}, // no rule
		{touch.PenDevice, "SMON-1", true},           // the one tablet rule
		{touch.PenDevice.For(2), "SMON-2", false},   // tablets map to one output only
	} {
		got, err := labwcMapsInput([]byte(rc), c.dev, c.output)
		if err != nil || got != c.want {
			t.Errorf("%s -> %s: %v %v, want %v", c.dev.Name, c.output, got, err, c.want)
		}
	}
	if _, err := labwcMapsInput([]byte("<labwc_config>"), touch.TouchDevice, "SMON-1"); err == nil {
		t.Error("broken rc.xml accepted")
	}
	if got := labwcInputRule(touch.TouchDevice.For(2), "SMON-2"); got != `<touch deviceName="smon touchscreen 2" mapToOutput="SMON-2" />` {
		t.Errorf("touch rule %s", got)
	}
	if got := labwcInputRule(touch.PenDevice, "SMON-1"); got != `<tablet mapToOutput="SMON-1" />` {
		t.Errorf("tablet rule %s", got)
	}
}
