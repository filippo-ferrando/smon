package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeMonitors struct {
	mu        sync.Mutex
	created   []string // "n:portrait"
	destroyed []int
	fail      bool
}

func (f *fakeMonitors) create(n int, portrait bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("share dialog cancelled")
	}
	f.created = append(f.created, fmt.Sprintf("%d:%v", n, portrait))
	return nil
}

func (f *fakeMonitors) destroy(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroyed = append(f.destroyed, n)
}

func newTestAllocator(max int) (*allocator, *fakeMonitors, *time.Time) {
	f := &fakeMonitors{}
	now := time.Unix(1000, 0)
	a := newAllocator(max, f.create, f.destroy)
	a.now = func() time.Time { return now }
	a.seats[1].created = now
	return a, f, &now
}

func mustClaim(t *testing.T, a *allocator, cid string, want int, portrait bool) int {
	t.Helper()
	n, err := a.claim(cid, want, portrait)
	if err != nil {
		t.Fatalf("claim %s: %v", cid, err)
	}
	return n
}

func TestAllocatorOneMonitorIsShared(t *testing.T) {
	a, f, _ := newTestAllocator(1)
	for _, cid := range []string{"a", "b", "c"} {
		if n := mustClaim(t, a, cid, 0, false); n != 1 {
			t.Fatalf("%s got monitor %d", cid, n)
		}
		a.join(1, cid)
	}
	if len(f.created) != 0 || a.viewers(1) != 3 {
		t.Fatalf("created %v, viewers %d", f.created, a.viewers(1))
	}
}

func TestAllocatorOnDemand(t *testing.T) {
	a, f, now := newTestAllocator(3)
	if n := mustClaim(t, a, "tablet", 0, false); n != 1 {
		t.Fatalf("first viewer got %d", n)
	}
	a.join(1, "tablet")
	if n := mustClaim(t, a, "phone", 0, true); n != 2 {
		t.Fatalf("second viewer got %d", n)
	}
	a.join(2, "phone")
	if !slices.Equal(f.created, []string{"2:true"}) {
		t.Fatalf("created %v, want a portrait monitor 2", f.created)
	}

	// A reload is the same viewer: same monitor, nothing new created.
	a.leave(2, "phone")
	*now = now.Add(5 * time.Second)
	if n := mustClaim(t, a, "phone", 0, true); n != 2 {
		t.Fatalf("reload got %d", n)
	}
	a.join(2, "phone")

	// Watching a monitor that exists; a missing one is an error.
	if n := mustClaim(t, a, "laptop", 1, false); n != 1 {
		t.Fatalf("?m=1 got %d", n)
	}
	if _, err := a.claim("laptop", 7, false); !errors.Is(err, errNoMonitor) {
		t.Fatalf("?m=7: %v", err)
	}

	mustClaim(t, a, "tv", 0, false) // monitor 3
	if _, err := a.claim("extra", 0, false); !errors.Is(err, errFull) {
		t.Fatalf("over the cap: %v", err)
	}
}

func TestAllocatorReapsIdleMonitors(t *testing.T) {
	a, f, now := newTestAllocator(2)
	a.join(1, mustClaimNoT(a, "a"))
	n := mustClaim(t, a, "b", 0, false)
	a.join(n, "b")
	a.leave(n, "b")
	a.leave(1, "a")

	*now = now.Add(monitorGrace - time.Second)
	a.reap()
	if len(f.destroyed) != 0 {
		t.Fatal("reaped within grace")
	}
	*now = now.Add(2 * time.Second)
	a.reap()
	if !slices.Equal(f.destroyed, []int{2}) {
		t.Fatalf("destroyed %v, want only monitor 2 (monitor 1 stays)", f.destroyed)
	}
	// Monitor 1 is free again for a newcomer; the next one is made anew.
	if n := mustClaim(t, a, "c", 0, false); n != 1 {
		t.Fatalf("newcomer got %d", n)
	}
	a.join(1, "c")
	if n := mustClaim(t, a, "d", 0, false); n != 2 || len(f.created) != 2 {
		t.Fatalf("got %d, created %v", n, f.created)
	}
}

// A claimed monitor nobody connects to is reaped too; a failed create
// leaves nothing behind.
func TestAllocatorUnusedAndFailed(t *testing.T) {
	a, f, now := newTestAllocator(3)
	a.join(1, "a")
	f.fail = true
	if _, err := a.claim("b", 0, false); err == nil {
		t.Fatal("failed create should fail the claim")
	}
	f.fail = false
	n := mustClaim(t, a, "b", 0, false) // never joins
	*now = now.Add(monitorGrace)
	a.reap()
	if !slices.Equal(f.destroyed, []int{n}) {
		t.Fatalf("destroyed %v", f.destroyed)
	}
}

func mustClaimNoT(a *allocator, cid string) string {
	if _, err := a.claim(cid, 0, false); err != nil {
		panic(err)
	}
	return cid
}

func TestHandleClaim(t *testing.T) {
	a, _, _ := newTestAllocator(2)
	s := &webServer{token: "tok", codec: videoCodecs["h264"], alloc: a, mons: map[int]*monitor{}}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()
	post := func(body string) (int, string) {
		res, err := http.Post(srv.URL+"/claim?t=tok", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, strings.TrimSpace(string(b))
	}
	if code, body := post(`{"cid":"a"}`); code != 200 || body != `{"m":1}` {
		t.Fatalf("first claim: %d %s", code, body)
	}
	a.join(1, "a")
	if code, body := post(`{"cid":"b","portrait":true}`); code != 200 || body != `{"m":2}` {
		t.Fatalf("second claim: %d %s", code, body)
	}
	a.join(2, "b")
	if code, body := post(`{"cid":"c"}`); code != http.StatusConflict || !strings.Contains(body, "&m=1") {
		t.Fatalf("over the cap: %d %s", code, body)
	}
	if code, _ := post(`{"cid":"c","m":5}`); code != http.StatusNotFound {
		t.Fatalf("missing monitor: %d", code)
	}
	if code, _ := post(`{}`); code != http.StatusBadRequest {
		t.Fatalf("no client id: %d", code)
	}
	// An offer for a monitor that closed since the claim: claim again.
	res, err := http.Post(srv.URL+"/offer?t=tok&m=2&c=b", "application/json", strings.NewReader(`{"type":"offer","sdp":"a=rtpmap:96 H264/90000"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("offer to a gone monitor: %d", res.StatusCode)
	}
}
