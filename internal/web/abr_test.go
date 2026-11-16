package web

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// fakeBWE reports GCC stats; unless frozen, each read is a new delay update.
type fakeBWE struct {
	stats  map[string]any
	frozen bool
	n      float64
}

func (f *fakeBWE) AddStream(_ *interceptor.StreamInfo, w interceptor.RTPWriter) interceptor.RTPWriter {
	return w
}
func (f *fakeBWE) WriteRTCP([]rtcp.Packet, interceptor.Attributes) error { return nil }
func (f *fakeBWE) GetTargetBitrate() int                                 { return 0 }
func (f *fakeBWE) OnTargetBitrateChange(func(int))                       {}
func (f *fakeBWE) GetStats() map[string]any {
	st := map[string]any{}
	for k, v := range f.stats {
		st[k] = v
	}
	if !f.frozen {
		f.n++
	}
	st["delayMeasurement"] = f.n
	return st
}
func (f *fakeBWE) Close() error { return nil }

func probe(usage string, loss float64, at time.Time) *bweProbe {
	p := &bweProbe{BandwidthEstimator: &fakeBWE{stats: map[string]any{"usage": usage, "state": "increase", "averageLoss": loss, "delayTargetBitrate": 5_000_000}}}
	p.last.Store(at.UnixNano())
	return p
}

func TestRateCtl(t *testing.T) {
	var got []int
	r := newRateCtl(func(k int) { got = append(got, k) }, 10000)
	t0 := time.Unix(1000, 0)

	// Congestion: down 30% at once, then at most once per abrDownHold.
	bad := probe("overuse", 0, t0)
	r.add(bad)
	for i := range 5 { // just under abrDownHold
		r.tick(t0.Add(time.Duration(i) * abrTick))
	}
	if len(got) != 1 || got[0] != 7000 {
		t.Fatalf("first second of congestion: %v, want [7000]", got)
	}
	for i := 5; i <= 60; i++ { // 11 more seconds
		now := t0.Add(time.Duration(i) * abrTick)
		bad.last.Store(now.UnixNano())
		r.tick(now)
	}
	if r.cur != r.min || r.min != 1000 {
		t.Fatalf("sustained congestion: at %d, want floor 1000", r.cur)
	}

	// Recovery: nothing for abrUpQuiet, then +15% per abrUpHold.
	clean := probe("normal", 0.01, t0)
	r.remove(bad)
	r.add(clean)
	t1 := t0.Add(13 * time.Second)
	got = nil
	for i := 0; i < 25; i++ { // 5 s
		now := t1.Add(time.Duration(i) * abrTick)
		clean.last.Store(now.UnixNano())
		r.tick(now)
	}
	if len(got) != 2 || got[0] != 1150 || got[1] != 1322 {
		t.Fatalf("recovery: %v, want [1150 1322]", got)
	}

	// GCC's zero-value stats (no estimate yet) read "overuse": not congestion.
	fresh := &bweProbe{BandwidthEstimator: &fakeBWE{stats: map[string]any{"usage": "overuse", "state": "increase", "averageLoss": 0.0, "delayTargetBitrate": 0}}}
	fresh.last.Store(t1.UnixNano())
	if c, _ := fresh.congested(t1); c {
		t.Fatal("estimator without an estimate counted as congested")
	}

	// Stats GCC stopped refreshing (it holds without writing them) are not a
	// lasting verdict: congested once, then not until they change.
	held := probe("overuse", 0, t1)
	held.BandwidthEstimator.(*fakeBWE).frozen = true
	if c, _ := held.congested(t1); !c {
		t.Fatal("first overuse report not counted")
	}
	if c, _ := held.congested(t1.Add(abrTick)); c {
		t.Fatal("unchanged overuse stats counted again")
	}

	// Heavy loss alone counts as congestion.
	lossy := probe("normal", 0.2, t1.Add(10*time.Second))
	r.add(lossy)
	before := r.cur
	r.tick(t1.Add(10 * time.Second))
	if r.cur >= before {
		t.Fatalf("20%% loss did not lower the rate (%d -> %d)", before, r.cur)
	}

	// A tiny -kbps keeps the floor at or under it.
	if tiny := newRateCtl(func(int) {}, 200); tiny.min > tiny.max {
		t.Fatalf("floor %d above max %d", tiny.min, tiny.max)
	}

	// Viewers that stopped sending feedback don't count: back to full rate.
	got = nil
	r.tick(t1.Add(time.Minute))
	if len(got) != 1 || got[0] != 10000 {
		t.Fatalf("only stale viewers: %v, want [10000]", got)
	}
}

// A real pion receiver (like a browser) sends transport-cc feedback that
// reaches the viewer's estimator, its PLI reaches the keyframe hook, and the
// estimator goes stale once media stops.
func TestPeerFeedback(t *testing.T) {
	_, newPC, err := webAPI(webrtc.SettingEngine{}, true)
	if err != nil {
		t.Fatal(err)
	}
	vt, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "video", "smon")

	client, err := webrtc.NewPeerConnection(webrtc.Configuration{}) // default interceptors: generates TWCC
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
	ssrc := make(chan webrtc.SSRC, 1)
	client.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		ssrc <- tr.SSRC()
		buf := make([]byte, 1500)
		for {
			if _, _, err := tr.Read(buf); err != nil {
				return
			}
		}
	})
	offer, _ := client.CreateOffer(nil)
	gather := webrtc.GatheringCompletePromise(client)
	client.SetLocalDescription(offer)
	<-gather

	var keys atomic.Int32
	r := newRateCtl(func(int) {}, 8000)
	answer, err := answerOffer(newPC, []webrtc.TrackLocal{vt}, *client.LocalDescription(), nil,
		peerHooks{keyframe: func() { keys.Add(1) }, abr: r})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetRemoteDescription(*answer); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	go func() {
		var seq uint16
		for range time.Tick(5 * time.Millisecond) {
			select {
			case <-stop:
				return
			default:
			}
			seq++
			b, _ := (&rtp.Packet{
				Header:  rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: uint32(seq) * 450, Marker: true},
				Payload: append([]byte{0x41}, make([]byte, 1000)...),
			}).Marshal()
			vt.Write(b)
		}
	}()

	var s webrtc.SSRC
	select {
	case s = <-ssrc:
	case <-time.After(10 * time.Second):
		t.Fatal("no video on the client")
	}
	deadline := time.Now().Add(5 * time.Second)
	for keys.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if keys.Load() == 0 {
		t.Fatal("no keyframe requested when the viewer connected")
	}

	var p *bweProbe
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for v := range r.viewers {
			p = v
		}
		r.mu.Unlock()
		if p != nil {
			if d, _ := p.GetStats()["delayTargetBitrate"].(int); d > 0 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if p == nil {
		t.Fatal("viewer's estimator never registered with the rate controller")
	}
	if c, fresh := p.congested(time.Now()); !fresh || c {
		t.Fatalf("loopback viewer: congested=%v fresh=%v, want uncongested with fresh feedback (stats %v)", c, fresh, p.GetStats())
	}

	before := keys.Load()
	client.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(s)}})
	for keys.Load() == before && time.Now().Before(deadline.Add(2*time.Second)) {
		time.Sleep(10 * time.Millisecond)
	}
	if keys.Load() == before {
		t.Fatal("PLI from the viewer did not request a keyframe")
	}

	close(stop)
	time.Sleep(abrStale + 500*time.Millisecond)
	if _, fresh := p.congested(time.Now()); fresh {
		t.Fatal("estimator still fresh after media stopped")
	}
}
