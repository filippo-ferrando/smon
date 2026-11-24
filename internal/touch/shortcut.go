package touch

import (
	"fmt"
	"sync"
)

// ShortcutDevice is the keyboard smon presses compositor shortcuts on (labwc,
// whose actions only keybinds can run). It is a device of its own, apart from
// the viewers' keyboard, so a viewer holding a key cannot get in the way.
var ShortcutDevice = Device{Name: "smon shortcuts", Vendor: Vendor, Product: 0x5343}

// Shortcuts is a uinput keyboard that presses key chords for smon itself.
type Shortcuts struct {
	mu sync.Mutex
	w  writer
}

// NewShortcuts creates the shortcut keyboard. The compositor picks it up a
// moment later, so the first chord pressed right away may be missed.
func NewShortcuts() (*Shortcuts, error) {
	keys := make([]uint16, 0, len(keyCodes))
	for _, k := range keyCodes {
		keys = append(keys, k)
	}
	f, err := create(spec{dev: ShortcutDevice, evs: []int{evSyn, evKey}, keys: keys})
	if err != nil {
		return nil, err
	}
	return &Shortcuts{w: writer{f: f, name: "shortcuts"}}, nil
}

// chordEvents presses keys in order and releases them in reverse.
func chordEvents(keys ...string) ([]ev, error) {
	codes := make([]uint16, len(keys))
	for i, k := range keys {
		c, ok := keyCodes[k]
		if !ok {
			return nil, fmt.Errorf("unknown key %q", k)
		}
		codes[i] = c
	}
	var out []ev
	for _, c := range codes {
		out = append(out, keyEvents(c, true)...)
	}
	for i := len(codes) - 1; i >= 0; i-- {
		out = append(out, keyEvents(codes[i], false)...)
	}
	return out, nil
}

// Press presses the chord keys (KeyboardEvent.code names, modifiers first)
// and lets go of it.
func (s *Shortcuts) Press(keys ...string) error {
	evs, err := chordEvents(keys...)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w.write(evs)
	return nil
}

// Close removes the device.
func (s *Shortcuts) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w.destroy()
}
