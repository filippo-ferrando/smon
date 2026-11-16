# Compositors

smon needs two things from a compositor: a way to create a virtual output, and an
xdg-desktop-portal ScreenCast implementation to capture it. The portal part is the same
everywhere. The output, placement and touch mapping are what each backend implements.

| | umbriel | Hyprland |
| --- | --- | --- |
| Detected by | `umbriel` on `PATH` | `$HYPRLAND_INSTANCE_SIGNATURE` set |
| Virtual output | `umbriel output-create` | `hyprctl output create headless` |
| Default placement | Saved display layout | `auto-right` |
| Share picker | umbriel share picker | hyprland-share-picker |
| Touch mapping | All touchscreens | Only smon's touchscreen (with a fallback) |

Every feature works the same on both.

## umbriel

Requires umbriel and `xdg-desktop-portal-umbriel`.

### Virtual output on umbriel

In extend mode smon creates SMON-1 with `umbriel output-create` at `-w`x`-h`@`-fps`, after
removing any stale SMON-1 left behind by a crash. It destroys the output on exit.

### Placement on umbriel

SMON-1 follows your saved display layout (`~/.config/umbriel/outputs.toml`, written by the
displays plugin), so move it with the displays plugin like any other monitor.

`-pos right|left|above|below` sets only an initial position, as a runtime override. As soon
as you rearrange displays in the plugin, smon drops its override so the plugin's layout
applies. On exit, smon removes any override it set.

### Share picker on umbriel

The first run of each mode opens the umbriel share picker. In extend mode pick **SMON-1**; in
mirror mode pick the screen to duplicate. See [The share picker](usage.md#the-share-picker).

### Touch input on umbriel

smon maps its touchscreen with the runtime setting `input.touch.map_to_output`, removed on exit.
That umbriel setting covers **every** touchscreen, so a built-in touch panel also maps to the
shared screen while smon runs.

## Hyprland

Requires Hyprland **0.42 or newer** and `xdg-desktop-portal-hyprland`.

smon drives Hyprland with `hyprctl`. Runtime rules go through `hyprctl eval` (Lua) when the
session uses a Lua config, which Hyprland 0.57+ always does, and through `hyprctl keyword` with
a hyprlang config. Nothing is written to your config files, and these rules last only for the
session.

### Virtual output on Hyprland

`hyprctl output create headless SMON-1`. smon sets a runtime monitor rule for its size and
refresh rate, and removes the output on exit.

### Placement on Hyprland

With no `-pos`, SMON-1 goes `auto-right`. `-pos right|left|above|below` places it next to your
other monitors.

To keep it somewhere specific, add a monitor rule to your config, for example:

```ini
monitor = SMON-1, 1920x1080@30, -1920x0, 1
```

The `-w`, `-h` and `-fps` flags still apply when the output is created.

### Share picker on Hyprland

xdg-desktop-portal-hyprland shows hyprland-share-picker. Pick SMON-1 (extend) or the screen to
duplicate (mirror), and tick **Allow a restore token** so later runs skip the picker.

### Touch input on Hyprland

`-input` maps only smon's own touchscreen (`device[smon-touchscreen]:output`), so a built-in
touch panel keeps working on its own screen. If a hyprlang config rejects per-device rules,
smon falls back to `input:touchdevice:output` (every touchscreen) and restores the previous
value on exit.

## Other compositors

Support for more Wayland compositors is planned. If you want to add one, see
[Adding a compositor](architecture.md#adding-a-compositor).
