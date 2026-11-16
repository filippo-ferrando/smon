# Compositors

smon needs two things from a compositor: a way to create a virtual output, and an
xdg-desktop-portal ScreenCast implementation to capture it. The portal part is the same
everywhere. The output, placement and touch mapping are what each backend implements.

| | umbriel | Hyprland | Sway | KDE Plasma |
| --- | --- | --- | --- | --- |
| Detected by | `umbriel` on `PATH` | `$HYPRLAND_INSTANCE_SIGNATURE` set | `$SWAYSOCK` set | `$KDE_FULL_SESSION=true` or `KDE` in `$XDG_CURRENT_DESKTOP` |
| Virtual output | `umbriel output-create` | `hyprctl output create headless` | `swaymsg create_output` | The portal (ScreenCast `VIRTUAL` source) |
| Output name | `SMON-1` | `SMON-1` | `HEADLESS-<n>` | `Virtual-…` |
| Size | `-w`/`-h`/`-fps` | `-w`/`-h`/`-fps` | `-w`/`-h`/`-fps` | Chosen in the portal dialog (6.8+), else 1920x1080 at 60 Hz |
| Default placement | Saved display layout | `auto-right` | Sway's default | KWin's default |
| Share picker | umbriel share picker | hyprland-share-picker | xdg-desktop-portal-wlr chooser | Plasma's share dialog |
| Touch mapping | All touchscreens | Only smon's touchscreen (with a fallback) | Only smon's touchscreen | Only smon's touchscreen |

Every feature works on all four, with the differences listed here.

> **Note:** the Sway and KDE Plasma backends were written from the compositors' sources and are
> covered by unit tests, but have not been run on real sessions yet. Reports are welcome.

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

## Sway

Requires Sway **1.8 or newer** (for `create_output` and `output … unplug`) and
`xdg-desktop-portal-wlr`, ideally **0.9 or newer**.

smon drives Sway with `swaymsg`. All changes are runtime commands. Nothing is written to your
config files.

### Virtual output on Sway

`swaymsg create_output` adds a headless output. Sway names it itself (`HEADLESS-<n>`, a new
number every time), so smon finds it by comparing the outputs before and after. smon then sets
its mode to `-w`x`-h`@`-fps` and removes it on exit (`output … unplug`). The name is kept in
`~/.config/smon/sway-output`, so a run after a crash removes the leftover output.

### Placement on Sway

With no `-pos`, Sway places the output itself (to the right of the others). `-pos
right|left|above|below` places it next to your other monitors. Move it later with
`swaymsg output HEADLESS-<n> pos X Y` or a tool such as wdisplays.

### Share picker on Sway

xdg-desktop-portal-wlr picks the output with the chooser set in its config (`chooser_type`,
by default slurp, wmenu, wofi, rofi and others, whichever is installed). Pick the new
`HEADLESS-<n>` output in extend mode.

- The portal returns the output's name (`mapping_id`) from version 0.9. With older versions,
  smon finds the output by its position.
- The portal hands out restore tokens only when it runs with `XDPW_PERSIST_MODE=transient` or
  `permanent` in its environment. Even then extend mode asks every time, because the token
  names the output and Sway names it differently on each run.

### Touch input on Sway

`-input` maps only smon's touchscreen (`input 4617:21312:smon_touchscreen map_to_output …`),
so a built-in touch panel keeps working on its own screen. On exit smon resets the rule to
`map_to_output *`.

## KDE Plasma

Requires Plasma **6** with `xdg-desktop-portal-kde`, `kscreen-doctor` (for `-pos`) and
`kwriteconfig6` (to clean up the touch mapping). Plasma installs all three.

### Virtual output on KDE Plasma

Plasma has no command to create an output. In extend mode smon asks the ScreenCast portal for a
**virtual** source instead, and the portal creates the output (`Virtual-…`). KWin removes it
when smon closes the portal session.

- The portal decides the size. Plasma **6.8+** lets you choose a resolution in the share
  dialog; earlier versions always use 1920x1080. `-w`/`-h` don't apply, and smon warns when
  the size differs from them. The refresh rate is 60 Hz; smon still streams at `-fps`.
- The portal names the output (`mapping_id`) from 6.8. On earlier versions smon finds it as the
  output that appeared after the request.

### Placement on KDE Plasma

With no `-pos`, KWin places the output. `-pos right|left|above|below` moves it next to your
other monitors with `kscreen-doctor` once it exists. You can also drag it in *System Settings →
Display & Monitor*.

### Share picker on KDE Plasma

The first run of extend mode shows a dialog to create a virtual display for smon. Accept it,
and keep **Allow the application to do this without asking next time** ticked: from Plasma 6.3
later runs then skip the dialog and recreate the display at the same size. In mirror mode pick
the screen to duplicate.

### Touch input on KDE Plasma

`-input` maps only smon's touchscreen, through KWin's input-device D-Bus interface
(`outputName`). KWin saves that mapping in `~/.config/kcminputrc`. smon removes its entry on exit.

## Other compositors

Support for more Wayland compositors is planned. If you want to add one, see
[Adding a compositor](architecture.md#adding-a-compositor).
