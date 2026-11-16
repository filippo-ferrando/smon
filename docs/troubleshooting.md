# Troubleshooting

Start smon from a terminal: it logs the encoder benchmark, bitrate changes and errors.

### "no supported compositor found"

smon didn't find Hyprland, Sway or KDE Plasma from the session's environment variables, or the
`umbriel` command. Run it from inside your Wayland session, or force a backend with
`-compositor umbriel|hyprland|sway|kde`.
Other compositors are [not supported yet](architecture.md#adding-a-compositor).

### The share picker shows up every time

- On Hyprland, tick **Allow a restore token** in hyprland-share-picker.
- On Sway, start xdg-desktop-portal-wlr with `XDPW_PERSIST_MODE=permanent`. Extend mode still asks
  every run, because Sway gives the output a new name each time.
- On KDE Plasma, leave **Allow the application to do this without asking next time** ticked
  (virtual displays are remembered from Plasma 6.3).
- In extend mode, make sure you pick the virtual output. If smon receives another screen, it forgets the
  choice and exits so the next run asks again.
- To reset the choice yourself, delete `~/.config/smon/restore_token-extend` or
  `restore_token-mirror`.

### The browser page loads but stays black

The page loads over TCP, but WebRTC media also needs **UDP** on the same port. Open the
`-addr` port for both TCP and UDP. See [Firewall](receivers.md#firewall).

### No sound on the receiver

1. Tap the page once (press OK on a TV remote). Browsers keep pages silent until then.
2. Select **Second Monitor (smon)** as the output for the app, or globally. smon never switches
   to it on its own.
3. Check the log for a warning about `opusenc` or `pulsesrc`. If one is missing, smon streams
   video only. See [Checking GStreamer](installation.md#checking-gstreamer).
4. AirPlay never carries audio.

### Sound keeps going to the second monitor

You made *Second Monitor (smon)* the default output once, and WirePlumber remembers it. Pick
another default output. See [Audio](audio.md).

### Laggy or blurry video

- Check the encoder benchmark in the log. If a driver changed, delete
  `~/.cache/smon/encoder.json` to benchmark again, or force one with `SMON_ENCODER`.
- Look for bitrate drops in the log. A congested viewer lowers the quality for everyone. Try a
  less busy Wi-Fi channel, `smon usb`, or a lower `-fps`.
- See [Performance](limitations.md).

### `-input` fails

smon needs write access to `/dev/uinput`. See
[Touch input permissions](installation.md#touch-input-permissions).

### A built-in touchscreen controls the second monitor

On umbriel the touch mapping applies to every touchscreen while smon runs with `-input`. It is
restored on exit. On Hyprland this only happens if your hyprlang config rejects per-device rules.

### `smon usb` doesn't find the tablet

Check `adb devices`. Enable USB debugging and accept the prompt on the tablet. With several
devices attached, set `ANDROID_SERIAL=<serial>`.

### Miracast sink doesn't connect

- Check that NetworkManager shows a `wifi-p2p` device (`nmcli device`).
- Install `dnsmasq`.
- Allow TCP 7236 in your firewall.

### AirPlay receiver rejects the connection (403)

The receiver is a licensed AirPlay 2 device (Apple TV, LG or Samsung TV). smon works only with
open receivers such as UxPlay and RPiPlay.

### LG TV refuses the connection

Enable *Settings → General → Devices → External Devices → TV On With Mobile / LG Connect Apps*.
To pair again, delete `~/.config/smon/webos-<ip>.key`.

### SMON-1 is still there after a crash

The next run of smon removes a stale SMON-1 before creating a new one. To remove it by hand,
use `umbriel output-destroy SMON-1` or `hyprctl output remove SMON-1`.
