package gst

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"smon/internal/h264"
)

// encodeHarness runs a live test pattern through encoder enc and the same
// payloader web viewers get, and counts what comes out.
type encodeHarness struct {
	g     *Pipeline
	mu    sync.Mutex
	keys  int
	bytes int
}

func newEncodeHarness(t *testing.T, enc string, kbps, gop int) *encodeHarness {
	t.Helper()
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	udp.SetReadBuffer(4 << 20)
	const w, h, fps = 1280, 720, 30
	g, err := Start(fmt.Sprintf("videotestsrc is-live=true pattern=smpte horizontal-speed=4 ! "+
		"video/x-raw,format=BGRx,width=%d,height=%d,framerate=%d/1 ! %s ! video/x-h264,profile=constrained-baseline ! "+
		"h264parse config-interval=-1 ! rtph264pay config-interval=-1 pt=96 mtu=1200 aggregate-mode=zero-latency ! "+
		"udpsink host=127.0.0.1 port=%d sync=false",
		w, h, fps, encoderChain(enc, w, h, fps, kbps, gop, "queue max-size-buffers=2 leaky=downstream"), udp.LocalAddr().(*net.UDPAddr).Port))
	if err != nil {
		udp.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Stop(); udp.Close() })
	e := &encodeHarness{g: g}
	go func() {
		var asm h264.Assembler
		buf := make([]byte, 1500)
		for {
			n, err := udp.Read(buf)
			if err != nil {
				return
			}
			e.mu.Lock()
			e.bytes += n
			if _, key := asm.Push(buf[:n]); key {
				e.keys++
			}
			e.mu.Unlock()
		}
	}()
	return e
}

func (e *encodeHarness) take() (keys, bytes int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	keys, bytes = e.keys, e.bytes
	e.keys, e.bytes = 0, 0
	return
}

// Every encoder smon may pick must honour a keyframe request at once, and
// change bitrate while playing without a keyframe storm.
func TestEncoderControl(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real encoders for several seconds")
	}
	cands := encoderCandidates()
	if len(cands) == 0 {
		t.Skip("no H.264 encoder installed")
	}
	for _, enc := range cands {
		t.Run(enc, func(t *testing.T) {
			e := newEncodeHarness(t, enc, 4000, 300) // 10 s GOP: only forced keyframes in the window
			time.Sleep(time.Second)
			if keys, bytes := e.take(); bytes == 0 || keys == 0 {
				t.Fatalf("no stream or no first keyframe: %d bytes, %d keyframes", bytes, keys)
			}

			e.g.Keyframe()
			time.Sleep(300 * time.Millisecond)
			if keys, _ := e.take(); keys != 1 {
				t.Errorf("forced keyframe: got %d keyframes in 300 ms, want 1", keys)
			}
			// Requests inside keyInterval merge into one, sent when it ends.
			for range 5 {
				e.g.Keyframe()
			}
			time.Sleep(keyInterval + 300*time.Millisecond)
			if keys, _ := e.take(); keys != 1 {
				t.Errorf("burst of requests: got %d keyframes, want 1", keys)
			}

			time.Sleep(time.Second)
			e.take()
			time.Sleep(2 * time.Second)
			_, high := e.take()
			e.g.SetBitrate(1000)
			time.Sleep(time.Second)
			keysAtChange, _ := e.take()
			time.Sleep(2 * time.Second)
			keysAfter, low := e.take()
			t.Logf("4000 kbps: %d kbit/s; 1000 kbps: %d kbit/s; keyframes from the change: %d, after: %d",
				high*8/2000, low*8/2000, keysAtChange, keysAfter)
			if low > high*6/10 {
				t.Errorf("bitrate did not drop: %d -> %d bytes/2s", high, low)
			}
			// VA-API encoders restart the sequence on a rate change (one IDR,
			// measured on Mesa radeonsi); x264 reconfigures in place. rateCtl
			// steps rarely for that reason.
			want := 1
			if enc == "x264enc" {
				want = 0
			}
			if keysAtChange+keysAfter > want {
				t.Errorf("bitrate change emitted %d keyframes, want <= %d", keysAtChange+keysAfter, want)
			}
		})
	}
}

// Stop and the controls are safe in any order, any number of times.
func TestPipelineLifecycle(t *testing.T) {
	for range 20 {
		g, err := Start("videotestsrc is-live=true ! video/x-raw,width=64,height=64 ! fakesink")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() { defer wg.Done(); g.Keyframe(); g.SetBitrate(500); g.Stop() }()
		}
		wg.Wait()
		g.Stop()
		g.Keyframe()
		g.SetBitrate(100)
		if err := g.Wait(); err != nil {
			t.Fatalf("stopped pipeline reported %v", err)
		}
	}
	if _, err := Start("nosuchelement"); err == nil {
		t.Fatal("bad description started")
	}
}
