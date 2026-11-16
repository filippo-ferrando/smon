package web

import "testing"

// Messages for devices that are off, or malformed ones, are dropped without
// touching anything.
func TestInputsDropWhatIsOff(t *testing.T) {
	var none *inputs
	if none.enabled() {
		t.Fatal("nil inputs enabled")
	}
	in := &inputs{}
	if in.enabled() {
		t.Fatal("no devices but enabled")
	}
	for _, m := range []string{
		`{"t":"d","id":1,"x":0.5,"y":0.5}`,
		`{"t":"h","pt":"pen","x":0.5,"y":0.5,"p":0.3}`,
		`{"t":"k","c":"KeyA","d":1}`,
		`{"t":"s","s":"hello"}`,
		`{"t":"dd"}`,
		`not json`,
	} {
		in.handle(1, []byte(m)) // must not panic on nil devices
	}
	in.release(1)
}
