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
	saveEncoderCache(map[string]string{cacheKey(H264, 1920, 1080, cands): "x264enc", cacheKey(H264, 2560, 1600, cands): "vah264enc"})
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CACHE_HOME"), "smon", "encoder.json")); err != nil {
		t.Fatal(err)
	}
	encPicked[keyFor(H264, 1920, 1080)] = "x264enc"
	ForgetEncoder(H264, 1920, 1080)
	m := loadEncoderCache()
	if _, ok := m[cacheKey(H264, 1920, 1080, cands)]; ok {
		t.Error("forgotten pick still saved")
	}
	if m[cacheKey(H264, 2560, 1600, cands)] != "vah264enc" {
		t.Error("other size's pick lost")
	}
	if _, ok := encPicked[keyFor(H264, 1920, 1080)]; ok {
		t.Error("forgotten pick still in memory")
	}
}

// A portrait output shares the landscape output's pick.
func TestSizeKeyPortrait(t *testing.T) {
	cands := []string{"vah264enc", "x264enc"}
	if cacheKey(H264, 1080, 1920, cands) != cacheKey(H264, 1920, 1080, cands) || sizeKey(1600, 2560) != sizeKey(2560, 1600) {
		t.Fatal("portrait and landscape keys differ")
	}
	if cacheKey(H264, 1920, 1080, cands) != "1920x1080 vah264enc,x264enc" {
		t.Fatalf("landscape key changed: %q (saved picks would be lost)", cacheKey(H264, 1920, 1080, cands))
	}
}
