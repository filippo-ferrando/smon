package compositor

import "testing"

const kscreenJSON = `{"outputs":[
 {"id":1,"name":"eDP-1","enabled":true,"connected":true,"pos":{"x":0,"y":0},"size":{"width":2880,"height":1800},"scale":2,"rotation":1},
 {"id":2,"name":"Virtual-virtual-xdp-kde-","enabled":true,"connected":true,"pos":{"x":1440,"y":0},"size":{"width":1920,"height":1080},"scale":1,"rotation":1},
 {"id":3,"name":"HDMI-A-1","enabled":false,"connected":false,"pos":{"x":0,"y":0},"size":{"width":-1,"height":-1},"scale":1,"rotation":1}
]}`

func TestParseKScreen(t *testing.T) {
	outs, err := parseKScreen([]byte(kscreenJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 3 {
		t.Fatalf("got %d outputs", len(outs))
	}
	if o := outs[0]; !o.Enabled || o.Logical.Width != 1440 || o.Logical.Height != 900 {
		t.Errorf("scaled output: %+v", o)
	}
	if o := outs[1]; o.Position.X != 1440 || o.Logical.Width != 1920 {
		t.Errorf("virtual output: %+v", o)
	}
	if outs[2].Enabled {
		t.Errorf("disabled output reported enabled")
	}
}
