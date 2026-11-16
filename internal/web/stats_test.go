package web

import (
	"encoding/json"
	"testing"
)

func TestStatsMessage(t *testing.T) {
	src := &statsSource{encoder: "vah264enc", codec: "h264", size: "1920x1080", fps: 60, kbps: 8000}
	src.viewers.Add(2)
	var m streamStats
	if err := json.Unmarshal(src.message(), &m); err != nil {
		t.Fatal(err)
	}
	if m.T != "stats" || m.Encoder != "vah264enc" || m.Kbps != 8000 || m.Max != 8000 || m.Viewers != 2 || m.FPS != 60 {
		t.Fatalf("fixed bitrate: %+v", m)
	}

	src.abr = newRateCtl(func(int) {}, 8000)
	src.abr.cur = 5600 // after a congestion step
	_ = json.Unmarshal(src.message(), &m)
	if m.Kbps != 5600 || m.Max != 8000 {
		t.Fatalf("adaptive bitrate: %+v", m)
	}
}
