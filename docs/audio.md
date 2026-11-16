# Audio

While smon streams, a new sound output called **Second Monitor (smon)** appears in your sound
settings (pavucontrol, your desktop's volume menu, …).

smon never switches to it on its own. Pick it as the output, globally or per app, and that
sound plays on the receiver instead of on your machine. Switch back any time.

When smon exits the device goes away, and its streams fall back to your default output.

> **Tip:** if you make *Second Monitor (smon)* the **default** output, WirePlumber remembers that. On
> the next run it becomes the default again by itself as soon as it appears. Choose another
> output to undo that.

## Codecs per receiver

| Receiver | Audio |
| --- | --- |
| Browser (`web`), webOS | Opus over WebRTC |
| USB | Opus over the WebSocket, decoded with WebCodecs |
| Miracast | AAC in the MPEG-TS, when the sink advertises AAC |
| AirPlay | None (AirPlay audio is a separate encrypted stream smon doesn't implement) |

## Starting sound in the browser

Browsers keep a page silent until the user interacts with it, so **tap the page once**. A "tap
for sound" hint shows until then. On a TV, press **OK** on the remote.

## Turning it off

`-audio=false` streams video only, and no sound output is created. If `opusenc` or `pulsesrc`
is missing, smon warns and streams video only.

Browser audio and video are not lip-synced to each other. Both are low-latency, so the drift is
small.
