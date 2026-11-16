# Touch input

With `-input` (`smon web` and `smon usb` only), touches on the tablet act as a real touchscreen
on the shared screen: tap, drag, swipe to scroll, pinch to zoom. Apps handle them with their own
touch support.

```sh
smon web -input
smon usb -input
```

## How it works

smon creates a virtual touchscreen named `smon touchscreen` through `/dev/uinput`, and asks the
compositor to map it to the shared output. The mapping is a runtime setting and is removed on
exit. How the mapping is scoped depends on the compositor:

- **umbriel** maps every touchscreen, so a built-in touch panel also maps to the shared screen
  while smon runs. See [umbriel](compositors.md#touch-input-on-umbriel).
- **Hyprland** maps only smon's touchscreen, falling back to all touchscreens if the config
  rejects per-device rules. See [Hyprland](compositors.md#touch-input-on-hyprland).
- **Sway** and **KDE Plasma** map only smon's touchscreen. See
  [Sway](compositors.md#touch-input-on-sway) and [KDE Plasma](compositors.md#touch-input-on-kde-plasma).

Your user needs write access to `/dev/uinput`. See
[Touch input permissions](installation.md#touch-input-permissions).

## Security

Input is **off by default** because anyone holding the URL token can control your desktop, and
over Wi-Fi the URL travels as plain HTTP. Use `-input` only on networks you trust, or over USB,
where smon listens on 127.0.0.1 only.

## Limits

- Touch only: no keyboard, mouse or pen.
- Only in the browser viewer (`web`, `usb`). The webOS viewer is always view-only, and Miracast
  and AirPlay have no input channel.
