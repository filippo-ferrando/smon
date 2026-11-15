# smon: a virtual second monitor for umbriel

smon creates a virtual output on the umbriel compositor and streams it to:

- **a browser** over WebRTC (any device on the LAN),
- **Miracast** sinks over Wi-Fi Direct (through NetworkManager),
- **AirPlay** open receivers (UxPlay, RPiPlay),
- **LG webOS TVs**: opens the TV's own browser on the WebRTC page.

```
go build -o smon .
smon web  [-addr :8080]                   # prints a URL with an access token
smon list                                 # AirPlay (mDNS) + Miracast (P2P scan)
smon cast miracast:<name|mac>
smon cast airplay:<name|host:port>
smon cast webos:<tv-ip>                   # first time: press Allow on the TV
flags: -mode extend|mirror -w 1920 -h 1080 -fps 30 -kbps 8000 [-pos right|left|above|below]
```

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

## Requirements

- umbriel, xdg-desktop-portal-umbriel, PipeWire
- GStreamer 1.2x with `pipewiresrc`, `vah264enc` or `x264enc`, `h264parse`, `rtph264pay`,
  `mpegtsmux`, `rtpmp2tpay`, `avenc_aac`, `pulsesrc`
- Miracast: NetworkManager with a `wifi-p2p` device, plus dnsmasq when we end up group owner
- AirPlay: avahi-daemon (`avahi-browse`), and cgo (for the playfair code)

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
- The web viewer is view-only (no input back to the desktop).

## License

GPL-3.0-or-later. `playfair/` is vendored from [UxPlay](https://github.com/FDH2/UxPlay) (GPL-3).
