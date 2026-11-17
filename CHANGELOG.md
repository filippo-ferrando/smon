# Changelog

Built from commit subjects by `scripts/changelog.sh`.

## v0.1.0 (2026-11-17)

### Features

- HTTPS is opt-in (-tls) (5056217)
- -monitors N: a monitor of its own for each device, on demand (d85a7d3)
- -codec h265|av1 for lower bandwidth (e98e07a)
- GNOME (Mutter) backend (0dcf524)
- -scale for high-DPI tablets and -portrait (9d662e0)
- pen tablet and keyboard input from the viewer (dce9b00)
- stream stats overlay in the viewer (5d2ffa8)
- installable viewer (manifest, service worker, icons) and backoff reconnect (403a05f)
- persistent token, HTTPS with a local CA, QR code for the viewer URL (eedfe72)
- kde and sway backed [WIP] (81be4ff)
- improved docs (fa42c9f)
- latest improvements (efb90a0)
- fixes on hyprland (1684c6d)
- added deps list (eb4b137)
- added deps (5ae3ed3)
- porting to hyprland (d3f0c7a)
- some new features (420177c)

### Fixes

- ci (d50d02b)
- keep the access token in a cookie; explain a missing token (02b18c1)
- show connection errors plainly; log why a viewer didn't connect (7bb6a4d)
- one monitor created at a time; GNOME setup once the monitor appears (3853de8)

### Other changes

- added actions (60750e0)
- x265 restarts the sequence on a bitrate change, like VA-API (c2fd8cd)
- HTTPS and app install, QR code, pen and keyboard, scale and portrait, several monitors, codecs, GNOME (6b5b46f)
- refactor (d3888b6)

### Uncategorized

- first push (806565f)

