# Plasma / Wayland helper

`audiomemo daemon` can run without this helper. On a graphical session, its Linux
adapter launches `audiomemo-desktop-linux` from `../libexec` relative to the Go
binary, or from an absolute `AUDIOMEMO_DESKTOP_LINUX_HELPER` override. Launch the
daemon with the graphical user's Wayland socket, runtime directory and session
bus in its environment. Nothing here registers a shortcut or starts at login.

## KDE shortcut and menu

In Plasma System Settings → Keyboard → Shortcuts, choose **Add New → Command or
Script**. Set the command to the absolute path to the installed binary followed
by `dictate toggle`, then assign a regular chord and resolve any KDE conflict.
This uses the same control socket as `audiomemo dictate toggle` in a terminal.
It does not start the daemon, open a meeting, or need an input-device grant.
For Us and Them captions, add a second shortcut to the same binary with
`dictate toggle --both`. Use an extra modifier such as Alt if the chord is free.
Configure `desktop.dictation_group` with both roles; if unset, the shortcut uses
`desktop.meeting_device`. Press either binding during Quick Scribe to stop it.
Reload the shortcut in KDE if you used a path that changes on upgrade.

The Plasma StatusNotifierItem menu includes capture status and elapsed time,
start/stop for both capture modes, Copy last result and Quit. The icon and elapsed
status track active capture, not background finalization. Starting a capture
from the menu requires a registered tray host and an unlocked session. Losing
the tray does not stop a capture or disable headless controls. Quit asks the
daemon for its normal recoverable shutdown.

## Preview and delivery limits

- On KWin, the caption is a layer-shell surface with no input region or keyboard
  interactivity. It uses a bottom margin and does not reserve screen space. Its
  logical-pixel size fits the smallest advertised output. KWin chooses the active
  output when capture is admitted, **not** the screen at the precise
  time of a separate CLI shortcut event. No pointer polling, X11 window hack or
  focusable popup is used. Stable text and tentative revisions are styled
  separately; the latest session-matching snapshot replaces the previous one.
- With no usable layer-shell plugin/protocol, captions are disabled, not moved
  into a regular window. On session lock or session change, the overlay disappears. Quick Scribe
  stays visible through final transcription with a progress animation, then
  reports the copy result briefly. During capture, the input meters follow
  the audio, with separate Us and Them cards for a mixed-role group. An overlay
  dismissed with its output or by lock is not remapped on
  another display for the same capture.
- When available, clipboard delivery uses `ext_data_control_manager_v1` on the
  unambiguous Wayland seat. Copy changes only the regular clipboard. The helper
  serves the latest owned selection until replaced or until it exits. It neither
  reads nor restores a previous selection. Clipboard managers can retain copies
  even after replacement or quit.
- Automatic paste is unavailable on Linux, even if requested. The portal's
  input grant does not prove that the original application still has focus.
  **Copy, then paste manually.** No input is synthesized and there is no portal
  permission prompt. If copying fails (including lock, ambiguous seat, absent
  data-control, or compositor revocation), the adapter does not try to focus a
  window or retry the delivery. Copy last result can be retried from an available
  desktop while the daemon still has the result.

The helper and daemon exchange bounded JSON over private process pipes. Caption
and clipboard text are not passed in argv, written to disk, or echoed to logs.

## Build and check

Build inputs: Qt6 Widgets/DBus, Qt Wayland, LayerShellQt, KF6
KStatusNotifierItem, `wayland-client`, and `wayland-protocols` providing the
staging ext-data-control XML. Build tools: CMake, pkg-config and `wayland-scanner`.
On Nix, use the corresponding `kdePackages` and Wayland packages from the project
pin. Wrap the installed **libexec** executable with `wrapQtApp`; plugin paths
must include the Qt Wayland platform and LayerShellQt shell integration. No Qt
libraries are linked into the Go binary.

```fish
cmake -S desktop/linux -B build-linux-desktop -DAUDIOMEMO_DESKTOP_TESTS=ON
cmake --build build-linux-desktop
ctest --test-dir build-linux-desktop --output-on-failure
```

Native checks additionally need Python 3 and `wayland-server`. The tests use an
offscreen helper or a synthetic Wayland compositor over a socketpair. An
offscreen rendering check also verifies the tray waveform and caption cards. The Go
bridge tests use a fake helper. None verifies a real KDE shortcut, tray,
compositor placement, click-through, bottom-panel clearance, mixed-scale
outputs, screen-lock notifications, or Klipper behavior. Those remain native
acceptance checks. Exact key-event output selection and safe automatic paste are
not guaranteed by this adapter.
