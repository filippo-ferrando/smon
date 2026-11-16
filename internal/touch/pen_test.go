package touch

import (
	"reflect"
	"testing"
)

func TestPenStroke(t *testing.T) {
	s := penState{w: 1001, h: 501}
	// Hover brings the pen in range, without touching.
	got := s.events(PenEvent{Kind: 'h', X: 0.5, Y: 0.5, TiltX: 10, TiltY: -100})
	want := []ev{
		{evKey, btnToolPen, 1},
		{evAbs, absX, 500},
		{evAbs, absY, 250},
		{evAbs, absTiltX, 10},
		{evAbs, absTiltY, -90},
		{evAbs, absPressure, 0},
		{evSyn, synReport, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hover:\n got %v\nwant %v", got, want)
	}

	got = s.events(PenEvent{Kind: 'd', X: 0.5, Y: 0.5, Pressure: 0.5})
	want = []ev{
		{evAbs, absX, 500},
		{evAbs, absY, 250},
		{evAbs, absTiltX, 0},
		{evAbs, absTiltY, 0},
		{evAbs, absPressure, 2048},
		{evKey, btnTouch, 1},
		{evSyn, synReport, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("down:\n got %v\nwant %v", got, want)
	}

	// Zero pressure while down still counts as touching.
	got = s.events(PenEvent{Kind: 'm', X: 0, Y: 0, Pressure: 0, Buttons: PenBarrel})
	if got[4] != (ev{evAbs, absPressure, 1}) || got[5] != (ev{evKey, btnStylus, 1}) {
		t.Fatalf("move with barrel button: %v", got)
	}

	got = s.events(PenEvent{Kind: 'u', X: 0, Y: 0})
	if got[4] != (ev{evAbs, absPressure, 0}) || got[5] != (ev{evKey, btnStylus, 0}) || got[6] != (ev{evKey, btnTouch, 0}) {
		t.Fatalf("up: %v", got)
	}

	got = s.events(PenEvent{Kind: 'l'})
	want = []ev{{evKey, btnToolPen, 0}, {evSyn, synReport, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("leave:\n got %v\nwant %v", got, want)
	}
	if s.events(PenEvent{Kind: 'l'}) != nil {
		t.Fatal("leaving twice should do nothing")
	}
}

func TestPenEraserSwitch(t *testing.T) {
	s := penState{w: 100, h: 100}
	s.events(PenEvent{Kind: 'd', X: 0.1, Y: 0.1, Pressure: 1})
	got := s.events(PenEvent{Kind: 'm', X: 0.1, Y: 0.1, Pressure: 1, Buttons: PenEraser})
	// The pen lifts and leaves, then the eraser comes in and touches.
	want := []ev{
		{evAbs, absPressure, 0},
		{evKey, btnTouch, 0},
		{evKey, btnToolPen, 0},
		{evKey, btnToolRubber, 1},
	}
	if !reflect.DeepEqual(got[:4], want) {
		t.Fatalf("switch to eraser:\n got %v\nwant %v...", got, want)
	}
	if s.tool != btnToolRubber || s.touching {
		t.Fatalf("after switch: tool %#x touching %v", s.tool, s.touching)
	}
}

func TestPenOwnerRelease(t *testing.T) {
	p := &Pen{st: penState{w: 10, h: 10}, w: writer{f: devNull(t), name: "pen"}}
	p.Event(1, PenEvent{Kind: 'd', X: 0.5, Y: 0.5, Pressure: 1})
	p.Event(2, PenEvent{Kind: 'h', X: 0.1, Y: 0.1}) // ignored: viewer 1 holds the pen
	if p.owner != 1 || !p.st.touching {
		t.Fatalf("owner %d touching %v", p.owner, p.st.touching)
	}
	p.Release(2)
	if p.owner != 1 {
		t.Fatal("another viewer's release must not lift the pen")
	}
	p.Release(1)
	if p.owner != 0 || p.st.tool != 0 {
		t.Fatal("release should take the pen out of range")
	}
}
