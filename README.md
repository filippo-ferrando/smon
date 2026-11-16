# smon: a virtual second monitor for umbriel and Hyprland

smon creates a virtual output on the umbriel or Hyprland compositor and streams it to:

- **a browser** over WebRTC (any device on the LAN),
- **an Android tablet over USB** (adb), no Wi-Fi needed,
- **Miracast** sinks over Wi-Fi Direct (through NetworkManager),
- **AirPlay** open receivers (UxPlay, RPiPlay),
- **LG webOS TVs**: opens the TV's own browser on the WebRTC page.

```
go build -o smon .
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

## Firewall

- Miracast: the sink connects to us on **TCP 7236**. With ufw: `sudo ufw allow 7236/tcp`.
- AirPlay: the receiver sends NTP to a random UDP port on this machine. Allow it, or allow the receiver's IP.
- Web / webOS: the `-addr` port (default 8080), **TCP and UDP**. HTTP and the WebRTC media share that port number.

The webOS pairing key is stored in `~/.config/smon/webos-<ip>.key`. If the TV refuses the
connection, enable *Settings → General → Devices → External Devices → TV On With Mobile / LG Connect Apps*.

## Limits

- **AirPlay works only with open receivers** (UxPlay/RPiPlay). Licensed AirPlay 2 receivers (Apple TV,
  LG/Samsung TVs) need HomeKit pairing and real FairPlay. They reject us at pair-setup (403).
- The encoder runs as a `gst-launch-1.0` child, so a receiver's IDR request can't force a keyframe.
  A 1-second GOP covers it instead.
- Miracast negotiates 1920x1080p30 when the sink supports it, otherwise 1280x720p30. AAC audio is sent
  when the sink advertises it.
- AirPlay mirroring carries no audio (AirPlay audio is a separate encrypted stream smon doesn't implement).
- Browser audio and video are not lip-synced to each other; both are low-latency, so the drift is small.
- Touch input supports touch only (no keyboard), and only in the browser viewer.

## License

GPL-3.0-or-later. `playfair/` is vendored from [UxPlay](https://github.com/FDH2/UxPlay) (GPL-3).
