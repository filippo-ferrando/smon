package web

import (
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// Adaptive bitrate for WebRTC viewers.
//
// Each peer connection runs pion's GCC send-side estimator on transport-cc
// feedback from the browser. Its target bitrate isn't used directly: GCC caps
// increases at 1.5x the rate actually sent, so on an idle desktop (a few
// hundred kbit/s) the target sinks to the floor and the next burst of motion
// comes out blurred while it climbs back. Instead its congestion signals
// (delay overuse, loss) drive a plain AIMD controller between -kbps and a
// floor, and the encoder bitrate follows. The encoder is shared, so the most
// congested viewer sets the rate for everyone. SMON_ABR=off disables it.

const (
	abrTick     = 200 * time.Millisecond
	abrDownHold = time.Second     // between decreases: let the last one take effect
	abrUpQuiet  = 3 * time.Second // no congestion for this long before increasing
	abrUpHold   = 2 * time.Second // between increases
	abrStale    = 2 * time.Second // no feedback for this long: viewer gives no signal
)

// bweProbe is a viewer's estimator, noting when transport-cc feedback last
// arrived so a stalled viewer stops counting.
type bweProbe struct {
	cc.BandwidthEstimator
	last atomic.Int64 // unix ns of the last transport-cc feedback
	prev string       // stats at the previous check (rateCtl.mu)
}

func (b *bweProbe) WriteRTCP(pkts []rtcp.Packet, attr interceptor.Attributes) error {
	for _, p := range pkts {
		if _, ok := p.(*rtcp.TransportLayerCC); ok {
			b.last.Store(time.Now().UnixNano())
			break
		}
	}
	return b.BandwidthEstimator.WriteRTCP(pkts, attr)
}

// congested reports whether the viewer's link is past capacity, and whether
// it has said anything recently at all.
//
// GCC's stats only refresh on some delay updates (not while it holds), so an
// old "overuse" can outlive the congestion: only stats that changed since the
// last check count. And until its first delay estimate they are zero values,
// whose usage reads "overuse": the delay signal needs a target first.
func (b *bweProbe) congested(now time.Time) (congested, fresh bool) {
	if now.Sub(time.Unix(0, b.last.Load())) > abrStale {
		return false, false
	}
	st := b.GetStats()
	key := fmt.Sprint(st["delayMeasurement"], st["delayEstimate"], st["usage"], st["state"], st["averageLoss"])
	changed := key != b.prev
	b.prev = key
	if !changed {
		return false, true
	}
	loss, _ := st["averageLoss"].(float64)
	delay, _ := st["delayTargetBitrate"].(int)
	return delay > 0 && (st["usage"] == "overuse" || st["state"] == "decrease") || loss > 0.1, true
}

// rateCtl sets the encoder bitrate from the viewers' congestion signals.
type rateCtl struct {
	set      func(kbps int)
	max, min int

	mu                              sync.Mutex
	cur                             int
	viewers                         map[*bweProbe]struct{}
	lastDown, lastUp, lastCongested time.Time

	done     chan struct{} // closed by stop: run returns
	stopOnce sync.Once
}

func newRateCtl(set func(kbps int), maxKbps int) *rateCtl {
	return &rateCtl{set: set, max: maxKbps, min: min(max(maxKbps/10, 300), maxKbps), cur: maxKbps, viewers: map[*bweProbe]struct{}{}, done: make(chan struct{})}
}

// current returns the encoder bitrate now, and the most it may climb to.
func (r *rateCtl) current() (cur, maxKbps int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cur, r.max
}

func (r *rateCtl) add(p *bweProbe) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.viewers[p] = struct{}{}
}

func (r *rateCtl) remove(p *bweProbe) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.viewers, p)
}

// run adapts the bitrate every abrTick until stop.
func (r *rateCtl) run() {
	t := time.NewTicker(abrTick)
	defer t.Stop()
	for {
		select {
		case now := <-t.C:
			r.tick(now)
		case <-r.done:
			return
		}
	}
}

func (r *rateCtl) stop() { r.stopOnce.Do(func() { close(r.done) }) }

func (r *rateCtl) tick(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	congested, active := false, false
	for p := range r.viewers {
		c, fresh := p.congested(now)
		congested = congested || c
		active = active || fresh
	}
	next := r.cur
	switch {
	case !active: // nobody to adapt to (or no transport-cc): full quality
		next = r.max
	case congested:
		r.lastCongested = now
		if now.Sub(r.lastDown) >= abrDownHold {
			next = max(r.min, r.cur*7/10)
			r.lastDown = now
		}
	case now.Sub(r.lastCongested) >= abrUpQuiet && now.Sub(r.lastUp) >= abrUpHold:
		next = min(r.max, r.cur*115/100)
		r.lastUp = now
	}
	if next != r.cur {
		log.Printf("bitrate %d -> %d kbps", r.cur, next)
		r.cur = next
		r.set(next)
	}
}

// webAPI builds the pion API for viewers: default codecs and interceptors
// (NACK, RTCP reports), plus transport-cc and a GCC estimator per peer
// connection when abr is on. newPC creates a peer connection and returns its
// estimator (nil without abr).
func webAPI(se webrtc.SettingEngine, abr bool) (api *webrtc.API, newPC func() (*webrtc.PeerConnection, *bweProbe, error), err error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		return nil, nil, err
	}
	reg := &interceptor.Registry{}
	var mu sync.Mutex
	var pending *bweProbe // set by the factory inside NewPeerConnection, under mu
	if abr {
		f, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
			// No pacing: it would queue frames, which is latency.
			bwe, err := gcc.NewSendSideBWE(gcc.SendSideBWEPacer(gcc.NewNoOpPacer()))
			if err != nil {
				return nil, err
			}
			return &bweProbe{BandwidthEstimator: bwe}, nil
		})
		if err != nil {
			return nil, nil, err
		}
		f.OnNewPeerConnection(func(_ string, e cc.BandwidthEstimator) { pending, _ = e.(*bweProbe) })
		reg.Add(f)
		if err := webrtc.ConfigureTWCCHeaderExtensionSender(m, reg); err != nil {
			return nil, nil, err
		}
	}
	if err := webrtc.RegisterDefaultInterceptors(m, reg); err != nil {
		return nil, nil, err
	}
	api = webrtc.NewAPI(webrtc.WithSettingEngine(se), webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(reg))
	newPC = func() (*webrtc.PeerConnection, *bweProbe, error) {
		mu.Lock()
		defer mu.Unlock()
		pending = nil
		pc, err := api.NewPeerConnection(webrtc.Configuration{}) // LAN only, no STUN
		return pc, pending, err
	}
	return api, newPC, nil
}

func abrEnabled() bool { return os.Getenv("SMON_ABR") != "off" }
