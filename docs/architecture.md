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
                │  (umbriel, Hyprland, …)  │  creates outputs, places and scales them, maps input
                └────────────┬─────────────┘
                             │ xdg-desktop-portal ScreenCast → PipeWire
                ┌────────────▼─────────────┐
                │  pipewiresrc → H.264,    │  internal/display, internal/gst
                │  H.265 or AV1 (fastest)  │  keyframes and bitrate on demand
                └────────────┬─────────────┘
         ┌───────────────────┼──────────────────────┬─────────────────┐
         ▼                   ▼                      ▼                 ▼
   WebRTC viewers      WebSocket (USB)       Miracast (WFD)       AirPlay
   web, webOS          adb reverse           RTSP + MPEG-TS/RTP   mirroring
   internal/web        internal/web          internal/cast        internal/cast

   "Second Monitor (smon)" sink → pulsesrc → Opus / AAC        internal/audio
   viewer touches, pen, keys → /dev/uinput devices             internal/touch
```

1. In extend mode the compositor backend creates the virtual output **SMON-1** (with
   `-monitors`, one more per viewer, on demand).
2. smon opens a ScreenCast session through xdg-desktop-portal and receives a PipeWire stream
   of that output (or of the mirrored screen).
3. An in-process GStreamer pipeline encodes it (H.264, H.265 or AV1, from `-codec`) with the
   fastest available encoder for that codec.
4. The access units fan out to every connected viewer through the transport for its receiver.
5. Audio is captured from smon's own sound output and encoded to Opus (browser, USB) or AAC
   (Miracast).
6. With `-input`, viewer touch and pen events are injected into a virtual uinput touchscreen
   and pen tablet that the compositor maps to the shared output; with `-keyboard`, key events
   into a virtual keyboard.

On exit, everything is undone in reverse order.

## Code layout

| Path | What it holds |
| --- | --- |
| `cmd/smon` | Command line: flag parsing and wiring |
| `internal/compositor` | Virtual output, placement, scale and input mapping on umbriel, Hyprland, Sway, KDE Plasma and GNOME |
| `internal/display` | Portal screencast of one monitor (SMON-n) or a mirrored screen, and its encoder pipeline |
| `internal/gst` | In-process GStreamer pipelines, the codec table, encoder selection, keyframe and bitrate control |
| `internal/rtpau` | Access units from RTP: H.264 and H.265 as Annex-B, AV1 as OBUs (for WebCodecs) |
| `internal/web` | WebRTC and WebSocket viewers, the viewer page (installable, with stats), the monitor allocator, adaptive bitrate, HTTPS, QR code, `smon usb` |
| `internal/cast` | Miracast (WFD RTSP), AirPlay, webOS, receiver discovery |
| `internal/cast/playfair` | FairPlay handshake for AirPlay, vendored from UxPlay (C, via cgo) |
| `internal/audio` | The *Second Monitor (smon)* sound output and its capture |
| `internal/touch` | Virtual uinput touchscreen, pen tablet and keyboard |
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
  one itself (ScreenCast source type `VIRTUAL`, as on KDE Plasma and GNOME; see `PortalOutput`
  below),
- ideally, a way to position and scale outputs, and to map a touchscreen or pen tablet to an
  output, at runtime.

### The interface

```go
// internal/compositor/compositor.go
type OutputSpec struct {
	Name      string // where smon names outputs (umbriel, Hyprland); "" is OutputName
	W, H, FPS int
	Pos       string  // "", right, left, above or below
	Scale     float64 // 0 or 1: not scaled
}

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
	PlaceOutput(name string, spec OutputSpec) error
}

// CapsSizedOutput: the portal-made output takes the size smon asks for in the
// capture caps (Mutter), rather than one chosen in the share dialog.
type CapsSizedOutput interface {
	CapsSized() bool
}
```

| Method | Called | Expected behavior |
| --- | --- | --- |
| `Name` | At startup | Lowercase backend name, as used by `-compositor`. |
| `CreateOutput` | Extend mode, before the screencast starts, once per monitor | Remove a stale output from a crashed run (only outputs of a previous run: with `-monitors` several are live at once), create a `W`x`H`@`FPS` output named `spec.Name` where the compositor lets smon choose, scale it, place it, and return its name. With `Pos == ""`, use the compositor's default placement. Backends implementing `PortalOutput` return `""`. |
| `DestroyOutput` | When the monitor closes (extend mode) | Remove the output and any placement or scale override. Must not fail loudly. |
| `WatchLayout` | In a goroutine, extend mode | Optional. React to the user rearranging displays (umbriel uses it to drop its `-pos` override). Return when `done` closes. |
| `MapInput` | With `-input`, right after each device is created | Map smon's uinput device `dev` to `output`. `dev` has the name, vendor and product ids (`smon touchscreen`, `smon pen`; ` <n>` after the name and their own product id for monitor n), and says whether it is a pen. `Sway()` and `Hyprland()` spell the name the way those compositors do. Map all touchscreens or tablets only if that is all the compositor offers. |
| `UnmapInput` | When the monitor closes | Restore the previous mapping. |
| `Outputs` | During placement, and to name the captured output when the portal sends no `mapping_id` | Outputs with position and **logical** size (after scale and rotation). |
| `PlaceOutput` (`PortalOutput` only) | Once the portal reports the output it created | Scale it, and place it according to `spec.Pos`. `spec` has the size the portal made. |

The `placeXY` helper turns a `-pos` value and the result of `Outputs` into coordinates, so most
backends only need to call it from `CreateOutput`.

### Steps

1. Add `internal/compositor/<name>.go` with a type implementing `Compositor`. Use
   `umbriel.go` (a CLI with runtime config keys), `hyprland.go` or `sway.go` (an IPC tool
   with JSON output), or `kde.go` / `gnome.go` (portal-made output, D-Bus placement and input
   mapping) as a template.
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
  run that was killed (Sway, whose names change, lists them in `~/.config/smon/sway-output`).
- **Logical coordinates.** `Outputs` reports sizes after scale and transform, since that is the
  space placement works in.
- **Least surprise for touch.** Map only smon's device when the compositor allows it, so built-in
  touch panels keep working.
- **Several monitors.** Key every runtime override by output name, never assume one output.

## Adding a codec

Codecs are rows of the table in `internal/gst/codec.go`: VA-API encoder suffix, software
encoders, caps, parser and RTP payloader. `encoderChain` in `internal/gst/encoder.go` needs the
software encoder's low-latency settings, and `bitrateProps`/`LiveBitrate` say how adaptive
bitrate drives it. The web side needs the WebRTC MIME type and SDP name in
`internal/web/codec.go`, a depacketizer in `internal/rtpau` for USB viewers, and the WebCodecs
codec string in the viewer page's `codecFor`.

## Several monitors

`internal/web/alloc.go` decides which monitor a viewer gets. The page sends `POST /claim` with
a client id it keeps in `localStorage`, the monitor it asks for (`?m=`) and its orientation;
the allocator answers with the one the client already holds, the one it asked for, a free one,
or a new one (made through `Options.NewMonitor`, which `cmd/smon` builds on `display.New`), up
to `-monitors`. The page then sends its WebRTC offer for that monitor. Connections are counted
per client; monitors after the first close once nobody has held them for 30 seconds.
