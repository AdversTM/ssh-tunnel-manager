# Testing

SSH Tunnel Manager is tested in four layers. Each one runs against real `ssh.exe`, `plink.exe` and a real `sshd` where possible, not against mocks.

| Layer | Command | Needs |
|---|---|---|
| Unit | `go test ./...` | nothing (runs in CI) |
| Frontend types | `cd frontend; bun run check` | Bun (runs in CI) |
| Integration | `go test -tags integration ./internal/...` | Docker test server, test keys |
| End-to-end (real exe) | `python test/qa_*.py` | Docker test server, Python + Playwright, a built exe |
| Manual | [qa-tray.md](qa-tray.md) | a person at a Windows desktop |

## Test environment

```powershell
# 1. test client keys (OpenSSH + PuTTY .ppk, with and without passphrase) -> test/keys (gitignored)
./test/sshd/gen-keys.ps1

# 2. throwaway sshd on 127.0.0.1:2222 with users pwuser/testpass and keyuser (key auth)
docker compose -f test/sshd/compose.yaml up -d --build

# 3. clean Debian + OpenSSH image for the server setup script tests
docker build -t stm-serverkit-test test/serverkit

# 4. end-to-end tooling
pip install playwright
```

`test/sshd/sim.ps1` simulates server-side failures:

| Action | Effect |
|---|---|
| `kill-sessions` | kills all sshd session processes (server-side drop) |
| `net-off` / `net-on` | disconnects or reconnects the container network |
| `occupy-port <p>` / `free-port <p>` | makes a remote forward port busy |
| `rotate-hostkey` | regenerates the server host key (MITM simulation) |
| `restart`, `listeners`, `fingerprint` | helpers |

Plink tests use PuTTY 0.78 or newer from `C:\Program Files\PuTTY`. Passphrase-protected `.ppk` keys work only through Pageant, which is a plink limitation.

## End-to-end scripts

| Script | Build | What it drives |
|---|---|---|
| `test/qa_gui.py` | QA (`wails3 build EXTRA_TAGS=qa`) | The window over the WebView2 DevTools port: add/edit/validate tunnels, host key dialogs, logs, settings, server setup, delete, kill-the-app cleanup. |
| `test/qa_system.py` | QA | Autostart registry value from the settings dialog; drop/restore toasts (aggregation, notifications off) counted from `app.log`. |
| `test/qa_release.py` | release (`wails3 build`) | The portable exe from an empty folder with a fresh `%APPDATA%`: GUI subsystem, version info, data dir, single instance, auto-connect key tunnel carrying HTTP. |
| `test/qa_installer.py` | release + `wails3 task windows:package VERSION=x.y.z` | Silent per-user install, upgrade refused while the exe is in use, uninstall cleanup, and autostart value handling. |
| `test/leak-check.ps1` | any | Fails if a test secret shows up in `ssh`/`plink` command lines or in the data directory. |

The QA build only adds `--remote-debugging-port` for WebView2 when `STM_QA_CDP_PORT` is set (see `qa_on.go`). Release builds never contain it.

`qa_gui.py` and the changed-key integration test rotate the test server's host key, so tests read the fingerprint at runtime. Never hard-code it.

## Acceptance criteria

The `AC-n` labels in tests and scripts refer to this list.

| # | Criterion | Covered by |
|---|---|---|
| AC-1 | The exe starts to the tray with no console window. A second start leaves one process and shows the existing window. | `qa_release.py`, [qa-tray.md](qa-tray.md) |
| AC-2 | A `-R` tunnel (OpenSSH, key) to the test sshd comes up, and the container reaches a local server through it. | supervisor `TestIntegrationRemoteForward`, `qa_release.py` |
| AC-3 | The same for plink (+ Pageant) and for `-L`. | supervisor `TestIntegrationLocalForward`, engine plink tests |
| AC-4 | A killed `ssh.exe` is replaced within 5 s, with the log line `reconnect attempt 1`, and the tunnel is Up again. | `TestIntegrationKilledClientReconnects` |
| AC-5 | A network outage gives Backoff with growing delays (capped at 60 s). Recovery gives Up and a "restored" toast. | `TestIntegrationNetworkOutage`, `qa_system.py` |
| AC-6 | A busy remote port gives the status "port busy" and backoff, without hanging. | `TestIntegrationRemotePortBusy` |
| AC-7 | A wrong password or key gives Error, no retry, and a readable message. | `TestIntegrationAuthFailure`, `TestIntegrationPlinkWrongPassword` |
| AC-8 | An unknown host key prompts with its fingerprint (reject → Error, accept → Up + saved). A changed key gives Error and a MITM warning with the old and new fingerprints. | engine host key tests, `qa_gui.py` |
| AC-9 | The password never appears in the config, logs, process command lines, or leftover files. | engine integration tests (leak check), `leak-check.ps1`, `qa_gui.py` |
| AC-10 | Killing the app leaves no `ssh.exe`/`plink.exe` after 2 s (Job Object). | `qa_gui.py`, [qa-tray.md](qa-tray.md) |
| AC-11 | Autostart on writes the `HKCU\…\Run` value; off removes it. Auto-connect tunnels start after sign-in. | `qa_system.py`, [qa-tray.md](qa-tray.md) |
| AC-12 | After sleep/wake, active tunnels restart. | manager `TestRestartActiveAfterResume`, [qa-tray.md](qa-tray.md) |
| AC-13 | A corrupt config file leads to a start with an error message and a backup of the file, with no crash. | config and manager unit tests |

## Server setup scripts

`go test ./internal/serverkit` compares the generated instructions, scripts, and Caddy blocks with golden files in `internal/serverkit/testdata`. After an intended change, regenerate them with `go test ./internal/serverkit -update` and review the diff.

`go test -tags integration ./internal/serverkit` runs each generated script twice inside the clean Debian image and checks:

- `sshd -t` passes and no entries are duplicated;
- forwarding outside `permitlisten` is refused;
- a second tunnel for the same user extends the allowed list;
- `caddy validate` accepts the Caddy block.
