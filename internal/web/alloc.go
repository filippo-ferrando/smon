package web

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

// monitorGrace is how long a monitor stays a viewer's after it disconnects:
// a reload or a Wi-Fi blip gets the same monitor back. Monitors after the
// first close once they have had no viewer for this long.
const monitorGrace = 30 * time.Second

var (
	errNoMonitor = errors.New("no such monitor")
	errFull      = errors.New("all monitors in use")
)

// allocator decides which monitor each viewer gets, creating monitors on
// demand up to max, and closing idle ones. Viewers are told apart by a
// client id their page keeps, so a reload is the same viewer.
type allocator struct {
	max     int
	grace   time.Duration
	now     func() time.Time
	create  func(n int, portrait bool) error // makes monitor n; called without mu
	destroy func(n int)                      // closes monitor n; called without mu

	mu      sync.Mutex
	seats   map[int]*seat
	pending map[int]bool // being created
}

// seat is one monitor's viewers.
type seat struct {
	conns   map[string]int       // client id -> open connections
	left    map[string]time.Time // client id -> when it last claimed or disconnected
	created time.Time
}

func newAllocator(max int, create func(int, bool) error, destroy func(int)) *allocator {
	a := &allocator{
		max: max, grace: monitorGrace, now: time.Now, create: create, destroy: destroy,
		seats: map[int]*seat{}, pending: map[int]bool{},
	}
	a.seats[1] = newSeat(a.now()) // monitor 1 exists from the start
	return a
}

func newSeat(now time.Time) *seat {
	return &seat{conns: map[string]int{}, left: map[string]time.Time{}, created: now}
}

// holds reports whether client cid has this monitor: connected, or
// claimed or disconnected within grace.
func (s *seat) holds(cid string, now time.Time, grace time.Duration) bool {
	if s.conns[cid] > 0 {
		return true
	}
	t, ok := s.left[cid]
	return ok && now.Sub(t) < grace
}

// free reports whether nobody holds the monitor.
func (s *seat) free(now time.Time, grace time.Duration) bool {
	if len(s.conns) > 0 {
		return false
	}
	for _, t := range s.left {
		if now.Sub(t) < grace {
			return false
		}
	}
	return true
}

// claim returns the monitor for client cid: the one it already has, the one
// it asks for (want > 0, to watch it), a free one, or a new one. With one
// monitor allowed everybody shares it, as before smon had several.
func (a *allocator) claim(cid string, want int, portrait bool) (int, error) {
	a.mu.Lock()
	now := a.now()
	if want > 0 {
		s, ok := a.seats[want]
		if !ok {
			a.mu.Unlock()
			return 0, fmt.Errorf("%w: %d", errNoMonitor, want)
		}
		if s.conns[cid] == 0 {
			s.left[cid] = now
		}
		a.mu.Unlock()
		return want, nil
	}
	order := slices.Sorted(maps.Keys(a.seats))
	for _, n := range order {
		if a.seats[n].holds(cid, now, a.grace) {
			a.reserve(n, cid, now)
			a.mu.Unlock()
			return n, nil
		}
	}
	if a.max <= 1 {
		a.reserve(1, cid, now)
		a.mu.Unlock()
		return 1, nil
	}
	for _, n := range order {
		if a.seats[n].free(now, a.grace) {
			a.reserve(n, cid, now)
			a.mu.Unlock()
			return n, nil
		}
	}
	if len(a.seats)+len(a.pending) >= a.max {
		a.mu.Unlock()
		return 0, errFull
	}
	n := 2
	for a.seats[n] != nil || a.pending[n] {
		n++
	}
	a.pending[n] = true
	a.mu.Unlock()

	err := a.create(n, portrait)

	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, n)
	if err != nil {
		return 0, err
	}
	a.seats[n] = newSeat(a.now())
	a.reserve(n, cid, a.now())
	return n, nil
}

// reserve marks monitor n as cid's until it connects (or grace runs out).
func (a *allocator) reserve(n int, cid string, now time.Time) {
	if s := a.seats[n]; s != nil && s.conns[cid] == 0 {
		s.left[cid] = now
	}
}

// join and leave count client cid's connections to monitor n.
func (a *allocator) join(n int, cid string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.seats[n]; s != nil {
		s.conns[cid]++
		delete(s.left, cid)
	}
}

func (a *allocator) leave(n int, cid string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.seats[n]
	if s == nil || s.conns[cid] == 0 {
		return
	}
	if s.conns[cid]--; s.conns[cid] == 0 {
		delete(s.conns, cid)
		s.left[cid] = a.now()
	}
}

// reap closes the monitors after the first that nobody has held for grace.
func (a *allocator) reap() {
	a.mu.Lock()
	now := a.now()
	var idle []int
	for n, s := range a.seats {
		if n > 1 && s.free(now, a.grace) && now.Sub(s.created) >= a.grace {
			delete(a.seats, n)
			idle = append(idle, n)
		}
	}
	a.mu.Unlock()
	for _, n := range idle {
		a.destroy(n)
	}
}

// count is the number of monitors and the viewers connected to monitor n.
func (a *allocator) viewers(n int) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.seats[n]; s != nil {
		return len(s.conns)
	}
	return 0
}
