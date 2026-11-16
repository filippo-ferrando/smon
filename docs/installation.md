# Installation

smon is a single Go binary. It uses cgo for the GStreamer video pipeline and the AirPlay
playfair code, so you need a C toolchain and the GStreamer headers to build it.

- [Requirements](#requirements)
- [Arch Linux](#arch-linux)
- [Other distributions](#other-distributions)
- [Building](#building)
- [Checking GStreamer](#checking-gstreamer)
- [Touch input permissions](#touch-input-permissions)

## Requirements

| Needed for | Requirement |
| --- | --- |
| Compositor | umbriel with `xdg-desktop-portal-umbriel`, **or** Hyprland 0.42+ (provides `hyprctl`) with `xdg-desktop-portal-hyprland` |
| Screen capture | PipeWire and WirePlumber |
| Video pipeline | GStreamer 1.2x with `pipewiresrc`, `h264parse`, `rtph264pay`, `mpegtsmux`, `rtpmp2tpay` |
| H.264 encoder | `vah264enc` (hardware, VA-API) and/or `x264enc` (software) |
| Audio | `pactl` (works with pipewire-pulse), `pulsesrc`; `opusenc` + `rtpopuspay` for browser/USB; `avenc_aac` for Miracast |
| `smon usb` | `adb` (android-tools) |
| Miracast | NetworkManager with a `wifi-p2p` capable device, plus `dnsmasq` (used when smon ends up group owner) |
| AirPlay | `avahi-daemon` running (`avahi-browse` is used for discovery) |
| Touch input (`-input`) | Write access to `/dev/uinput`, see [below](#touch-input-permissions) |
| Building | Go (see `go.mod`), `gcc`, `pkg-config` |

## Arch Linux

Package names are for Arch Linux and its derivatives (CachyOS, Manjaro, …).

**Build tools**

```sh
sudo pacman -S --needed go gcc pkgconf
```

**Core runtime** (capture, encoding, web and USB streaming, audio)

```sh
sudo pacman -S --needed pipewire wireplumber pipewire-pulse libpulse \
    gstreamer gst-plugins-base gst-plugins-good gst-plugins-bad-libs \
    gst-plugin-pipewire gst-plugin-va gst-plugins-ugly gst-libav
```

**Compositor**, pick yours:

```sh
# umbriel: install umbriel and xdg-desktop-portal-umbriel
sudo pacman -S --needed hyprland xdg-desktop-portal-hyprland   # Hyprland
```

**Optional, per feature**

```sh
sudo pacman -S --needed android-tools             # smon usb
sudo pacman -S --needed networkmanager dnsmasq    # Miracast
sudo pacman -S --needed avahi                     # AirPlay discovery
sudo systemctl enable --now avahi-daemon.service
```

**What each GStreamer package provides**

| Package | Provides |
| --- | --- |
| `gst-plugin-pipewire` | `pipewiresrc` (screen capture) |
| `gst-plugin-va` | `vah264enc` (hardware H.264; needs a VA-API driver such as `intel-media-driver` or `libva-mesa-driver`) |
| `gst-plugins-ugly` | `x264enc` (software encoder; often the fastest, see [Performance](limitations.md#encoder-selection)) |
| `gst-plugins-bad-libs` | `h264parse`, `mpegtsmux` |
| `gst-plugins-good` | `rtph264pay`, `rtpmp2tpay`, `rtpopuspay`, `pulsesrc`, `aacparse`, `udpsink` |
| `gst-plugins-base` | `opusenc`, `videoconvert`, `videoscale`, `audioconvert` |
| `gst-libav` | `avenc_aac` (Miracast audio) |
| `libpulse` | `pactl` |

The Arch `gstreamer` and `gst-plugins-base` packages ship the headers that cgo needs.

## Other distributions

smon is not tied to Arch. Install the equivalents of the packages above. Most distributions
split GStreamer the same way (base, good, bad, ugly, libav), and some ship headers in
separate `-dev` or `-devel` packages, which you need for building.

## Building

```sh
git clone https://git.fferrando.cc/rdfilippo/smon smon
cd smon
make build          # or: go build -o bin/smon ./cmd/smon
```

The binary is `bin/smon`. Copy it anywhere on your `PATH`, for example:

```sh
install -Dm755 bin/smon ~/.local/bin/smon
```

## Checking GStreamer

Check that GStreamer finds every element smon uses:

```sh
for e in pipewiresrc h264parse rtph264pay mpegtsmux opusenc pulsesrc; do
    gst-inspect-1.0 $e >/dev/null || echo "missing: $e"
done
gst-inspect-1.0 vah264enc >/dev/null || gst-inspect-1.0 x264enc >/dev/null || echo "missing: an H.264 encoder"
```

If `opusenc` or `pulsesrc` is missing, smon still runs. It warns and streams video only.

## Touch input permissions

`-input` creates a virtual touchscreen through `/dev/uinput`, so your user needs write access
to it. Either add a udev rule that grants it to the logged-in user (`uaccess`):

```sh
echo 'KERNEL=="uinput", SUBSYSTEM=="misc", TAG+="uaccess", OPTIONS+="static_node=uinput"' \
    | sudo tee /etc/udev/rules.d/60-smon-uinput.rules
```

then reboot (or reload the rules and log in again). Some packages, such as KDE Connect,
already install an equivalent rule. Alternatively, add your user to the `input` group if
that group owns `/dev/uinput` on your system.
