// Package compositor creates and places smon's virtual output, and maps its
// touchscreen, on the supported Wayland compositors.
package compositor

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// OutputName is the virtual output smon creates in extend mode where the
// compositor lets it pick the name (umbriel, Hyprland).
const OutputName = "SMON-1"

// Compositor is everything smon needs from the compositor beyond the
// xdg-desktop-portal ScreenCast interface, which is the same on all of them.
type Compositor interface {
	Name() string
	// CreateOutput adds the virtual output (removing a stale one from a crash
	// first), places it, and returns its name: pos is "" (compositor default)
	// or right|left|above|below. It returns "" when the ScreenCast portal
	// creates the output instead (see PortalOutput).
	CreateOutput(w, h, fps int, pos string) (string, error)
	DestroyOutput(name string)
	// WatchLayout runs until done is closed; a no-op where it does not apply.
	WatchLayout(done chan struct{})
	// MapTouch maps smon's virtual touchscreen, which already exists, to an
	// output; UnmapTouch undoes it after the touchscreen is gone.
	MapTouch(output string) error
	UnmapTouch()
	Outputs() ([]Output, error)
}

// PortalOutput is implemented by compositors whose virtual output is created
// by the ScreenCast portal (source type VIRTUAL) rather than by CreateOutput,
// and removed when the portal session closes.
type PortalOutput interface {
	// PlaceOutput positions the portal-made output once its name is known;
	// pos is as for CreateOutput.
	PlaceOutput(name, pos string, w, h int) error
}

// Output is one monitor and where it sits in the layout, in logical pixels.
type Output struct {
	Name     string
	Enabled  bool
	Position struct{ X, Y int }
	Logical  struct{ Width, Height int } `json:"logical_size"`
}

// Detect picks the backend: the -compositor flag if given, else the one the
// session runs (Hyprland, Sway, KDE Plasma), else umbriel when it is installed.
func Detect(flag string) (Compositor, error) {
	switch flag {
	case "umbriel":
		return umbriel{}, nil
	case "hyprland":
		return &hyprland{}, nil
	case "sway":
		return &sway{}, nil
	case "kde":
		return &kde{}, nil
	case "":
	default:
		return nil, fmt.Errorf("bad -compositor %q (umbriel|hyprland|sway|kde)", flag)
	}
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		return &hyprland{}, nil
	case os.Getenv("SWAYSOCK") != "":
		return &sway{}, nil
	case os.Getenv("KDE_FULL_SESSION") == "true", strings.Contains(os.Getenv("XDG_CURRENT_DESKTOP"), "KDE"):
		return &kde{}, nil
	}
	if _, err := exec.LookPath("umbriel"); err == nil {
		return umbriel{}, nil
	}
	return nil, fmt.Errorf("no supported compositor found (umbriel, Hyprland, Sway or KDE Plasma); use -compositor to force one")
}

// placeXY returns where a w x h output goes when placed pos (right|left|above|below)
// of the bounding box of the other enabled outputs. ok is false when there are none.
func placeXY(outs []Output, skip, pos string, w, h int) (x, y int, ok bool, err error) {
	minX, minY, maxX, maxY := 1<<30, 1<<30, -1<<30, -1<<30
	for _, o := range outs {
		if o.Name == skip || !o.Enabled {
			continue
		}
		minX, minY = min(minX, o.Position.X), min(minY, o.Position.Y)
		maxX, maxY = max(maxX, o.Position.X+o.Logical.Width), max(maxY, o.Position.Y+o.Logical.Height)
	}
	switch pos {
	case "right":
		x, y = maxX, minY
	case "left":
		x, y = minX-w, minY
	case "above":
		x, y = minX, minY-h
	case "below":
		x, y = minX, maxY
	default:
		return 0, 0, false, fmt.Errorf("bad -pos %q (right|left|above|below)", pos)
	}
	return x, y, maxX >= minX, nil
}
