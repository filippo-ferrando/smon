# Receivers

smon can stream to five kinds of receiver. The browser viewer is the most capable (audio,
touch, adaptive bitrate). The others cover devices where a browser is not practical.

- [Browser (WebRTC)](#browser-webrtc)
- [HTTPS and the certificate](#https-and-the-certificate)
- [Installing the viewer as an app](#installing-the-viewer-as-an-app)
- [USB (Android)](#usb-android)
- [Miracast](#miracast)
- [AirPlay](#airplay)
- [LG webOS](#lg-webos)
- [Firewall](#firewall)

## Browser (WebRTC)

```sh
smon web [-addr :8080] [-input] [-keyboard]
```

smon prints one URL per network address, each with an access token (`/?t=…`), and the first
one as a QR code: scan it with the tablet's camera. Open it in any modern browser on the same
network. Video is H.264 (or [H.265 or AV1](usage.md#codecs)) over WebRTC and audio is Opus.

- The token is the only thing protecting the page. It is saved in `~/.config/smon/token` and
  stays the same between runs, so a bookmarked or installed viewer keeps working. Anyone who
  ever had the URL can connect until you run smon with `-new-token`. Share it only with devices
  you trust, especially with `-input` or `-keyboard`.
- If smon stops or the network drops, the page shows *reconnecting…* and comes back on its
  own when smon is reachable again.
- The **i** button in the corner shows stream stats: encoder, bitrate, frame rate, round trip,
  loss. The button bar appears for a few seconds after each touch.
- Browsers keep the page silent until you interact with it, so **tap the page once** for sound.
  A "tap for sound" hint shows until you do.
- Use the corner button for fullscreen. It appears for a few seconds after each touch.
- HTTP and the WebRTC media share one port number, on TCP and UDP. See [Firewall](#firewall).

## HTTPS and the certificate

`smon web` serves the page over HTTPS (`-tls=false` turns that off). The certificate comes
from a small certificate authority smon creates on first use, in `~/.config/smon/tls/`. It can
only vouch for local addresses and names (private IP ranges, `localhost`, `.local`, `.lan`,
`home.arpa` and this machine's name), never for a public site.

- **Without further setup**, the browser warns about the certificate the first time. Accept
  it and the page works, including keeping the screen awake.
- **To trust smon for good**, open `https://<address>:<port>/ca.crt` on the device (smon prints
  the link) and install it:
  - **Android:** *Settings → Security → Encryption & credentials → Install a certificate → CA
    certificate*, then pick the downloaded `smon-ca.crt`. The menu path varies by vendor.
  - **iPad/iPhone:** open the link in Safari and allow the profile download, install it in
    *Settings → General → VPN & Device Management*, then turn it on in *Settings → General →
    About → Certificate Trust Settings*.

  The warning is then gone for good, and the page can be installed as an app. When your LAN
  address changes, smon issues a new server certificate from the same authority, so devices
  stay trusted.

Over USB (`smon usb`) and on webOS TVs smon keeps plain HTTP: `localhost` is already trusted,
and a TV browser can't accept a certificate warning.

## Installing the viewer as an app

Once the device trusts smon's certificate, the browser offers to install the viewer (an
install button appears in the button bar on Chrome; on iOS use *Share → Add to Home Screen*).
The app opens fullscreen, keeps the screen awake, and when smon is not running it waits on a
"reconnecting…" screen instead of an error page. Its link carries the saved token; after
`-new-token`, install it again from the new URL.

## USB (Android)

```sh
smon usb [-addr :8080] [-input] [-keyboard]
```

1. On the tablet, enable *Developer options → USB debugging*, plug it in and accept the prompt.
2. Run `smon usb` (add `-input` for touch). smon runs `adb reverse` on the port and opens
   `http://localhost:<port>/` in the tablet's default browser. Chrome is recommended.

`adb reverse` carries only TCP, so over USB the page skips WebRTC. It receives the video over a
WebSocket and decodes it with WebCodecs, which works because `localhost` is a secure context.
Audio is Opus over the same WebSocket, also decoded with WebCodecs.

smon listens only on 127.0.0.1 in this mode, so it needs no firewall rule. On exit it removes
the reverse mapping. With several devices attached, pick one with `ANDROID_SERIAL=<serial>`.

**Fallback for any tablet:** turn on *USB tethering* on the tablet and run plain `smon web`.
smon prints a URL for the tethering interface too.

## Miracast

```sh
smon list                          # find sinks
smon cast miracast:<name|mac>
```

smon drives Wi-Fi Direct through NetworkManager, so you need a Wi-Fi card that exposes a
`wifi-p2p` device, and `dnsmasq` for the case where smon becomes the group owner.

- smon negotiates 1920x1080p30 when the sink supports it, otherwise 1280x720p30.
- Miracast is H.264 only: `-codec` must stay `h264`.
- Audio is AAC inside the MPEG-TS, sent when the sink advertises AAC and audio is on.
- The sink connects back to smon on **TCP 7236**. Open it in your firewall.

## AirPlay

```sh
smon list                          # find receivers (needs avahi-daemon)
smon cast airplay:<name|host:port>
```

AirPlay works only with **open receivers** such as [UxPlay](https://github.com/FDH2/UxPlay) and
RPiPlay. Licensed AirPlay 2 receivers (Apple TV, LG and Samsung TVs) require HomeKit pairing
and real FairPlay, and reject smon at pair-setup with a 403.

AirPlay mirroring is H.264 only, and carries no audio, because AirPlay audio is a separate encrypted stream that
smon does not implement. The receiver also sends NTP to a random UDP port on your machine.
See [Firewall](#firewall).

## LG webOS

```sh
smon cast webos:<tv-ip>
```

smon pairs with the TV and opens the TV's own browser on the WebRTC viewer page. The first time,
press **Allow** on the TV. The pairing key is stored in `~/.config/smon/webos-<ip>.key`.

- If the TV refuses the connection, enable *Settings → General → Devices → External Devices →
  TV On With Mobile / LG Connect Apps*.
- Press **OK** on the remote to start sound (the browser's interaction requirement).
- The webOS viewer is always view-only. `-input` is not available.
- The TV browser gets plain HTTP, and may not decode `-codec h265` or `av1`.
- It uses the `-addr` port like `smon web`. See [Firewall](#firewall).

## Firewall

| Receiver | What to allow |
| --- | --- |
| Browser, webOS | The `-addr` port (default 8080), **TCP and UDP**. HTTP and WebRTC media share that port number. |
| USB | Nothing, smon listens on 127.0.0.1 only. |
| Miracast | **TCP 7236** (the sink connects to smon). |
| AirPlay | NTP from the receiver to a random UDP port. Allow that, or allow the receiver's IP. |

With ufw, for example:

```sh
sudo ufw allow 8080              # web and webOS (TCP and UDP)
sudo ufw allow 7236/tcp          # Miracast
sudo ufw allow from <receiver-ip> # AirPlay
```
