# AwayTrace

AwayTrace is a Windows system-tray screen recorder for the current user. It
waits for a configurable grace period, then records while there is mouse or
keyboard activity. The defaults are a 30-second grace period and a 10-second
idle timeout. It records neither audio nor individual keystrokes.

Left-click the tray icon to stop and save the current session, or to start
capture after the grace period. Right-click for status, Settings, the recording
folder, About, and Quit. About shows the version and a link to the repository.
Locking or signing out ends the recording; unlocking re-arms
capture. Idle periods are omitted without creating a separate file.

Settings include independent checkboxes for the bottom-left timestamp and the
brief skipped-time card. Both are enabled by default. The timestamp supports
24-hour (default) or 12-hour time. Changing video or overlay settings applies
to the next recording session.

The settings window follows the Windows app light/dark preference when it is
opened and leaves Windows high-contrast themes under system control. Some
native drop-down controls use the system's light appearance.

Video settings provide H.264 (default), H.265, and AV1 software encoding;
constant quality; encoding speed; and an optional aspect-preserving resolution
limit. The default is H.264, quality 28, Balanced speed, and original
resolution. Lower quality numbers generally give more detail and larger files;
codec quality scales are not directly comparable. Encoding uses up to ten
seconds between keyframes with low-latency settings. Smaller files can require
more CPU; playback compatibility depends on the codec and player.

Recordings are local MKV files under `Videos\AwayTrace` by default. The app
responds to Windows shutdown promptly and attempts to finalize the file; an
interruption or power loss can lose recent frames. The included FFmpeg is
verified and extracted to the current user's AwayTrace data folder on first
run. Existing Python-version settings and recordings are retained.

The tray menu includes **Check for updates**. Automatic update checks are on by
default; automatic installation is a separate setting and is off by default.
Enabling it downloads newer stable releases and installs them only when no
recording is active and Windows is unlocked. Downloads are checked against the
size and SHA-256 digest provided by the project's GitHub release before they
are run. This integrity check relies on the GitHub release and HTTPS; it is not
an independent digital signature. The update service requires the repository
and its release assets to be publicly accessible.

## Install

Run `AwayTrace-Setup.exe` on 64-bit Windows. Installation is per-user and
does not require administrator rights. Close AwayTrace before upgrading. The
uninstaller leaves settings and recordings in place. The executable and
installer are unsigned.

## Build from source

Install Go 1.25.10 or later. Obtain the gyan.dev **FFmpeg 9.0.2 essentials**
Windows build and identify its `ffmpeg.exe`. The pinned executable's SHA-256 is
`3256173f3f8bffd7df12227c68adf68025edb1832273a9530688a7bb1ed8edec`.
The build rejects a different binary. See [gyan.dev](https://www.gyan.dev/ffmpeg/builds/)
for the build and source, and [THIRD-PARTY.md](THIRD-PARTY.md) for licenses.

```powershell
.\build.ps1 -FFmpegPath 'C:\path\to\ffmpeg.exe'
```

This compresses and embeds the verified encoder and writes
`dist\AwayTrace.exe`. Subsequent builds reuse the compressed file. The checked-in
`.syso` contains the application icon and Windows manifest; regenerate it from
`awaytrace.exe.manifest` and `assets\awaytrace.ico` with `rsrc` when those
resources change. To build the installer, compile `AwayTrace.iss` with Inno
Setup 7.

## Tests

Set `AWAYTRACE_TEST_FFMPEG` to the pinned `ffmpeg.exe`, prepare the embedded
asset with the build script, then run `go test ./...`. Tests use generated
frames rather than capturing the desktop. They cover configuration migration,
codec outputs, overlay choices, idle gaps, simulated shutdown, full-resolution
finalization, and interrupted recordings. The opt-in native UI test sends
session messages to a separate test window; it does not lock or shut down the
computer: `$env:AWAYTRACE_NATIVE_UI='1'; go test -run TestNativeSessionMessages`.
An opt-in desktop smoke test captures four frames to a temporary recording and
removes it afterward: `$env:AWAYTRACE_DESKTOP_TEST='1'; go test -run TestDesktopRecordingSmoke`.
Set `AWAYTRACE_TEST_GO` to the Go executable to run the update-helper integration
test, which uses harmless temporary fixture programs instead of a real install.
