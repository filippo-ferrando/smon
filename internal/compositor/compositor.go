// Package compositor creates and places smon's virtual output, and maps its
// touchscreen, on the supported Wayland compositors.
package compositor

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"smon/internal/touch"
)

// OutputName is the virtual output smon creates in extend mode where the
// compositor lets it pick the name (umbriel, Hyprland, labwc).
const OutputName = "SMON-1"

// MonitorName is the name of smon's monitor n (1-based) where smon picks it:
// SMON-1, SMON-2, ...
func MonitorName(n int) string { return fmt.Sprintf("SMON-%d", max(n, 1)) }

// OutputSpec is the virtual output smon asks for: W x H pixels at FPS,
// placed Pos (""; or right|left|above|below) and scaled by Scale (0 or 1: not
// scaled; 2 makes everything on it twice as big, for a high-DPI tablet).
type OutputSpec struct {
	Name      string // where smon names outputs (umbriel, Hyprland, labwc); "" is OutputName
	W, H, FPS int
	Pos       string
	Scale     float64
}

// name is the output name smon asks for.
func (s OutputSpec) name() string {
	if s.Name == "" {
		return OutputName
	}
	return s.Name
}

// scale is the output scale, 1 when unset.
func (s OutputSpec) scale() float64 {
	if s.Scale <= 0 {
		return 1
	}
	return s.Scale
}

// Scaled reports whether the output asks for a scale other than 1.
func (s OutputSpec) Scaled() bool { return s.scale() != 1 }

// Logical is the output's size in layout coordinates: its pixels divided by
// its scale.
func (s OutputSpec) Logical() (w, h int) {
	sc := s.scale()
	return int(float64(s.W)/sc + 0.5), int(float64(s.H)/sc + 0.5)
}

// scaleArg formats the scale the way the compositors' commands take it.
func (s OutputSpec) scaleArg() string { return strconv.FormatFloat(s.scale(), 'f', -1, 64) }

// Compositor is everything smon needs from the compositor beyond the
// xdg-desktop-portal ScreenCast interface, which is the same on all of them.
type Compositor interface {
	Name() string
	// CreateOutput adds the virtual output (removing a stale one from a crash
	// first), places and scales it, and returns its name. It returns "" when
	// the ScreenCast portal creates the output instead (see PortalOutput).
	CreateOutput(spec OutputSpec) (string, error)
	DestroyOutput(name string)
	// WatchLayout runs until done is closed; a no-op where it does not apply.
	WatchLayout(name string, done chan struct{})
	// MapInput maps one of smon's virtual input devices (touchscreen or pen),
	// which already exists, to an output; UnmapInput undoes it after the
	// device is gone.
	MapInput(dev touch.Device, output string) error
	UnmapInput(dev touch.Device)
	Outputs() ([]Output, error)
}

// PortalOutput is implemented by compositors whose virtual output is created
// by the ScreenCast portal (source type VIRTUAL) rather than by CreateOutput,
// and removed when the portal session closes.
type PortalOutput interface {
	// PlaceOutput positions and scales the portal-made output once its name
	// is known; spec has its actual size.
	PlaceOutput(name string, spec OutputSpec) error
}

// ResizableOutput is implemented by compositors that can change the size of
// an output CreateOutput made while it is being shared, so it can turn
// between landscape and portrait with the device showing it. The capture
// follows the new size.
type ResizableOutput interface {
	// ResizeOutput gives output name the mode spec.W x spec.H at spec.FPS,
	// and places it again by spec.Pos (its logical size changed).
	ResizeOutput(name string, spec OutputSpec) error
}

// CapsSizedOutput is implemented by portal-made outputs that take their size
// from the video format the consumer negotiates (Mutter's virtual monitors):
// smon then asks for -w x -h in the capture caps.
type CapsSizedOutput interface {
	CapsSized() bool
}

// Output is one monitor and where it sits in the layout, in logical pixels.
type Output struct {
	Name     string
	Enabled  bool
	Position struct{ X, Y int }
	Logical  struct{ Width, Height int } `json:"logical_size"`
}

// Detect picks the backend: the -compositor flag if given, else the one the
// session runs (Hyprland, Sway, labwc, KDE Plasma, GNOME), else umbriel when it is installed.
func Detect(flag string) (Compositor, error) {
	switch flag {
	case "umbriel":
		return umbriel{}, nil
	case "hyprland":
		return &hyprland{}, nil
	case "sway":
		return &sway{}, nil
	case "labwc":
		return &labwc{}, nil
	case "kde":
		return &kde{}, nil
	case "gnome":
		return &gnome{}, nil
	case "":
	default:
		return nil, fmt.Errorf("bad -compositor %q (umbriel|hyprland|sway|labwc|kde|gnome)", flag)
	}
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		return &hyprland{}, nil
	case os.Getenv("SWAYSOCK") != "":
		return &sway{}, nil
	case os.Getenv("LABWC_PID") != "":
		return &labwc{}, nil
	case os.Getenv("KDE_FULL_SESSION") == "true", strings.Contains(os.Getenv("XDG_CURRENT_DESKTOP"), "KDE"):
		return &kde{}, nil
	case strings.Contains(os.Getenv("XDG_CURRENT_DESKTOP"), "GNOME"):
		return &gnome{}, nil
	}
	if _, err := exec.LookPath("umbriel"); err == nil {
		return umbriel{}, nil
	}
	return nil, fmt.Errorf("no supported compositor found (umbriel, Hyprland, Sway, labwc, KDE Plasma or GNOME); use -compositor to force one")
}

// placeXY returns where a w x h output goes when placed pos (right|left|above|below)
// of the bounding box of the other enabled outputs. ok is false when there are none.
// overlaps reports whether output name shares any area with another enabled output.
func overlaps(outs []Output, name string) bool {
	for _, a := range outs {
		if a.Name != name {
			continue
		}
		for _, b := range outs {
			if b.Name != name && b.Enabled &&
				a.Position.X < b.Position.X+b.Logical.Width && b.Position.X < a.Position.X+a.Logical.Width &&
				a.Position.Y < b.Position.Y+b.Logical.Height && b.Position.Y < a.Position.Y+a.Logical.Height {
				return true
			}
		}
	}
	return false
}

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
