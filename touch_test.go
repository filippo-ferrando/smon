package main

import (
	"reflect"
	"testing"
)

func TestTouchState(t *testing.T) {
	s := newTouchState(1920, 1080)
	got := s.events('d', 7, 0.5, 1.5) // y clamps to the bottom edge
	want := []ev{
		{evAbs, absMTSlot, 0}, {evAbs, absMTTrackID, 0},
		{evAbs, absMTPositionX, 959}, {evAbs, absMTPositionY, 1079},
		{evKey, btnTouch, 1}, {evAbs, absX, 959}, {evAbs, absY, 1079},
		{evSyn, synReport, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first down:\n got %v\nwant %v", got, want)
	}

	// second finger takes slot 1 and does not move the single-touch axes
	got = s.events('d', 9, 0, 0)
	want = []ev{
		{evAbs, absMTSlot, 1}, {evAbs, absMTTrackID, 1},
		{evAbs, absMTPositionX, 0}, {evAbs, absMTPositionY, 0},
		{evSyn, synReport, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("second down:\n got %v\nwant %v", got, want)
	}

	if s.events('m', 42, 0, 0) != nil || s.events('u', 42, 0, 0) != nil {
		t.Fatal("unknown pointer should be ignored")
	}

	// first finger up: slot 0 freed, BTN_TOUCH stays down
	got = s.events('u', 7, 0, 0)
	want = []ev{{evAbs, absMTSlot, 0}, {evAbs, absMTTrackID, -1}, {evSyn, synReport, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first up:\n got %v\nwant %v", got, want)
	}

	// a new finger reuses slot 0 (already selected, so no ABS_MT_SLOT)
	if got = s.events('d', 3, 0, 0); got[0] != (ev{evAbs, absMTTrackID, 2}) {
		t.Fatalf("reuse: %v", got)
	}

	// release only pointer 9: finger 3 stays, so BTN_TOUCH stays down
	got = s.release(func(id int) bool { return id == 9 })
	want = []ev{{evAbs, absMTSlot, 1}, {evAbs, absMTTrackID, -1}, {evSyn, synReport, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("release 9:\n got %v\nwant %v", got, want)
	}

	got = s.release(nil)
	want = []ev{
		{evAbs, absMTSlot, 0}, {evAbs, absMTTrackID, -1},
		{evKey, btnTouch, 0}, {evSyn, synReport, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("release all:\n got %v\nwant %v", got, want)
	}
	if s.release(nil) != nil {
		t.Fatal("second release should be empty")
	}
}
