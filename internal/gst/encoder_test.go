package gst

import (
	"os"
	"path/filepath"
	"testing"
)

// A saved pick round-trips, and ForgetEncoder drops only that output size.
func TestEncoderCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if len(loadEncoderCache()) != 0 {
		t.Fatal("fresh cache not empty")
	}
	cands := []string{"vah264enc", "x264enc"}
	saveEncoderCache(map[string]string{cacheKey(1920, 1080, cands): "x264enc", cacheKey(2560, 1600, cands): "vah264enc"})
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "smon", "encoder.json")); err != nil {
		t.Fatal(err)
	}
	encPicked[[2]int{1920, 1080}] = "x264enc"
	ForgetEncoder(1920, 1080)
	m := loadEncoderCache()
	if _, ok := m[cacheKey(1920, 1080, cands)]; ok {
		t.Error("forgotten pick still saved")
	}
	if m[cacheKey(2560, 1600, cands)] != "vah264enc" {
		t.Error("other size's pick lost")
	}
	if _, ok := encPicked[[2]int{1920, 1080}]; ok {
		t.Error("forgotten pick still in memory")
	}
}
