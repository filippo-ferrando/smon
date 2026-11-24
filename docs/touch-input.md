# Touch, pen and keyboard

With `-input` (`smon web` and `smon usb` only), the tablet controls the shared screen: touches
act as a real touchscreen (tap, drag, swipe to scroll, pinch to zoom), and a stylus acts as a
pen tablet with pressure. With `-keyboard`, viewers can also type.

```sh
smon web -input              # touch and pen
smon web -input -keyboard    # and typing
smon usb -input -keyboard
```

- [Touch](#touch)
- [Pen](#pen)
- [Keyboard](#keyboard)
- [How devices are mapped](#how-devices-are-mapped)
- [Security](#security)
- [Limits](#limits)

## Touch

Fingers drive a virtual touchscreen named `smon touchscreen`. Apps handle it with their own
touch support, as on a laptop with a touch panel. Several devices can touch at once without
mixing up their fingers.

## Pen

A stylus (Apple Pencil, S Pen, any pen the browser reports as `pen`) drives a separate virtual
pen tablet named `smon pen`, mapped to the shared screen like the touchscreen:

- **Pressure** and **tilt**, for drawing apps such as Krita, GIMP or Xournal++.
- **Hover**: the cursor follows the pen before it touches.
- The **eraser** end, and the **side buttons**, where the pen and browser report them.

One device holds the pen at a time: the first to bring a pen near the screen, until it lifts
away. If the pen tablet can't be created or mapped, touch still works and smon logs why.

## Keyboard

`-keyboard` adds a virtual keyboard named `smon keyboard`. It types into the focused window, on
any monitor.

- **A hardware keyboard** on the tablet (Bluetooth, keyboard cover) sends physical keys, so
  your desktop's own keyboard layout applies, shortcuts included.
- **The on-screen keyboard** opens with the ⌨ button in the button bar. It types text, which
  smon turns into key presses on a US layout. With another desktop layout, letters and digits
  mostly come out right but some symbols won't, and characters outside ASCII are skipped. Use a
  hardware keyboard for anything beyond plain text.
- Keys held down when a viewer disconnects are released, so nothing stays stuck.
- In fullscreen, the browser keeps <kbd>Esc</kbd> to leave fullscreen.

## How devices are mapped

smon creates the devices through `/dev/uinput` and asks the compositor to map the touchscreen
and the pen to the shared output. The keyboard needs no mapping. The mapping is a runtime
setting, removed on exit. How it is scoped depends on the compositor:

- **umbriel** maps every touchscreen (and every pen tablet), so a built-in touch panel also maps
  to the shared screen while smon runs. With [several monitors](usage.md#several-monitors),
  touch and pen work on SMON-1 only. See [umbriel](compositors.md#touch-input-on-umbriel).
- **Hyprland** maps only smon's devices, falling back to all touchscreens (or tablets) if the
  config rejects per-device rules. See [Hyprland](compositors.md#touch-input-on-hyprland).
- **labwc** maps input only from `rc.xml`: add the rules in
  [Setup on labwc](compositors.md#setup-on-labwc). smon checks for them and warns when they are
  missing. The touch rule names smon's touchscreen; the tablet rule covers every pen tablet.
- **Sway**, **KDE Plasma** and **GNOME** map only smon's devices. See
  [Sway](compositors.md#touch-input-on-sway), [KDE Plasma](compositors.md#touch-input-on-kde-plasma)
  and [GNOME](compositors.md#gnome).

With several monitors each one has its own touchscreen and pen (`smon touchscreen 2`,
`smon pen 2`, ...), mapped to its output.

Your user needs write access to `/dev/uinput`. See
[Touch input permissions](installation.md#touch-input-permissions).

## Security

Input is **off by default** because anyone holding the URL token can control your desktop.
The token is saved between runs (see [Receivers](receivers.md#browser-webrtc)), so a URL you
handed out keeps working until `-new-token`. `-keyboard` is a separate switch because typing is
as powerful as sitting at your computer: a viewer can open a terminal and run commands.

- Use `-input` and `-keyboard` only on networks and with devices you trust, or over USB, where
  smon listens on 127.0.0.1 only.
- Over Wi-Fi, use `-tls` (after installing smon's certificate on the device) so the token and
  keystrokes don't cross the network in clear.
- Run `smon web -new-token` to lock out every device that had the old URL.

## Limits

- Only in the browser viewer (`web`, `usb`). The webOS viewer is always view-only, and Miracast
  and AirPlay have no input channel.
- No mouse: a mouse on the tablet acts as touch.
- The on-screen keyboard types on a US layout (see [Keyboard](#keyboard)).
