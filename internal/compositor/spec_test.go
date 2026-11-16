package compositor

import "testing"

func TestOutputSpecLogical(t *testing.T) {
	for _, c := range []struct {
		spec   OutputSpec
		w, h   int
		scaled bool
		arg    string
	}{
		{OutputSpec{W: 2560, H: 1600}, 2560, 1600, false, "1"},
		{OutputSpec{W: 2560, H: 1600, Scale: 1}, 2560, 1600, false, "1"},
		{OutputSpec{W: 2560, H: 1600, Scale: 2}, 1280, 800, true, "2"},
		{OutputSpec{W: 1920, H: 1080, Scale: 1.5}, 1280, 720, true, "1.5"},
		{OutputSpec{W: 1600, H: 2560, Scale: 1.25}, 1280, 2048, true, "1.25"},
	} {
		w, h := c.spec.Logical()
		if w != c.w || h != c.h || c.spec.Scaled() != c.scaled || c.spec.scaleArg() != c.arg {
			t.Errorf("%+v: logical %dx%d scaled %v arg %s", c.spec, w, h, c.spec.Scaled(), c.spec.scaleArg())
		}
	}
}

func TestScaleCommands(t *testing.T) {
	spec := OutputSpec{W: 2560, H: 1600, FPS: 60, Scale: 2}
	if got, want := swayOutputCmd("HEADLESS-1", spec), "output HEADLESS-1 mode 2560x1600@60Hz scale 2"; got != want {
		t.Errorf("sway: %q, want %q", got, want)
	}
	if got, want := swayOutputCmd("HEADLESS-1", OutputSpec{W: 1920, H: 1080, FPS: 30}), "output HEADLESS-1 mode 1920x1080@30Hz"; got != want {
		t.Errorf("sway unscaled: %q, want %q", got, want)
	}
	if got, want := monitorKeyword("SMON-1", "2560x1600@60", "auto-right", spec.scaleArg()), "SMON-1,2560x1600@60,auto-right,2"; got != want {
		t.Errorf("hyprlang: %q, want %q", got, want)
	}
	if got, want := monitorLua("SMON-1", "2560x1600@60", "auto-right", "1.5"), `hl.monitor({output = "SMON-1", mode = "2560x1600@60", position = "auto-right", scale = "1.5"})`; got != want {
		t.Errorf("lua: %q, want %q", got, want)
	}
}

// A scaled output is placed by its logical size: a 2x 2560-wide tablet
// left of a 1920-wide screen starts 1280 to the left.
func TestPlaceScaled(t *testing.T) {
	var o Output
	o.Name, o.Enabled = "DP-1", true
	o.Logical.Width, o.Logical.Height = 1920, 1080
	w, h := OutputSpec{W: 2560, H: 1600, Scale: 2}.Logical()
	x, y, ok, err := placeXY([]Output{o}, OutputName, "left", w, h)
	if err != nil || !ok || x != -1280 || y != 0 {
		t.Fatalf("placeXY = %d,%d %v %v", x, y, ok, err)
	}
}
