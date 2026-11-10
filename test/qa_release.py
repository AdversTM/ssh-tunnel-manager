"""Release QA: the release exe copied to an empty folder, run with a fresh
%APPDATA% (stands in for a clean user profile; Windows Sandbox is not available
on Windows Home).

Checks: GUI subsystem (no console), version info, data dir created under
%APPDATA%\\SshTunnelManager with the WebView2 data inside it, single instance
(AC-1), and an auto-connect key tunnel -R that the container reaches over HTTP (AC-2).

Needs: release build (wails3 build), Docker container stm-sshd on 127.0.0.1:2222.
Run: python test/qa_release.py
"""

import http.server
import json
import secrets
import shutil
import struct
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BUILT = ROOT / "bin" / "ssh-tunnel-manager.exe"
WORK = Path(tempfile.gettempdir()) / "stm-release-qa"
APPDATA = WORK / "appdata"
EXE = WORK / "portable" / "ssh-tunnel-manager.exe"
DATA = APPDATA / "SshTunnelManager"
KEY = ROOT / "test" / "keys" / "client_test"
REMOTE_PORT, HTTP_PORT = 18600, 18601

results: list[tuple[str, bool]] = []


def step(name: str, ok: bool, detail: str = "") -> None:
    results.append((name, ok))
    print(("PASS " if ok else "FAIL ") + name + (f" - {detail}" if detail else ""), flush=True)


def sh(*args: str) -> str:
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=60).stdout


def app_pids() -> list[str]:
    ps = (f"Get-CimInstance Win32_Process -Filter \"Name='ssh-tunnel-manager.exe'\" | "
          f"Where-Object {{ $_.ExecutablePath -eq '{EXE}' }} | ForEach-Object {{ $_.ProcessId }}")
    return sh("powershell", "-NoProfile", "-Command", ps).split()


def start() -> subprocess.Popen:
    env = dict(__import__("os").environ, APPDATA=str(APPDATA))
    return subprocess.Popen([str(EXE)], env=env, cwd=EXE.parent)


def wait_for(cond, timeout: float) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if cond():
            return True
        time.sleep(0.3)
    return False


def gui_subsystem(path: Path) -> bool:
    b = path.read_bytes()
    pe = struct.unpack_from("<I", b, 0x3C)[0]
    return struct.unpack_from("<H", b, pe + 24 + 68)[0] == 2


def tunnel_config() -> dict:
    return {
        "version": 1,
        "settings": {"notifications": False},
        "tunnels": [{
            "id": "release-qa", "name": "release-qa", "backend": "openssh",
            "host": "127.0.0.1", "port": 2222, "user": "keyuser",
            "auth": {"method": "key", "keyPath": str(KEY)},
            "forward": {"type": "R", "bindAddress": "127.0.0.1", "bindPort": REMOTE_PORT,
                        "targetHost": "127.0.0.1", "targetPort": HTTP_PORT},
            "autoConnect": True, "hostKey": {}, "server": {"mode": "none"},
        }],
    }


def run(token: str) -> None:
    step("exe is a GUI-subsystem binary (no console window)", gui_subsystem(EXE))
    version = sh("powershell", "-NoProfile", "-Command", f"(Get-Item '{EXE}').VersionInfo.ProductVersion").strip()
    step("version info embedded", version == "0.1.0", version)

    app = start()
    step("first start creates %APPDATA%\\SshTunnelManager with app.log",
         wait_for(lambda: (DATA / "logs" / "app.log").exists(), 20))
    step("WebView2 data kept inside the data dir",
         wait_for(lambda: (DATA / "webview").is_dir(), 20) and not (APPDATA / "ssh-tunnel-manager.exe").exists())
    second = start()
    second.wait(timeout=20)
    time.sleep(1)
    pids = app_pids()
    step("AC-1 second start exits, one instance remains", app.poll() is None and len(pids) == 1, f"pids={pids}")
    subprocess.run(["taskkill", "/F", "/PID", str(app.pid)], capture_output=True)
    app.wait(timeout=10)

    (DATA / "config.json").write_text(json.dumps(tunnel_config(), indent=2), encoding="utf-8")
    host_key = " ".join(sh("docker", "exec", "stm-sshd", "cat", "/etc/ssh/ssh_host_ed25519_key.pub").split()[:2])
    (DATA / "known_hosts").write_text(f"[127.0.0.1]:2222 {host_key}\n", encoding="utf-8")
    app = start()
    log = DATA / "logs" / "ssh-tunnel-manager.log"
    up = wait_for(lambda: log.exists() and "connected (pid" in log.read_text(encoding="utf-8", errors="replace"), 30)
    step("auto-connect key tunnel comes up after start", up)
    got = subprocess.run(["docker", "exec", "stm-sshd", "wget", "-qO-", f"http://127.0.0.1:{REMOTE_PORT}/token.txt"],
                         capture_output=True, text=True, timeout=30).stdout.strip()
    step("AC-2 container reaches the local HTTP server through -R", got == token, got)

    subprocess.run(["taskkill", "/F", "/PID", str(app.pid)], capture_output=True)
    app.wait(timeout=10)


def main() -> int:
    if not BUILT.exists():
        print(f"missing {BUILT}; run wails3 build first")
        return 2
    shutil.rmtree(WORK, ignore_errors=True)
    EXE.parent.mkdir(parents=True)
    APPDATA.mkdir()
    shutil.copy2(BUILT, EXE)
    token = secrets.token_hex(8)
    site = WORK / "site"
    site.mkdir()
    (site / "token.txt").write_text(token, encoding="utf-8")
    class Handler(http.server.SimpleHTTPRequestHandler):
        def __init__(self, *a):
            super().__init__(*a, directory=str(site))

        def log_message(self, format, *args):
            pass

    httpd = http.server.ThreadingHTTPServer(("127.0.0.1", HTTP_PORT), Handler)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    try:
        run(token)
    finally:
        httpd.shutdown()
        for pid in app_pids():
            subprocess.run(["taskkill", "/F", "/PID", pid], capture_output=True)
        time.sleep(1)
        shutil.rmtree(WORK, ignore_errors=True)
    failed = [r for r in results if not r[1]]
    print(f"\n{len(results) - len(failed)}/{len(results)} steps passed")
    return 1 if failed or not results else 0


if __name__ == "__main__":
    sys.exit(main())
