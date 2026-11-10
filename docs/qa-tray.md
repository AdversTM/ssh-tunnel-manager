# Manual QA

Checks that need a person at a real Windows desktop: the tray icon and its menu cannot be automated, and neither can real sleep/wake or sign-in. Everything else is automated; see [testing.md](testing.md). Allow about 10 minutes.

## Setup

1. Docker test server running: `docker compose -f test/sshd/compose.yaml up -d` (`stm-sshd` on 127.0.0.1:2222).
2. Build: `wails3 build` → `bin\ssh-tunnel-manager.exe`.
3. Start with a throwaway data dir: `bin\ssh-tunnel-manager.exe -data $env:TEMP\stm-tray-qa`.
4. In the window, add two tunnels (OpenSSH, password auth):
   - `qa-ok`: user `pwuser`, password `testpass`, `-R 127.0.0.1:18400 → 127.0.0.1:9`
   - `qa-bad`: user `pwuser`, password `wrong`, `-R 127.0.0.1:18401 → 127.0.0.1:9`

## Tray

- [ ] No console window. A **grey** circle appears in the tray; it may sit in the `^` overflow, so drag it out. The tooltip reads `SSH Tunnel Manager - 0/2 up`.
- [ ] Left-click the icon → the window shows and gets focus. Left-click again → it hides. The close button (X) hides to the tray and the process keeps running.
- [ ] Right-click → the menu shows `qa-ok ▸`, `qa-bad ▸`, a separator, `Open manager`, `Connect all auto-connect`, `Disconnect all`, a separator, `Exit`.
- [ ] `qa-ok ▸` shows a greyed `Status: Stopped` and an unchecked `Connect`. Click `Connect` → the host key dialog pops up in the window → Trust. Within 2 s the icon turns **green**; reopening the submenu shows `Status: Up` and a checked `Disconnect`.
- [ ] The tooltip now reads `SSH Tunnel Manager - 1/2 up`.
- [ ] `qa-ok ▸ Show logs` → the window opens on the logs of `qa-ok`.
- [ ] Network drop: `test/sshd/sim.ps1 net-off` → the icon turns **yellow** within ~60 s (keepalive), and the submenu shows `Status: Reconnecting (attempt N)`. `sim.ps1 net-on` → back to **green**.
- [ ] `qa-bad ▸ Connect` → the icon turns **red**, the submenu shows `Status: Error: authentication failed`, and the checkbox reads `Connect` (no retrying).
- [ ] `Disconnect all` → `qa-ok` stops. The icon stays red while `qa-bad` is in error, and turns grey after you delete `qa-bad` in the window.
- [ ] Toggling a tunnel in the window updates the tray menu, and toggling it in the tray updates the window, within 2 s.
- [ ] Start the exe a second time → no second icon or process appears; the existing window comes to the front.
- [ ] `Exit` → the icon disappears, `Get-Process ssh-tunnel-manager` finds nothing, and no `ssh.exe` started by the app remains (AC-10).

## System integration

`test/qa_system.py` (QA build) automates the registry autostart check and toast counting. These checks need a real machine:

- [ ] **Toast visible**: with `qa-ok` up, run `sim.ps1 kill-sessions; sim.ps1 net-off`, wait ~5 s, then `sim.ps1 net-on` → a Windows toast `Tunnel disconnected`, then `Tunnel restored`. Clicking a toast opens the window.
- [ ] **Sleep/wake**: with `qa-ok` up, Start ▸ Power ▸ Sleep, then wake after at least 1 min → the `qa-ok` logs show `resume → restart` once, and the tunnel is **green** within ~15 s.
- [ ] **Adapter toggle**: with `qa-ok` up, disable the Wi-Fi/Ethernet adapter (Settings ▸ Network ▸ Change adapter options), then re-enable it → the logs show `network change → restart` once after the adapter is back. Starting or stopping Docker or WSL alone does **not** log it.
- [ ] **Autostart**: turn on Settings ▸ `Start with Windows` (with the default data dir), then sign out and in → the tray icon appears without the window, and auto-connect tunnels connect. Turn it off → `Get-ItemProperty HKCU:\Software\Microsoft\Windows\CurrentVersion\Run` has no `SshTunnelManager`.

## Cleanup

Delete both tunnels in the window before exiting (this removes their Credential Manager entries), then remove `%TEMP%\stm-tray-qa`.
