# smon: a virtual second monitor for umbriel and Hyprland

smon creates a virtual output on the umbriel or Hyprland compositor and streams it to:

- **a browser** over WebRTC (any device on the LAN),
- **an Android tablet over USB** (adb), no Wi-Fi needed,
- **Miracast** sinks over Wi-Fi Direct (through NetworkManager),
- **AirPlay** open receivers (UxPlay, RPiPlay),
- **LG webOS TVs**: opens the TV's own browser on the WebRTC page.

```
make build                                # builds bin/smon (or: go build -o bin/smon ./cmd/smon)
smon web  [-addr :8080] [-input]          # prints a URL with an access token
smon usb  [-addr :8080] [-input]          # Android over USB: opens the page on the tablet
smon list                                 # AirPlay (mDNS) + Miracast (P2P scan)
smon cast miracast:<name|mac>
smon cast airplay:<name|host:port>
smon cast webos:<tv-ip>                   # first time: press Allow on the TV
flags: -mode extend|mirror -w 1920 -h 1080 -fps 30 -kbps 8000 [-pos right|left|above|below] [-audio=false]
       [-compositor umbriel|hyprland]     # default: detected (Hyprland when $HYPRLAND_INSTANCE_SIGNATURE is set)
```

The sections below describe umbriel; see [Hyprland](#hyprland) for what differs there.

**Placement:** SMON-1 follows your saved display layout (`~/.config/umbriel/outputs.toml`, written by the
displays plugin), so move it with the displays plugin like any other monitor. `-pos` sets only an
initial position. As soon as you rearrange in the plugin, smon drops its override so the plugin's
layout applies. On exit, smon removes any override it set.

- `-mode extend` (default) creates a virtual output called SMON-1 and streams it. The receiver becomes a second monitor.
- `-mode mirror` creates nothing and streams the screen you pick. The receiver duplicates it, at that screen's size.

The first run of each mode opens the umbriel share picker. In extend mode pick **SMON-1**; in mirror
mode pick the screen to duplicate. The choice is saved in `~/.config/smon/restore_token-<mode>`,
so later runs don't ask. If extend mode gets a screen other than SMON-1, smon forgets the choice
and exits with an error, so the next run asks again. smon removes the virtual output when it exits.

## Audio

While smon streams, a new sound output called **Second Monitor (smon)** appears in your sound
settings (pavucontrol, the desktop's volume menu, …). smon never switches to it on its own: pick it
as the output, globally or per app, and that sound plays on the receiver instead of here. Switch back
any time. When smon exits the device goes away and its streams fall back to your default output.
If you make it the *default* output, WirePlumber remembers that, so on the next run it becomes the
default again by itself as soon as it appears. Choose another output to undo that.

- Browser (web, webOS): Opus over WebRTC. USB: Opus over the WebSocket, decoded with WebCodecs.
- Miracast: AAC in the MPEG-TS, when the sink advertises AAC (smon offers it only when audio is on).
- Browsers keep the page silent until a gesture, so **tap the page once** (a "tap for sound" hint
  shows until then). On a TV, press OK on the remote.
- `-audio=false` streams video only. If `opusenc` or `pulsesrc` is missing, smon warns and streams
  video only.

## Touch input

With `-input` (web and usb only), touches on the tablet act as a real touchscreen on the shared screen:
tap, drag, swipe to scroll, pinch to zoom, all handled by the apps' own touch support. smon creates
a virtual touchscreen through `/dev/uinput` and maps it with `input.touch.map_to_output` (a runtime
override, removed on exit). That umbriel setting covers every touchscreen, so a built-in touch panel
also maps to the shared screen while smon runs.

Input is off by default because anyone holding the URL token can control the desktop. Over Wi-Fi
the URL travels as plain HTTP. The webOS path is always view-only.

Use the corner button for fullscreen; it appears for a few seconds after each touch.

## Hyprland

smon detects Hyprland from `$HYPRLAND_INSTANCE_SIGNATURE` and drives it with `hyprctl`. Runtime
rules go through `hyprctl eval` (Lua) with a Lua config, which 0.57+ always uses, and through
`hyprctl keyword` with a hyprlang config. Nothing is written to your config files, and these rules
last only for the session. Every feature works the same; these parts differ:

- **Virtual output:** `hyprctl output create headless SMON-1` (needs Hyprland 0.42 or newer). smon sets
  a runtime monitor rule for its size and refresh rate, and removes the output on exit.
- **Placement:** with no `-pos`, SMON-1 goes `auto-right`. `-pos right|left|above|below` places it
  next to your other monitors. To keep it somewhere specific, add a rule to your config, for example
  `monitor = SMON-1, 1920x1080@30, -1920x0, 1`. The `-w`/`-h`/`-fps` flags still apply when the
  output is created.
- **Share picker:** xdg-desktop-portal-hyprland shows hyprland-share-picker. Pick SMON-1 (extend)
  or the screen to duplicate (mirror), and tick **Allow a restore token** so later runs skip the
  picker.
- **Touch input:** `-input` maps only smon's own touchscreen (`device[smon-touchscreen]:output`), so a
  built-in touch panel keeps working on its own screen. If a hyprlang config rejects per-device rules,
  smon falls back to `input:touchdevice:output` (every touchscreen) and restores it on exit.

### Dependencies on Hyprland

Package names are for Arch (and CachyOS, Manjaro, …); other distributions use similar names.

| Needed for | Packages |
| --- | --- |
| Compositor and screen capture | `hyprland` (0.42+, provides `hyprctl`), `xdg-desktop-portal-hyprland`, `pipewire`, `wireplumber` |
| Video pipeline (every mode) | `gstreamer`, `gst-plugins-base`, `gst-plugins-good`, `gst-plugins-bad`, `gst-plugin-pipewire` |
| H.264 encoder | `gst-plugin-va` (hardware, VA-API) or `gst-plugins-ugly` (`x264enc`, software fallback) |
| Audio | `pipewire-pulse`, `libpulse` (`pactl`); `gst-libav` for Miracast AAC |
| USB (`smon usb`) | `android-tools` (`adb`) |
| Miracast | `networkmanager` (with a Wi-Fi P2P capable card), `dnsmasq` |
| AirPlay | `avahi` (running `avahi-daemon`) |
| Touch input (`-input`) | write access to `/dev/uinput` (see [Requirements](#requirements)) |
| Building | `go`, `gcc`, `pkgconf` (cgo: the AirPlay playfair code, and the video pipeline, which links GStreamer; Arch ships its headers in `gstreamer`/`gst-plugins-base`) |

Everything at once:

```
sudo pacman -S --needed hyprland xdg-desktop-portal-hyprland pipewire wireplumber pipewire-pulse libpulse \
  gstreamer gst-plugins-base gst-plugins-good gst-plugins-bad gst-plugin-pipewire gst-plugin-va \
  gst-plugins-ugly gst-libav android-tools networkmanager dnsmasq avahi go gcc pkgconf
```

Check that GStreamer finds the elements smon uses:

```
for e in pipewiresrc h264parse rtph264pay mpegtsmux opusenc pulsesrc; do gst-inspect-1.0 $e >/dev/null || echo "missing: $e"; done
gst-inspect-1.0 vah264enc >/dev/null || gst-inspect-1.0 x264enc >/dev/null || echo "missing: an H.264 encoder"
```

## USB (Android)

1. On the tablet, enable *Developer options → USB debugging*, plug it in and accept the prompt.
2. Run `smon usb` (add `-input` for touch). smon runs `adb reverse` on the port and opens
   `http://localhost:<port>/` in the tablet's default browser (Chrome recommended).

`adb reverse` carries only TCP, so over USB the page skips WebRTC: it receives H.264 over a
WebSocket and decodes it with WebCodecs. WebCodecs works because `localhost` is a secure context.
smon listens only on 127.0.0.1 in this mode, so it needs no firewall rule. On exit it removes the
reverse mapping. With several devices attached, pick one with `ANDROID_SERIAL=<serial>`.

Fallback for any tablet: turn on *USB tethering* on the tablet and run plain `smon web`. smon
prints a URL for the tethering interface too.

## Requirements

- umbriel + xdg-desktop-portal-umbriel, **or** Hyprland 0.42+ + xdg-desktop-portal-hyprland; PipeWire
- GStreamer 1.2x with `pipewiresrc`, `vah264enc` or `x264enc`, `h264parse`, `rtph264pay`,
  `mpegtsmux`, `rtpmp2tpay`, `avenc_aac`, `pulsesrc`, plus `opusenc` + `rtpopuspay` for browser/USB audio
  (gst-plugins-base, gst-plugins-good)
- Audio: `pactl` (works with pipewire-pulse)
- Miracast: NetworkManager with a `wifi-p2p` device, plus dnsmasq when we end up group owner
- AirPlay: avahi-daemon (`avahi-browse`), and cgo (for the playfair code)
- USB: `adb` (android-tools)
- Touch input: write access to `/dev/uinput` (for example a `uaccess` udev rule, or the `input` group)

### Arch Linux packages

Build:

```
sudo pacman -S --needed go gcc pkgconf
```

Runtime (core: capture, encoding, web/USB streaming, audio):

```
sudo pacman -S --needed pipewire pipewire-pulse libpulse \
    gstreamer gst-plugins-base gst-plugins-good gst-plugins-bad-libs \
    gst-plugin-pipewire gst-plugin-va gst-plugins-ugly gst-libav
```

| Package | Provides |
|---|---|
| `gst-plugin-pipewire` | `pipewiresrc` (screen capture) |
| `gst-plugin-va` | `vah264enc` (hardware H.264; needs a VA-API driver such as `intel-media-driver` or `libva-mesa-driver`) |
| `gst-plugins-ugly` | `x264enc` (software encoder; often the fastest, see below) |
| `gst-plugins-bad-libs` | `h264parse`, `mpegtsmux` |
| `gst-plugins-good` | `rtph264pay`, `rtpmp2tpay`, `rtpopuspay`, `pulsesrc`, `aacparse`, `udpsink` |
| `gst-plugins-base` | `opusenc`, `videoconvert`, `videoscale`, `audioconvert` |
| `gst-libav` | `avenc_aac` (Miracast audio) |
| `libpulse` | `pactl` |

Optional, per feature:

```
sudo pacman -S --needed android-tools                 # smon usb
sudo pacman -S --needed networkmanager dnsmasq        # Miracast
sudo pacman -S --needed avahi                         # AirPlay discovery (enable avahi-daemon.service)
```

umbriel and xdg-desktop-portal-umbriel must be installed as well.

## Firewall

- Miracast: the sink connects to us on **TCP 7236**. With ufw: `sudo ufw allow 7236/tcp`.
- AirPlay: the receiver sends NTP to a random UDP port on this machine. Allow it, or allow the receiver's IP.
- Web / webOS: the `-addr` port (default 8080), **TCP and UDP**. HTTP and the WebRTC media share that port number.

The webOS pairing key is stored in `~/.config/smon/webos-<ip>.key`. If the TV refuses the
connection, enable *Settings → General → Devices → External Devices → TV On With Mobile / LG Connect Apps*.

## Limits

- **AirPlay works only with open receivers** (UxPlay/RPiPlay). Licensed AirPlay 2 receivers (Apple TV,
  LG/Samsung TVs) need HomeKit pairing and real FairPlay. They reject us at pair-setup (403).
- At startup smon times every H.264 encoder (each GPU's VA-API encoder, and x264) at the output size and
  uses the fastest, since time per frame is latency the encoder adds and older GPUs can be slower than
  x264. The log prints the timings; `SMON_ENCODER=x264enc` (or e.g. `varenderD129h264enc`) skips the test.
  The winner is saved in `~/.cache/smon/encoder.json` per output size, so later runs start without the
  test. It runs again when the set of encoders changes (new GPU or plugins), or when the capture
  pipeline fails within its first 10 s. Delete the file to force a new test, for example after a driver update.
- Browser viewers get a keyframe on demand: when they connect, when they report loss (PLI/FIR), and when
  a USB viewer falls behind. At most one every 500 ms; the periodic one comes every 5 s. AirPlay and
  Miracast receivers can't ask, so they keep a 1-second GOP.
- WebRTC viewers adapt the bitrate: on congestion (GCC delay overuse, or >10% loss via transport-cc) the
  encoder drops 30%, down to a tenth of `-kbps`; after 3 s without, it climbs 15% every 2 s back to
  `-kbps`. The encoder is shared, so the most congested viewer sets the quality for everyone, USB
  included. Each change is logged; `SMON_ABR=off` keeps the bitrate fixed. With a VA-API encoder each
  change also costs one keyframe.
- Miracast negotiates 1920x1080p30 when the sink supports it, otherwise 1280x720p30. AAC audio is sent
  when the sink advertises it.
- AirPlay mirroring carries no audio (AirPlay audio is a separate encrypted stream smon doesn't implement).
- Browser audio and video are not lip-synced to each other; both are low-latency, so the drift is small.
- Touch input supports touch only (no keyboard), and only in the browser viewer.

## Development

```
make            # fmt-check, vet, lint, short tests, then build
make test       # full tests, including the real-encoder ones (needs GStreamer)
make fmt        # goimports + gofumpt
make lint       # staticcheck with every check enabled
```

| Path | What it holds |
| --- | --- |
| `cmd/smon` | command line: flag parsing and wiring |
| `internal/display` | portal screencast of SMON-1 or a mirrored screen, and its encoder pipeline |
| `internal/compositor` | virtual output, placement and touch mapping on umbriel and Hyprland |
| `internal/gst` | in-process GStreamer pipelines, encoder selection, keyframe and bitrate control |
| `internal/h264` | Annex-B access units from RTP |
| `internal/web` | WebRTC and WebSocket viewers, adaptive bitrate, `smon usb` |
| `internal/cast` | Miracast (WFD RTSP), AirPlay, webOS, receiver discovery |
| `internal/audio` | the "Second Monitor (smon)" sound output and its capture |
| `internal/touch` | virtual uinput touchscreen |
| `internal/config` | per-user state directory |

## License

GPL-3.0-or-later. `internal/cast/playfair/` is vendored from [UxPlay](https://github.com/FDH2/UxPlay) (GPL-3).
