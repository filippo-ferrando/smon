package touch

import (
	"reflect"
	"testing"
)

// A chord presses its keys in order and lets go in reverse, so the
// modifiers are held around the key.
func TestChordEvents(t *testing.T) {
	got, err := chordEvents("MetaLeft", "ControlLeft", "F8")
	if err != nil {
		t.Fatal(err)
	}
	var want []ev
	for _, k := range []struct {
		code uint16
		down bool
	}{{125, true}, {29, true}, {66, true}, {66, false}, {29, false}, {125, false}} {
		want = append(want, keyEvents(k.code, k.down)...)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	if _, err := chordEvents("MetaLeft", "Hyper"); err == nil {
		t.Error("unknown key accepted")
	}
}
