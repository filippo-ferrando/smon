package main

import (
	"fmt"
	"os"
	"os/exec"
)

// compositor is everything smon needs from the compositor beyond the
// xdg-desktop-portal ScreenCast interface, which is the same on all of them.
type compositor interface {
	name() string
	// createOutput adds the virtual output (removing a stale one from a crash
	// first) and places it: pos is "" (compositor default) or right|left|above|below.
	createOutput(name string, w, h, fps int, pos string) error
	destroyOutput(name string)
	// watchLayout runs until done is closed; a no-op where it does not apply.
	watchLayout(done chan struct{})
	// mapTouch maps smon's virtual touchscreen to an output; unmapTouch undoes it.
	mapTouch(output string) error
	unmapTouch()
	outputs() ([]outputInfo, error)
}

type outputInfo struct {
	Name     string
	Enabled  bool
	Position struct{ X, Y int }
	Logical  struct{ Width, Height int } `json:"logical_size"`
}

// detectCompositor picks the backend: the -compositor flag if given, else
// Hyprland when running inside it, else umbriel when it is installed.
func detectCompositor(flag string) (compositor, error) {
	switch flag {
	case "umbriel":
		return umbriel{}, nil
	case "hyprland":
		return &hyprland{}, nil
	case "":
	default:
		return nil, fmt.Errorf("bad -compositor %q (umbriel|hyprland)", flag)
	}
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" {
		return &hyprland{}, nil
	}
	if _, err := exec.LookPath("umbriel"); err == nil {
		return umbriel{}, nil
	}
	return nil, fmt.Errorf("no supported compositor found (umbriel or Hyprland); use -compositor to force one")
}

// placeXY returns where a w x h output goes when placed pos (right|left|above|below)
// of the bounding box of the other enabled outputs. ok is false when there are none.
func placeXY(outs []outputInfo, skip, pos string, w, h int) (x, y int, ok bool, err error) {
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
