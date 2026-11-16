package web

import "testing"

// Audio fills its own queue and never makes a viewer wait for a keyframe.
func TestHubAudioSeparateFromVideo(t *testing.T) {
	var h auHub
	s := h.subscribe()
	h.publish([]byte{wsKey, 1}, true)
	for range 100 { // far more than either queue holds
		h.publishAudio([]byte{wsAudio, 2})
	}
	if s.waitKey {
		t.Fatal("audio overflow set waitKey")
	}
	h.publish([]byte{0, 3}, false) // delta frame still delivered
	if len(s.c) != 2 || len(s.a) != cap(s.a) {
		t.Fatalf("video queue %d, audio queue %d/%d", len(s.c), len(s.a), cap(s.a))
	}
}

// After a lost access unit viewers skip deltas until a keyframe, and one is asked for.
func TestHubLost(t *testing.T) {
	asked := 0
	h := auHub{needKey: func() { asked++ }}
	s := h.subscribe()
	h.publish([]byte{wsKey, 1}, true)
	asked = 0
	h.lost()
	h.publish([]byte{0, 2}, false)
	if len(s.c) != 1 || asked != 1 {
		t.Fatalf("queue %d, keyframes asked %d", len(s.c), asked)
	}
	h.publish([]byte{wsKey, 3}, true)
	if len(s.c) != 2 || h.empty() {
		t.Fatalf("keyframe not delivered")
	}
	h.unsubscribe(s)
	if !h.empty() {
		t.Fatal("hub not empty after unsubscribe")
	}
}
