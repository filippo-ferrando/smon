# Architecture

smon is a single Go binary. The compositor-specific parts are small, and everything after
capture (encoding, streaming, audio, input) is shared by every backend.

- [Data flow](#data-flow)
- [Code layout](#code-layout)
- [Adding a compositor](#adding-a-compositor)

## Data flow

```text
                ┌──────────────────────────┐
                │  Wayland compositor      │  internal/compositor
                │  (umbriel, Hyprland, …)  │  creates the output, places it, maps touch
                └────────────┬─────────────┘
                             │ xdg-desktop-portal ScreenCast → PipeWire
                ┌────────────▼─────────────┐
                │  pipewiresrc → H.264     │  internal/display, internal/gst
                │  (fastest encoder)       │  keyframes and bitrate on demand
                └────────────┬─────────────┘
         ┌───────────────────┼──────────────────────┬─────────────────┐
         ▼                   ▼                      ▼                 ▼
   WebRTC viewers      WebSocket (USB)       Miracast (WFD)       AirPlay
   web, webOS          adb reverse           RTSP + MPEG-TS/RTP   mirroring
   internal/web        internal/web          internal/cast        internal/cast

   "Second Monitor (smon)" sink → pulsesrc → Opus / AAC        internal/audio
   viewer touches → /dev/uinput touchscreen                    internal/touch
```

1. In extend mode the compositor backend creates the virtual output **SMON-1**.
2. smon opens a ScreenCast session through xdg-desktop-portal and receives a PipeWire stream
   of that output (or of the mirrored screen).
3. An in-process GStreamer pipeline encodes it to H.264 with the fastest available encoder.
4. The access units fan out to every connected viewer through the transport for its receiver.
5. Audio is captured from smon's own sound output and encoded to Opus (browser, USB) or AAC
   (Miracast).
6. With `-input`, viewer touch events are injected into a virtual uinput touchscreen that the
   compositor maps to the shared output.

On exit, everything is undone in reverse order.

## Code layout

| Path | What it holds |
| --- | --- |
| `cmd/smon` | Command line: flag parsing and wiring |
| `internal/compositor` | Virtual output, placement and touch mapping on umbriel, Hyprland, Sway and KDE Plasma |
| `internal/display` | Portal screencast of SMON-1 or a mirrored screen, and its encoder pipeline |
| `internal/gst` | In-process GStreamer pipelines, encoder selection, keyframe and bitrate control |
| `internal/h264` | Annex-B access units from RTP |
| `internal/web` | WebRTC and WebSocket viewers, the viewer page, adaptive bitrate, `smon usb` |
| `internal/cast` | Miracast (WFD RTSP), AirPlay, webOS, receiver discovery |
| `internal/cast/playfair` | FairPlay handshake for AirPlay, vendored from UxPlay (C, via cgo) |
| `internal/audio` | The *Second Monitor (smon)* sound output and its capture |
| `internal/touch` | Virtual uinput touchscreen |
| `internal/config` | Per-user state directory |

## Adding a compositor

Supporting a new Wayland compositor means implementing one interface in
`internal/compositor`. Capture already goes through the standard xdg-desktop-portal
ScreenCast interface, so the compositor needs:

- an xdg-desktop-portal backend that implements ScreenCast with restore tokens, and ideally
  reports the shared output's name in the stream's `mapping_id` property (smon uses it to
  check that extend mode captures the virtual output, to name the mirrored screen, and to map
  touch; without it, smon matches the stream's `position` against `Outputs`; `source_type` and
  `size` are read too),
- a way to create and destroy a virtual (headless) output at runtime, or a portal that creates
  one itself (ScreenCast source type `VIRTUAL`, as on KDE Plasma; see `PortalOutput` below),
- ideally, a way to position outputs and to map a touchscreen to an output at runtime.

### The interface

```go
// internal/compositor/compositor.go
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
	PlaceOutput(name, pos string, w, h int) error
}
```

| Method | Called | Expected behavior |
| --- | --- | --- |
| `Name` | At startup | Lowercase backend name, as used by `-compositor`. |
| `CreateOutput` | Extend mode, before the screencast starts | Remove a stale output from a crashed run, create a `w`x`h`@`fps` output, place it, and return its name (`SMON-1` where the compositor lets smon choose). With `pos == ""`, use the compositor's default placement. Backends implementing `PortalOutput` return `""`. |
| `DestroyOutput` | On exit (extend mode) | Remove the output and any placement override. Must not fail loudly. |
| `WatchLayout` | In a goroutine, extend mode | Optional. React to the user rearranging displays (umbriel uses it to drop its `-pos` override). Return when `done` closes. |
| `MapTouch` | With `-input`, right after the touchscreen is created | Map smon's uinput device, named `smon touchscreen` (Hyprland calls it `smon-touchscreen`), (or all touchscreens, if that is all the compositor offers) to `output`. |
| `UnmapTouch` | On exit | Restore the previous mapping. |
| `Outputs` | During placement, and to name the captured output when the portal sends no `mapping_id` | Outputs with position and **logical** size (after scale and rotation). |
| `PlaceOutput` (`PortalOutput` only) | Once the portal reports the output it created | Place it according to `pos`; a no-op for `""`. |

The `placeXY` helper turns a `-pos` value and the result of `Outputs` into coordinates, so most
backends only need to call it from `CreateOutput`.

### Steps

1. Add `internal/compositor/<name>.go` with a type implementing `Compositor`. Use
   `umbriel.go` (a CLI with runtime config keys), `hyprland.go` or `sway.go` (an IPC tool
   with JSON output), or `kde.go` (portal-made output, D-Bus touch mapping) as a template.
2. Register it in `Detect` in `compositor.go`: accept it in the `-compositor` switch, and add
   automatic detection, preferably from an environment variable the compositor sets for its
   clients (like `HYPRLAND_INSTANCE_SIGNATURE`).
3. Update the `-compositor` flag help in `cmd/smon/options.go`, and the error message in
   `Detect`.
4. If the compositor's share picker needs a hint (Hyprland's needs **Allow a restore token**),
   extend the `comp.Name()` switch in `display.New` (`internal/display/display.go`).
5. Add unit tests for any parsing of the compositor's output (see `hyprland_test.go`).
6. Document it: add a section to [compositors.md](compositors.md), a row to the tables in the
   [README](../README.md#supported-platforms), and its packages to
   [installation.md](installation.md).

### Guidelines

- **Runtime only.** Never write to the user's config files. Every change must be undone on exit.
- **Clean up after crashes.** `CreateOutput` must cope with a leftover output from a previous
  run that was killed (Sway, whose names change, keeps the last one in `~/.config/smon/sway-output`).
- **Logical coordinates.** `Outputs` reports sizes after scale and transform, since that is the
  space placement works in.
- **Least surprise for touch.** Map only smon's device when the compositor allows it, so built-in
  touch panels keep working.
