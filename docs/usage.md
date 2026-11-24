# Usage

- [Commands](#commands)
- [Flags](#flags)
- [Extend and mirror](#extend-and-mirror)
- [The share picker](#the-share-picker)
- [Placement](#placement)
- [Scale and portrait](#scale-and-portrait)
- [Several monitors](#several-monitors)
- [Codecs](#codecs)
- [Environment variables](#environment-variables)
- [Files](#files)

## Commands

```text
smon web  [flags]           stream to a browser (WebRTC)
smon usb  [flags]           stream to an Android tablet over USB (adb)
smon list                   discover Miracast and AirPlay receivers
smon cast [flags] <target>  cast to a receiver from 'smon list', or webos:<tv-ip>
```

| Command | What it does |
| --- | --- |
| `smon web` | Serves the viewer page and prints a URL with an access token for each network interface, the first one also as a QR code. Open it in any browser on the LAN. |
| `smon usb` | Sets up `adb reverse` and opens the viewer page in the tablet's default browser. See [USB](receivers.md#usb-android). |
| `smon list` | Lists AirPlay receivers (mDNS) and Miracast sinks (Wi-Fi P2P scan). |
| `smon cast miracast:<name\|mac>` | Casts to a Miracast sink. See [Miracast](receivers.md#miracast). |
| `smon cast airplay:<name\|host:port>` | Casts to an open AirPlay receiver. See [AirPlay](receivers.md#airplay). |
| `smon cast webos:<tv-ip>` | Opens the viewer page in an LG TV's browser. See [webOS](receivers.md#lg-webos). |

Stop smon with <kbd>Ctrl</kbd>+<kbd>C</kbd> (or `SIGTERM`). It removes the virtual output and
undoes every runtime change it made.

## Flags

Flags go after the command, for example `smon web -input -fps 60`.

| Flag | Default | Applies to | Description |
| --- | --- | --- | --- |
| `-mode` | `extend` | all but `list` | `extend`: new virtual monitor. `mirror`: duplicate an existing screen. |
| `-w` | `1920` | all but `list` | Width of the virtual output. |
| `-h` | `1080` | all but `list` | Height of the virtual output. This is **not** a help flag. |
| `-fps` | `30` | all but `list` | Frame rate. |
| `-kbps` | `8000` | all but `list` | Video bitrate. With WebRTC viewers this is the ceiling for [adaptive bitrate](limitations.md#adaptive-bitrate). |
| `-pos` | see [Placement](#placement) | extend | Initial placement: `right`, `left`, `above` or `below`. |
| `-addr` | `:8080` | `web`, `usb`, `cast webos:` | Listen address, TCP and UDP. `usb` uses only the port and always listens on 127.0.0.1. |
| `-scale` | `1` | extend | Output scale: `2` makes everything twice as big, for a high-DPI tablet. See [Scale and portrait](#scale-and-portrait). |
| `-portrait` | `false` | extend | Portrait monitor: swaps `-w` and `-h`. |
| `-rotate` | `true` | `web`, `usb`, extend | The monitor turns between landscape and portrait with the device showing it. See [Scale and portrait](#scale-and-portrait). |
| `-monitors` | `1` | `web`, extend | Up to this many monitors, one per device that connects. See [Several monitors](#several-monitors). |
| `-codec` | `h264` | all but `list` | `h264`, `h265` or `av1`. See [Codecs](#codecs). |
| `-input` | `false` | `web`, `usb` | Viewer touches and pen strokes control the shared screen. See [Touch, pen and keyboard](touch-input.md). |
| `-keyboard` | `false` | `web`, `usb` | Viewers can type into the desktop. See [Touch, pen and keyboard](touch-input.md#keyboard). |
| `-tls` | `false` | `web` | Serve the viewer over HTTPS with smon's own certificate, for app install and keeping the screen awake. Install the certificate on the device first: see [HTTPS](receivers.md#https-and-the-certificate). |
| `-qr` | `true` | `web` | Also print the viewer URL as a QR code (only on a terminal). |
| `-new-token` | `false` | `web`, `usb` | Replace the saved access token, so links handed out before stop working. |
| `-audio` | `true` | all but `list` | Add the *Second Monitor (smon)* sound output. `-audio=false` streams video only. See [Audio](audio.md). |
| `-compositor` | detected | all but `list` | `umbriel`, `hyprland`, `sway`, `labwc`, `kde` or `gnome`. |

In mirror mode the stream has the size of the screen you pick, whatever `-w` and `-h` say.

### Compositor detection

Without `-compositor`, smon uses, in order: Hyprland when `$HYPRLAND_INSTANCE_SIGNATURE` is set,
Sway when `$SWAYSOCK` is set, labwc when `$LABWC_PID` is set, KDE Plasma when `$KDE_FULL_SESSION=true` or `$XDG_CURRENT_DESKTOP`
contains `KDE`, GNOME when `$XDG_CURRENT_DESKTOP` contains `GNOME`, and umbriel when the
`umbriel` command is on `PATH`. If none applies it exits
with an error.

## Extend and mirror

- **`-mode extend`** (default) creates a virtual output and streams it. It is called **SMON-1**
  on umbriel, Hyprland and labwc, `HEADLESS-<n>` on Sway, `Virtual-…` on KDE Plasma and `Meta-<n>` on
  GNOME. The
  receiver becomes a second monitor: move windows to it, arrange it in your display settings.
  smon removes the output when it exits.
- **`-mode mirror`** creates nothing and streams a screen you choose. The receiver duplicates
  it, at that screen's size.

## The share picker

smon captures through the xdg-desktop-portal ScreenCast interface, so the first run of each
mode opens your compositor's share picker:

- In **extend** mode, pick **SMON-1** (Sway: the new `HEADLESS-<n>`; KDE: allow the new virtual
  display; GNOME: choose the virtual monitor).
- In **mirror** mode, pick the screen to duplicate.

The choice is saved as a restore token in `~/.config/smon/restore_token-<mode>` (with
`-<n>` after it for [monitor n](#several-monitors)), so later runs don't ask. On Hyprland, tick **Allow a restore token** in the picker, or it asks every time.
On Sway the choice is remembered only in mirror mode, and only with `XDPW_PERSIST_MODE` set. See
[Sway](compositors.md#share-picker-on-sway). On labwc it is remembered with `XDPW_PERSIST_MODE` set, in both modes.

If extend mode receives a screen other than the virtual output, smon forgets the saved choice and exits
with an error, so the next run asks again.

## Placement

Where the virtual output appears depends on the compositor:

- **umbriel**: SMON-1 follows your saved display layout, so move it with the displays plugin
  like any other monitor. `-pos` only sets an initial position. See
  [umbriel placement](compositors.md#placement-on-umbriel).
- **Hyprland**: SMON-1 goes `auto-right` unless you pass `-pos` or have a monitor rule for it.
  See [Hyprland placement](compositors.md#placement-on-hyprland).
- **Sway**, **labwc** and **KDE Plasma**: the compositor's own placement unless you pass `-pos`. Move it
  later with your usual display tool.

## Scale and portrait

A tablet's screen is often high-DPI: 2560x1600 on a 10" panel makes text tiny at scale 1.
`-scale 2` tells the compositor to draw everything on the virtual monitor twice as big, so it
behaves like a 1280x800 screen in the layout while staying sharp. Fractional scales such as
`1.5` work where the compositor allows them; GNOME snaps to the nearest scale it supports.

```sh
smon web -w 2560 -h 1600 -scale 2
```

`-portrait` makes the monitor upright by swapping `-w` and `-h`, for a tablet held vertically.
It is a real portrait mode, not a rotation, so touch and pen land where expected.

Turn the tablet or phone while it is connected and the monitor turns with it: smon swaps the
output's width and height, the desktop lays itself out again, and the picture comes back at
the new size a moment later, with the viewer still connected. The device's orientation counts
when it connects too, so a phone held upright makes monitor 1 portrait.

- It works on umbriel. Hyprland, Sway and labwc can resize smon's output while it runs too, so it
  should work there, but it is untested on hardware. On KDE Plasma and GNOME the monitor keeps
  the orientation it was made with, and smon logs that it can't turn it.
- With `-pos`, the monitor is placed again at its new size, unless you have rearranged the
  displays since (umbriel); without it, it stays where it is.
- A monitor that several devices show at once stays as it is, so they don't fight over it.
- `-rotate=false` keeps every monitor the way it started.

## Several monitors

With `-monitors N`, each device that opens the URL gets a monitor of its own, created when it
connects, up to N:

```sh
smon web -monitors 3 -input
```

- The first device gets SMON-1, made when smon starts. The next gets SMON-2, and so on (on
  Sway and KDE, the compositor names them).
- A new monitor follows the device's orientation: a phone held upright gets a portrait monitor,
  and it [turns](#scale-and-portrait) when the phone does.
- The browser keeps an id, so a device that reloads or reconnects within 30 seconds gets its
  own monitor back. A monitor other than the first closes 30 seconds after its device leaves.
- When all N are in use, another device gets an error. Add `&m=1` to the URL to watch
  monitor 1 instead (any number works, for a monitor that exists).
- The share dialog may show on the desktop when a monitor is created, the first time for each
  monitor number; the viewer waits for it. Each number has its own restore token.
- Sound plays on every device. The keyboard (`-keyboard`) types into the focused window,
  whichever device it comes from. On umbriel, touch and pen work on SMON-1 only: umbriel maps
  every touchscreen to one output.
- Without `-monitors`, every device shows the same monitor, as before.

`-monitors` works with `smon web` in extend mode only.

## Codecs

`-codec` picks the video codec. H.265 and AV1 give the same picture quality for roughly half
the bandwidth of H.264, which helps on a busy Wi-Fi.

| Codec | Encoders smon tries | Notes |
| --- | --- | --- |
| `h264` (default) | VA-API `*h264enc` per GPU, `x264enc` | Plays everywhere. The only choice for Miracast and AirPlay. |
| `h265` | VA-API `*h265enc` per GPU, `x265enc` | Browser support varies, often only where the device decodes H.265 in hardware. |
| `av1` | VA-API `*av1enc` per GPU, `av1enc` (aom, real-time mode) | Recent Chromium-based browsers and Firefox. Software AV1 encoding costs a lot of CPU. Needs the `rtpav1pay` element from gst-plugins-rs (`gst-plugin-rsrtp` on Arch). aom can't change bitrate while running, so adaptive bitrate is off with it. |

If a browser can't receive the codec, the viewer page says so and suggests `-codec h264`. smon
times each codec's encoders separately, as for H.264 (see
[Encoder selection](limitations.md#encoder-selection)).

## Environment variables

| Variable | Effect |
| --- | --- |
| `SMON_ENCODER` | Use this GStreamer encoder and skip the benchmark, for example `x264enc` or `varenderD129h264enc`. It must match `-codec`. See [Encoder selection](limitations.md#encoder-selection). |
| `SMON_ABR=off` | Keep the bitrate fixed at `-kbps` instead of adapting to congestion. |
| `ANDROID_SERIAL` | With several Android devices attached, the one `smon usb` uses (standard `adb` variable). |
| `HYPRLAND_INSTANCE_SIGNATURE` | Set by Hyprland. Its presence selects the Hyprland backend. |
| `SWAYSOCK` | Set by Sway. Its presence selects the Sway backend. |
| `LABWC_PID` | Set by labwc. Its presence selects the labwc backend. |
| `KDE_FULL_SESSION`, `XDG_CURRENT_DESKTOP` | Set by Plasma and GNOME. They select the KDE or GNOME backend. |

## Files

smon keeps its state under `$XDG_CONFIG_HOME/smon` (usually `~/.config/smon`) and
`$XDG_CACHE_HOME/smon` (usually `~/.cache/smon`).

| Path | Contents | Safe to delete? |
| --- | --- | --- |
| `~/.config/smon/restore_token-extend`, `restore_token-mirror` | Saved share-picker choice per mode (`restore_token-extend-<n>` for monitor n) | Yes, the picker shows again on the next run |
| `~/.config/smon/token` | The viewer access token, kept so links and the installed app keep working | Yes, a new one is made; old links stop working (same as `-new-token`) |
| `~/.config/smon/tls/` | smon's certificate authority (`ca.pem`, `ca-key.pem`) and server certificate | Yes, but devices that trusted the old CA warn again |
| `~/.config/smon/sway-output` | Outputs smon made on Sway, for cleanup after a crash | Yes, when smon is not running |
| `~/.config/smon/webos-<ip>.key` | Pairing key for an LG TV | Yes, the TV asks to *Allow* again |
| `~/.cache/smon/encoder.json` | Fastest encoder per codec and output size | Yes, the benchmark runs again |

smon never edits your compositor's config files. All compositor changes are runtime-only and
undone on exit. On GNOME, the touch and pen mapping is a setting under smon's own device ids
(`org.gnome.desktop.peripherals`), reset on exit.
