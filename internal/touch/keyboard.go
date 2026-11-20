package touch

import (
	"sync"

	"smon/internal/logx"
)

// Keyboard is a virtual uinput keyboard fed by viewer key events. It types
// into whatever window has focus, on any output.
type Keyboard struct {
	mu     sync.Mutex
	w      writer
	held   map[int]map[uint16]bool // viewer -> keys it holds down
	warned bool                    // logged an untypeable character
}

// NewKeyboard creates the uinput keyboard.
func NewKeyboard() (*Keyboard, error) {
	keys := make([]uint16, 0, len(keyCodes))
	for _, k := range keyCodes {
		keys = append(keys, k)
	}
	f, err := create(spec{dev: KeyboardDevice, evs: []int{evSyn, evKey}, keys: keys})
	if err != nil {
		return nil, err
	}
	return &Keyboard{w: writer{f: f, name: "keyboard"}, held: map[int]map[uint16]bool{}}, nil
}

// keyEvents returns the events pressing (down) or releasing a key.
func keyEvents(code uint16, down bool) []ev {
	return []ev{{evKey, code, b2i(down)}, {evSyn, synReport, 0}}
}

// Key presses or releases the physical key code (KeyboardEvent.code) for
// viewer owner. Unknown codes are ignored.
func (k *Keyboard) Key(owner int, code string, down bool) {
	c, ok := keyCodes[code]
	if !ok {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	h := k.held[owner]
	if h == nil {
		h = map[uint16]bool{}
		k.held[owner] = h
	}
	if h[c] == down {
		return // a repeat, or a release of a key we never pressed
	}
	h[c] = down
	if !down {
		delete(h, c)
	}
	k.w.write(keyEvents(c, down))
}

// textEvents returns the key presses that type s on a US layout, and how
// many characters had no key.
func textEvents(s string) (out []ev, skipped int) {
	for _, r := range s {
		t, ok := textKeys[r]
		if !ok {
			skipped++
			continue
		}
		if t.shift {
			out = append(out, keyEvents(keyLeftShift, true)...)
		}
		out = append(out, keyEvents(t.code, true)...)
		out = append(out, keyEvents(t.code, false)...)
		if t.shift {
			out = append(out, keyEvents(keyLeftShift, false)...)
		}
	}
	return out, skipped
}

// Text types s, as an on-screen keyboard reports it.
func (k *Keyboard) Text(s string) {
	evs, skipped := textEvents(s)
	k.mu.Lock()
	defer k.mu.Unlock()
	if skipped > 0 && !k.warned {
		k.warned = true
		logx.Warnf("keyboard", "%d character(s) typed on the viewer have no key on a US layout; skipped", skipped)
	}
	k.w.write(evs)
}

// Release lets go of every key viewer owner holds, so a viewer that leaves
// mid-keypress leaves no key stuck down.
func (k *Keyboard) Release(owner int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.w.write(releaseEvents(k.held[owner]))
	delete(k.held, owner)
}

func releaseEvents(held map[uint16]bool) []ev {
	var out []ev
	for c := range held {
		out = append(out, keyEvents(c, false)...)
	}
	return out
}

// Close releases every key and removes the device.
func (k *Keyboard) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, h := range k.held {
		k.w.write(releaseEvents(h))
	}
	k.held = nil
	k.w.destroy()
}
