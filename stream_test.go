package main

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
