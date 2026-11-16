# smon

**A wireless-first second monitor for Wayland compositors.**

smon adds a virtual monitor to your Wayland desktop and streams it to the screen you
already have nearby: a tablet, a phone, a laptop, a TV. Open a URL in a browser, or cast to
a Miracast, AirPlay or webOS receiver, and that device becomes a real extra display.
You can drag windows onto it, play sound on it, and with `-input`, control it by touch and pen.

[![License: GPL-3.0-or-later](https://img.shields.io/badge/license-GPL--3.0--or--later-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27-00ADD8.svg?logo=go&logoColor=white)](go.mod)
![Wayland](https://img.shields.io/badge/wayland-umbriel%20%7C%20Hyprland%20%7C%20Sway%20%7C%20KDE%20%7C%20GNOME-informational)

[Features](#features) ·
[Supported platforms](#supported-platforms) ·
[Quick start](#quick-start) ·
[Usage](#usage) ·
[Documentation](#documentation) ·
[Contributing](#contributing) ·
[License](#license)

---

## Features

- **Extend or mirror.** `extend` creates a new virtual output that behaves like any
  other monitor. `mirror` duplicates a screen you already have.
- **Any browser is a receiver.** Low-latency video over WebRTC to any device on your LAN, with
  no app to install: scan the QR code smon prints. The page is protected by an access token and
  reconnects on its own; with `-tls` it is served over HTTPS and can be installed as a
  fullscreen app.
- **Several monitors.** With `-monitors 3`, every tablet, phone or laptop that connects gets a
  monitor of its own, created on demand and removed when it leaves.
- **High-DPI and portrait.** `-scale 2` keeps text readable on a sharp tablet, and the monitor
  turns between landscape and portrait with the tablet or phone showing it (umbriel; Hyprland
  and Sway untested). `-portrait` starts it upright.
- **Native casting.** Miracast over Wi-Fi Direct, open AirPlay receivers (UxPlay, RPiPlay), and
  LG webOS TVs.
- **USB fallback.** No Wi-Fi? `smon usb` streams to an Android tablet over `adb`.
- **Audio.** A *Second Monitor (smon)* sound output appears while smon runs. Send any app to it
  and it plays on the receiver.
- **Touch, pen and keyboard.** With `-input`, touches drive the shared screen as a real
  touchscreen, and a stylus as a pen tablet with pressure, tilt and eraser (draw in Krita on
  your iPad). With `-keyboard`, type from the tablet's keyboard.
- **Fast by default.** smon benchmarks every encoder on your machine (VA-API on each GPU, and
  software) and picks the fastest. WebRTC viewers get adaptive bitrate and keyframes on demand.
  `-codec h265` or `av1` halves the bandwidth for the same quality. A stats overlay shows what
  the stream is doing.
- **Leaves nothing behind.** Outputs, layout overrides, input mappings and the sound device are
  all removed when smon exits. Your config files are never edited.

## Supported platforms

### Compositors

| Compositor | Status | Notes |
| --- | --- | --- |
| [umbriel](docs/compositors.md#umbriel) | Supported | Needs `xdg-desktop-portal-umbriel` |
| [Hyprland](docs/compositors.md#hyprland) | Supported | 0.42 or newer, needs `xdg-desktop-portal-hyprland` |
| [Sway](docs/compositors.md#sway) | Supported, untested on hardware | 1.8 or newer, needs `xdg-desktop-portal-wlr` (0.9+ recommended) |
| [KDE Plasma](docs/compositors.md#kde-plasma) | Supported, untested on hardware | Plasma 6, needs `xdg-desktop-portal-kde`; 6.8+ to choose the size |
| [GNOME](docs/compositors.md#gnome) | Supported, untested on hardware | Needs `xdg-desktop-portal-gnome` |
| Others | Planned | See [Adding a compositor](docs/architecture.md#adding-a-compositor) |

smon picks the compositor automatically. Use `-compositor umbriel|hyprland|sway|kde|gnome` to override.

### Receivers

| Receiver | Command | Transport | Codecs | Audio | Touch, pen, keyboard |
| --- | --- | --- | --- | --- | --- |
| Any browser on the LAN | `smon web` | WebRTC (HTTPS with `-tls`) | H.264, H.265, AV1 | Opus | Yes |
| Android over USB | `smon usb` | WebSocket over `adb reverse` | H.264, H.265, AV1 | Opus | Yes |
| Miracast sinks | `smon cast miracast:…` | Wi-Fi Direct, MPEG-TS/RTP | H.264 | AAC | No |
| Open AirPlay receivers | `smon cast airplay:…` | AirPlay mirroring | H.264 | No | No |
| LG webOS TVs | `smon cast webos:…` | TV browser on the WebRTC page | H.264 | Opus | No |

## Quick start

On Arch Linux and derivatives, install the core dependencies:

```sh
sudo pacman -S --needed go gcc pkgconf pipewire wireplumber pipewire-pulse libpulse \
    gstreamer gst-plugins-base gst-plugins-good gst-plugins-bad-libs \
    gst-plugin-pipewire gst-plugin-va gst-plugins-ugly gst-libav
```

Add the screencast portal for your compositor (`xdg-desktop-portal-umbriel`,
`xdg-desktop-portal-hyprland`, `xdg-desktop-portal-wlr` for Sway or `xdg-desktop-portal-kde`). Other distributions and optional extras are covered in
[Installation](docs/installation.md).

Then build and run it:

```sh
git clone <repository-url> smon
cd smon
make build
./bin/smon web
```

smon prints a URL with an access token, and a QR code for it. Scan it or open the URL on the
other device. The first run shows
your compositor's screen-share picker: pick **SMON-1** (on Sway, the new `HEADLESS-<n>`
output; on KDE and GNOME, allow the new virtual display). smon saves that choice, so later
runs skip the picker.

> **Note:** the web viewer uses UDP and TCP on the `-addr` port (default `8080`). If a firewall is
> running, open that port. See [Firewall](docs/receivers.md#firewall).

## Usage

```text
smon web  [flags]           stream to a browser (WebRTC)
smon usb  [flags]           stream to an Android tablet over USB (adb)
smon list                   discover Miracast and AirPlay receivers
smon cast [flags] <target>  cast to miracast:<name|mac>, airplay:<name|host:port> or webos:<tv-ip>
```

| Flag | Default | Description |
| --- | --- | --- |
| `-mode extend\|mirror` | `extend` | New virtual monitor, or duplicate an existing screen |
| `-w`, `-h` | `1920`, `1080` | Output size (`-h` is height, not help) |
| `-fps` | `30` | Frame rate |
| `-kbps` | `8000` | Video bitrate (the ceiling when adaptive bitrate is on) |
| `-pos right\|left\|above\|below` | layout | Initial placement of the virtual output |
| `-addr` | `:8080` | Listen address, TCP and UDP (`usb` uses only the port) |
| `-scale` | `1` | Output scale, for example `2` on a high-DPI tablet |
| `-portrait` | off | Upright monitor (swaps `-w` and `-h`) |
| `-rotate` | on | `web` and `usb`: the monitor turns with the device showing it |
| `-monitors` | `1` | `web` only: up to this many monitors, one per device that connects |
| `-codec h264\|h265\|av1` | `h264` | Video codec; H.265 and AV1 need less bandwidth |
| `-input` | off | `web` and `usb` only: viewer touches and pen control the shared screen |
| `-keyboard` | off | `web` and `usb` only: viewers can type into the desktop |
| `-tls` | off | `web`: serve the viewer over HTTPS (install smon's certificate on the device first) |
| `-new-token` | off | Replace the saved access token (old links stop working) |
| `-audio` | `true` | Add the *Second Monitor (smon)* sound output |
| `-compositor umbriel\|hyprland\|sway\|kde\|gnome` | detected | Force a compositor backend |

Some examples:

```sh
smon web -input                       # tablet as a touchscreen (and pen) second monitor
smon web -input -keyboard -scale 2    # high-DPI tablet with a keyboard cover
smon web -monitors 3                  # a monitor for each device that connects
smon web -codec h265                  # less bandwidth on a busy Wi-Fi
smon web -mode mirror                 # duplicate a screen in the browser
smon usb -input                       # Android tablet over USB, with touch
smon list                             # find Miracast and AirPlay receivers
smon cast miracast:"Living Room TV"   # cast to a Miracast sink
smon cast webos:192.168.1.50          # LG TV: press Allow on the TV the first time
smon web -w 2560 -h 1600 -fps 60 -pos left
```

All commands, environment variables and files are in the [Usage guide](docs/usage.md).

## Documentation

| Guide | Contents |
| --- | --- |
| [Installation](docs/installation.md) | Requirements, packages, building, `/dev/uinput` access |
| [Usage](docs/usage.md) | Commands, flags, extend and mirror modes, environment variables, state files |
| [Receivers](docs/receivers.md) | Browser, HTTPS and app install, USB, Miracast, AirPlay and webOS setup; firewall rules |
| [Compositors](docs/compositors.md) | How smon behaves on umbriel, Hyprland, Sway, KDE Plasma and GNOME |
| [Audio](docs/audio.md) | The *Second Monitor (smon)* sound output |
| [Touch, pen and keyboard](docs/touch-input.md) | Using the tablet as a touchscreen, pen tablet and keyboard, and security notes |
| [Performance and limitations](docs/limitations.md) | Encoder choice, bitrate adaptation, known limits |
| [Troubleshooting](docs/troubleshooting.md) | Common problems and how to fix them |
| [Architecture](docs/architecture.md) | Code layout, data flow, adding a compositor backend or a codec |

## Roadmap

- [x] umbriel
- [x] Hyprland
- [x] Sway
- [x] KDE Plasma 6
- [x] GNOME
- [ ] More Wayland compositors (niri, river, Wayfire, …)

Want smon on your compositor? Each backend is a small Go type. See
[Adding a compositor](docs/architecture.md#adding-a-compositor).

## Contributing

Contributions are welcome: bug reports, compositor backends, receiver support, docs.
Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.

```sh
make            # fmt-check, vet, lint, short tests, then build
make test       # full test suite, including the real-encoder tests (needs GStreamer)
```

## License

smon is free software, released under the [GNU General Public License v3.0 or later](LICENSE).

`internal/cast/playfair/` is vendored from [UxPlay](https://github.com/FDH2/UxPlay) (GPL-3.0).
