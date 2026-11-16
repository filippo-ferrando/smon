package compositor

import (
	"testing"

	"smon/internal/touch"
)

const swayOutputsJSON = `[
 {"name":"eDP-1","active":true,"rect":{"x":0,"y":0,"width":1440,"height":900},"scale":2.0,"transform":"normal"},
 {"name":"HDMI-A-1","active":false},
 {"name":"HEADLESS-2","active":true,"rect":{"x":1440,"y":0,"width":1920,"height":1080},"scale":1.0,"transform":"normal"}
]`

func TestParseSwayOutputs(t *testing.T) {
	outs, err := parseSwayOutputs([]byte(swayOutputsJSON))
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
		t.Errorf("inactive output reported enabled")
	}
	if o := outs[2]; o.Position.X != 1440 || o.Logical.Width != 1920 {
		t.Errorf("headless output: %+v", o)
	}
	if n := newOutput(outs[:2], outs); n != "HEADLESS-2" {
		t.Errorf("newOutput = %q", n)
	}
}

func TestSwayReply(t *testing.T) {
	if err := swayReply([]byte(`[{"success":true},{"success":false,"error":"Unknown/invalid command 'x'"}]`)); err == nil || err.Error() != "Unknown/invalid command 'x'" {
		t.Errorf("got %v", err)
	}
	if err := swayReply([]byte(`[{"success":true}]`)); err != nil {
		t.Errorf("got %v", err)
	}
	if id := touch.TouchDevice.Sway(); id != "4617:21312:smon_touchscreen" {
		t.Errorf("touchscreen id = %q", id)
	}
	if id := touch.PenDevice.Sway(); id != "4617:21313:smon_pen" {
		t.Errorf("pen id = %q", id)
	}
}
