# Performance and limitations

- [Encoder selection](#encoder-selection)
- [Keyframes](#keyframes)
- [Adaptive bitrate](#adaptive-bitrate)
- [Known limitations](#known-limitations)

## Encoder selection

At startup smon times every H.264 encoder (each GPU's VA-API encoder, and x264) at the output
size and uses the fastest. The time per frame is latency the encoder adds, and older GPUs can
be slower than x264. The log prints the timings.

- The winner is saved in `~/.cache/smon/encoder.json` per output size, so later runs start
  without the benchmark.
- The benchmark runs again when the set of encoders changes (new GPU or plugins), or when the
  capture pipeline fails within its first 10 seconds.
- Delete the file to force a new benchmark, for example after a driver update.
- `SMON_ENCODER=x264enc` (or for example `varenderD129h264enc`) skips the benchmark and uses
  that encoder.

## Keyframes

- Browser viewers get a keyframe on demand: when they connect, when they report loss (PLI/FIR),
  and when a USB viewer falls behind. At most one every 500 ms, with a periodic one every 5 s.
- AirPlay and Miracast receivers can't ask for one, so they get a 1-second GOP.

## Adaptive bitrate

WebRTC viewers adapt the bitrate:

- On congestion (GCC delay overuse, or more than 10% loss reported via transport-cc), the encoder
  drops 30%, down to a tenth of `-kbps`.
- After 3 s without congestion, it climbs 15% every 2 s back to `-kbps`.
- The encoder is shared, so the most congested viewer sets the quality for everyone, USB
  viewers included.
- Each change is logged. With a VA-API encoder each change also costs one keyframe.
- `SMON_ABR=off` keeps the bitrate fixed at `-kbps`.

## Known limitations

- **AirPlay works only with open receivers** (UxPlay, RPiPlay). Licensed AirPlay 2 receivers
  (Apple TV, LG and Samsung TVs) need HomeKit pairing and real FairPlay, and reject smon at
  pair-setup (403).
- **AirPlay mirroring carries no audio.** AirPlay audio is a separate encrypted stream that
  smon doesn't implement.
- **Miracast** negotiates 1920x1080p30 when the sink supports it, otherwise 1280x720p30. AAC
  audio is sent when the sink advertises it.
- **Browser audio and video are not lip-synced** to each other. Both are low-latency, so the
  drift is small.
- **Touch input** is touch only (no keyboard), and only in the browser viewer (`web`, `usb`).
- **umbriel touch mapping** applies to every touchscreen while smon runs with `-input`.
