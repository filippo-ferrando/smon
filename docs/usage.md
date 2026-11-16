# Usage

- [Commands](#commands)
- [Flags](#flags)
- [Extend and mirror](#extend-and-mirror)
- [The share picker](#the-share-picker)
- [Placement](#placement)
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
| `smon web` | Serves the viewer page and prints a URL with an access token for each network interface. Open it in any browser on the LAN. |
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
| `-input` | `false` | `web`, `usb` | Viewer touches control the shared screen. See [Touch input](touch-input.md). |
| `-audio` | `true` | all but `list` | Add the *Second Monitor (smon)* sound output. `-audio=false` streams video only. See [Audio](audio.md). |
| `-compositor` | detected | all but `list` | `umbriel`, `hyprland`, `sway` or `kde`. |

In mirror mode the stream has the size of the screen you pick, whatever `-w` and `-h` say.

### Compositor detection

Without `-compositor`, smon uses, in order: Hyprland when `$HYPRLAND_INSTANCE_SIGNATURE` is set,
Sway when `$SWAYSOCK` is set, KDE Plasma when `$KDE_FULL_SESSION=true` or `$XDG_CURRENT_DESKTOP`
contains `KDE`, and umbriel when the `umbriel` command is on `PATH`. If none applies it exits
with an error.

## Extend and mirror

- **`-mode extend`** (default) creates a virtual output and streams it. It is called **SMON-1**
  on umbriel and Hyprland, `HEADLESS-<n>` on Sway, and `Virtual-…` on KDE Plasma. The
  receiver becomes a second monitor: move windows to it, arrange it in your display settings.
  smon removes the output when it exits.
- **`-mode mirror`** creates nothing and streams a screen you choose. The receiver duplicates
  it, at that screen's size.

## The share picker

smon captures through the xdg-desktop-portal ScreenCast interface, so the first run of each
mode opens your compositor's share picker:

- In **extend** mode, pick **SMON-1** (Sway: the new `HEADLESS-<n>`; KDE: allow the new virtual display).
- In **mirror** mode, pick the screen to duplicate.

The choice is saved as a restore token in `~/.config/smon/restore_token-<mode>`, so later runs
don't ask. On Hyprland, tick **Allow a restore token** in the picker, or it asks every time.
On Sway the choice is remembered only in mirror mode, and only with `XDPW_PERSIST_MODE` set. See
[Sway](compositors.md#share-picker-on-sway).

If extend mode receives a screen other than the virtual output, smon forgets the saved choice and exits
with an error, so the next run asks again.

## Placement

Where the virtual output appears depends on the compositor:

- **umbriel**: SMON-1 follows your saved display layout, so move it with the displays plugin
  like any other monitor. `-pos` only sets an initial position. See
  [umbriel placement](compositors.md#placement-on-umbriel).
- **Hyprland**: SMON-1 goes `auto-right` unless you pass `-pos` or have a monitor rule for it.
  See [Hyprland placement](compositors.md#placement-on-hyprland).
- **Sway** and **KDE Plasma**: the compositor's own placement unless you pass `-pos`. Move it
  later with your usual display tool.

## Environment variables

| Variable | Effect |
| --- | --- |
| `SMON_ENCODER` | Use this GStreamer H.264 encoder and skip the benchmark, for example `x264enc` or `varenderD129h264enc`. See [Encoder selection](limitations.md#encoder-selection). |
| `SMON_ABR=off` | Keep the bitrate fixed at `-kbps` instead of adapting to congestion. |
| `ANDROID_SERIAL` | With several Android devices attached, the one `smon usb` uses (standard `adb` variable). |
| `HYPRLAND_INSTANCE_SIGNATURE` | Set by Hyprland. Its presence selects the Hyprland backend. |
| `SWAYSOCK` | Set by Sway. Its presence selects the Sway backend. |
| `KDE_FULL_SESSION`, `XDG_CURRENT_DESKTOP` | Set by Plasma. They select the KDE backend. |

## Files

smon keeps its state under `$XDG_CONFIG_HOME/smon` (usually `~/.config/smon`) and
`$XDG_CACHE_HOME/smon` (usually `~/.cache/smon`).

| Path | Contents | Safe to delete? |
| --- | --- | --- |
| `~/.config/smon/restore_token-extend`, `restore_token-mirror` | Saved share-picker choice per mode | Yes, the picker shows again on the next run |
| `~/.config/smon/webos-<ip>.key` | Pairing key for an LG TV | Yes, the TV asks to *Allow* again |
| `~/.cache/smon/encoder.json` | Fastest encoder per output size | Yes, the benchmark runs again |

smon never edits your compositor's config files. All compositor changes are runtime-only and
undone on exit.
