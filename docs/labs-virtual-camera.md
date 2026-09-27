# Labs: virtual camera broadcaster

!!! danger "Experimental feature. Review Discord's rules before use."
    The virtual camera broadcaster is an experimental, unsupported Labs feature. It is off by default and never starts on its own.

    Before enabling or using it, independently review Discord's current [Terms of Service](https://discord.com/terms), [Community Guidelines](https://discord.com/guidelines), [Developer Terms of Service](https://discord.com/developers/docs/policies-and-agreements/developer-terms-of-service) and [Developer Policy](https://discord.com/developers/docs/policies-and-agreements/developer-policy), and the copyright rules that apply to you and your audience. ViewDock has not established whether a particular use complies with Discord's rules, and this feature is not approved or endorsed by Discord. You are responsible for how you use it.

## What it does

The broadcaster renders a watch party, or a single movie or episode, in a separate FFmpeg process on your ViewDock server and sends the picture to one of these outputs:

| Mode | Output | Audio |
|------|--------|-------|
| RTMP / RTMPS | A receiver you control, for example OBS or a media server | Yes |
| SRT | An SRT receiver you control | Yes |
| Virtual camera | A Linux `v4l2loopback` camera device on the server | No, video only |

A person then shares that camera, or the receiver's window or virtual camera, from their own Discord desktop app, the same way they would share any other camera or window.

When the source is a watch party, the broadcast follows the party's current title, position and pause state.

ViewDock never signs in to Discord as a user, never asks for or stores a Discord password or user token, and does not automate Discord user accounts. Discord's bot API does not support broadcasting video, so the [Discord bot](discord.md) is not involved.

## Requirements

- An administrator account.
- FFmpeg with the `libx264` and AAC encoders. The ViewDock Docker image includes a suitable build.
- For RTMP or RTMPS: FFmpeg with the FLV muxer and RTMP output. For SRT: FFmpeg built with libsrt.
- For the virtual camera mode: a Linux server with the `v4l2loopback` kernel module loaded and a camera device created by it. It is not available on other platforms; send RTMP or SRT to a receiver such as OBS on the broadcasting computer and use its virtual camera instead.
- The server master key, which ViewDock creates automatically, so the output URL (which usually contains a stream key) can be stored encrypted. See `VD_MASTER_KEY` in [Environment](environment.md).

The **This server** section of the Labs page checks each of these and explains what is missing for each mode.

### Virtual camera device

Load the driver on the host, for example:

```bash
sudo modprobe v4l2loopback video_nr=10 card_label="ViewDock" exclusive_caps=1
```

In Docker, pass the device to the container. With the installer's Compose project, add a `docker-compose.override.yml` next to `docker-compose.yml`:

```yaml
services:
  viewdock:
    devices:
      - /dev/video10:/dev/video10
```

Then run `docker compose up -d`. If you use the GPU profile, add the same `devices` entry to the `viewdock-gpu` service instead.

## Enable and configure

1. Open **Admin → Labs**.
2. Read the risk notice, tick the confirmation and press **Enable experimental broadcaster**. The acknowledgment is recorded in the audit log. If the notice changes in a later release, it must be accepted again.
3. Under **Output and source**, choose the mode:
    - RTMP / RTMPS or SRT: enter the receiver URL, for example `rtmp://127.0.0.1:1935/live/viewdock` or `srt://obs-host.example.com:9000?streamid=viewdock`. After saving, only the scheme, host and port are shown.
    - Virtual camera: enter the device, for example `/dev/video10`.
4. Choose the resolution (640×360, 854×480, 1280×720 or 1920×1080), frame rate (10 to 60), video and audio bitrates, and the FFmpeg thread limit.
5. Choose the source: an active watch party, or a single movie or episode.
6. Press **Save configuration**, then **Start**. The page shows the state, frame rate, audio level and a preview of the outgoing picture. Changes saved while a broadcast is running apply after you stop and start it.

To stop broadcasting, press **Stop**. **Disable and withdraw acknowledgment** stops any running broadcast and turns the feature off until the notice is accepted again.

## How it behaves

- The broadcaster is isolated from normal playback: it runs its own FFmpeg process with a lower CPU priority and a thread limit, and a failure never affects viewers.
- If FFmpeg exits, it restarts with a delay that grows from 2 seconds to 1 minute. After 5 failures within 10 minutes it stops in the `failed` state until an administrator starts it again. A run that lasts longer than a minute resets the count.
- A broadcast with no new frames for 20 seconds is reported as stalled.
- The state appears on **Admin → Resilience**: unconfigured until the notice is accepted, degraded while stalled or retrying, and down after a failure.
- Starting, stopping, configuration changes and acknowledgments are written to the audit log, with the output URL redacted.

## Limitations

- Experimental and unsupported: behaviour may change between releases.
- The virtual camera mode is Linux only and carries no audio.
- Broadcasting uses extra CPU, memory and network bandwidth on the server.
- Each server process has its own broadcaster. In a multi-server setup, start it on one control plane only.
- Output addresses are checked when saved and on every start. Loopback and private network addresses are allowed, because the usual receiver is on the same machine or network. Link-local, multicast, broadcast, unspecified and cloud metadata addresses are refused.

## Troubleshooting

| Symptom | What to check |
|---------|---------------|
| A mode is unavailable | Read the reason in **This server** and press **Check again** after fixing it |
| "The v4l2loopback driver is not loaded" | Load the module on the host with `modprobe`, as above |
| Driver loaded but no device visible | Pass the device to the container with `devices` (or `--device`) |
| Output URL cannot be saved | Check that the URL uses `rtmp://`, `rtmps://` or `srt://` and that the host is not a refused address |
| "No master key" | Restore `/config/master.key` or set `VD_MASTER_KEY` |
| State is `failed` | Check the last error on the page and **Admin → Logs** (category `labs`), fix the cause, then press **Start** |
| No picture in Discord | Confirm the receiver or camera shows the preview first, then select that camera or window in Discord |

## Privacy and copyright

- Only broadcast media you have the rights to share with your audience. Sharing ViewDock output on Discord does not change your obligations under copyright law or Discord's rules.
- Anyone who can see the Discord stream sees the broadcast, regardless of their ViewDock permissions, content ratings or library access.
- Stream keys in output URLs are stored encrypted and redacted from the interface, the audit log and FFmpeg log lines.
