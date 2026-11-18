# macOS desktop adapter

Build with Darwin cgo, the Apple SDK (AppKit, ApplicationServices,
AVFoundation, Carbon and Security), and macOS 14.2+ for the system audio tap.
The adapter is linked into `audiomemo`, not a permission broker or subprocess.
Install `Info.plist` as `AudioMemo.app/Contents/Info.plist` and the **real Mach-O
executable** as `AudioMemo.app/Contents/MacOS/audiomemo`. Start desktop mode in
the logged-in user's Aqua session, never from a system LaunchDaemon. Keep the
headless CLI separate. On the desktop entry path, lock the initial Go OS thread
before starting goroutines and run the adapter there. Start the controller on
workers; shutdown of the adapter and daemon should share a lifetime.

The menu mirrors the controller. Start Meeting uses its configured default;
Choose Meeting Group accepts a configured group name. Quick Scribe defaults to
copy. Control–Option–Space uses a registered system hotkey (no event monitor);
if another app owns the shortcut, the menu remains usable and shows a warning.
The caption is click-through and never key. For a shortcut it uses the focused
window's display when Accessibility can identify it, otherwise the pointer's
display. The menu uses the pointer's display. Display removal falls back to the
main display. The plate shows the last two visual lines and hides when capture
stops, on cancel, and on lock. If the controller sends a meeting preview, the
same plate shows it as a live draft; it never goes to the clipboard.

Opt-in paste only posts Command–V to the process whose focused window and
editable field still match the original Accessibility snapshot. Unknown fields,
secure fields, app switches, a held modifier or missing Accessibility permission
fall back to copy. A locked/inactive session refuses both. The copy is intentional
even after paste: AudioMemo does not restore the previous clipboard or overwrite
newer clipboard contents as part of restoration. A clipboard manager may keep
copies. AX focus and event delivery cannot be made atomic by macOS; the adapter
never activates the old target, and the OS does not acknowledge actual insertion.
Paste falls back to copy if the active layout does not map the macOS ANSI V
virtual key to Command–V.

## Permissions and identity

- **Microphone:** request only on explicit start when `AVFoundation` says the
  user has not decided. If denied, use System Settings → Privacy & Security →
  Microphone. A device-managed restriction is reported separately.
- **System audio:** the actual Core Audio tap opens audio and may trigger the OS
  prompt. A tap permission error points to Privacy & Security → Screen & System
  Audio Recording. There is no public system-audio-only preflight, so an
  unrelated tap failure is not reported as permission denied.
- **Accessibility:** needed only to verify the target and post paste. AudioMemo
  does not request Input Monitoring, use Apple Events, or automatically prompt
  for Accessibility. Enable it in Privacy & Security → Accessibility if you want
  opt-in paste. Copy and menu/shortcut controls do not require it.

A granted parent does not prove that a child recorder can open a microphone.
Check capture permission and report ingress errors at the actual recording
boundary; packaging should test TCC attribution for the app and any capture
subprocesses, not the UI alone. The native system audio tap runs in process.

The app declares `com.joegoldin.audiomemo`. A future stable TCC deployment should
use a consistent app location and a stable signing identity/designated requirement
across upgrades. A stable bundle ID or profile symlink alone is not sufficient:
a bare, unsigned or ad hoc Nix-store binary can lose grants when the store path or
code changes. Development packaging must not promise TCC persistence or silently
reset or renew grants. No signing identity or entitlements are provisioned here.
