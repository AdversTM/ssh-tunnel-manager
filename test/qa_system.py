"""System QA: autostart registry entry (AC-11) and drop/restore toasts.

Needs: a QA build (wails3 build EXTRA_TAGS=qa), Docker container stm-sshd on
127.0.0.1:2222, Python Playwright. Toggles the container network (sim.ps1).
Toasts are counted from the "toast:" lines the app writes to app.log.
Run: python test/qa_system.py
"""

import shutil
import subprocess
import sys
import tempfile
import time
import winreg
from pathlib import Path

from playwright.sync_api import expect, sync_playwright

import qa_gui as g

g.DATA = Path(tempfile.gettempdir()) / "stm-system-qa"
RUN_KEY = r"Software\Microsoft\Windows\CurrentVersion\Run"
RUN_NAME = "SshTunnelManager"
SIM = str(g.ROOT / "test" / "sshd" / "sim.ps1")


def run_value() -> str | None:
    try:
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, RUN_KEY) as k:
            return winreg.QueryValueEx(k, RUN_NAME)[0]
    except FileNotFoundError:
        return None


def restore_run_value(original: str | None) -> None:
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER, RUN_KEY, 0, winreg.KEY_SET_VALUE) as k:
        if original is None:
            try:
                winreg.DeleteValue(k, RUN_NAME)
            except FileNotFoundError:
                pass
        else:
            winreg.SetValueEx(k, RUN_NAME, 0, winreg.REG_SZ, original)


def sim(action: str) -> None:
    subprocess.run(["powershell", "-NoProfile", "-File", SIM, action], check=True, capture_output=True, timeout=60)


def toasts() -> list[str]:
    log = g.DATA / "logs" / "app.log"
    if not log.exists():
        return []
    return [l.split("toast: ", 1)[1].strip() for l in log.read_text(encoding="utf-8").splitlines() if "toast: " in l]


def new_toasts(before: int, want: int, timeout: float = 15.0) -> list[str]:
    deadline = time.time() + timeout
    while time.time() < deadline and len(toasts()) - before < want:
        time.sleep(0.2)
    time.sleep(3)  # batch window + margin, so an extra (wrong) toast would show up too
    return toasts()[before:]


def save_settings(page, *, autostart=None, notifications=None):
    page.get_by_test_id("nav-settings").click()
    box = page.get_by_test_id("settings-autostart")
    expect(box).to_be_enabled()
    if autostart is not None:
        box.set_checked(autostart)
    if notifications is not None:
        page.get_by_test_id("settings-notifications").set_checked(notifications)
    page.get_by_test_id("settings-save").click()
    expect(page.get_by_test_id("settings-save")).to_be_hidden()


def add_and_connect(page, refs, name, port):
    g.open_new_form(page)
    g.fill_tunnel(page, name=name, bind_port=port)
    page.get_by_test_id("form-save").click()
    expect(g.row(page, name)).to_be_visible()
    refs.append("ssh-tunnel-manager:" + g.tunnel_id(name))
    g.row(page, name).get_by_test_id("tunnel-toggle").click()
    dialog = page.get_by_test_id("hostkey-dialog")
    deadline = time.time() + 20
    while time.time() < deadline:
        if dialog.is_visible():
            dialog.get_by_test_id("hostkey-accept").click()
        if g.row(page, name).get_attribute("data-state") == "up":
            return
        time.sleep(0.2)
    g.step(f"{name} connects", False)


def outage(page, names) -> None:
    """Drop every tunnel and keep the server unreachable longer than the toast batch window."""
    sim("kill-sessions")
    sim("net-off")
    for n in names:
        expect(g.row(page, n)).not_to_have_attribute("data-state", "up", timeout=60_000)
    time.sleep(3)
    sim("net-on")
    for n in names:
        g.wait_state(page, n, "up", timeout=60_000)


def run_steps(page, refs):
    # AC-11 autostart on/off through the settings dialog
    save_settings(page, autostart=True)
    value = run_value() or ""
    g.step("autostart on -> Run value points at the exe with -data",
           str(g.EXE) in value and "-data" in value and str(g.DATA) in value, value)
    save_settings(page, autostart=False)
    g.step("autostart off -> Run value removed", run_value() is None)

    names = ["qa-a", "qa-b", "qa-c"]
    for i, n in enumerate(names):
        add_and_connect(page, refs, n, str(18320 + i))
    g.step("3 tunnels up", True)

    before = len(toasts())
    outage(page, names)
    got = new_toasts(before, 2)
    drops = [t for t in got if "disconnected" in t]
    restores = [t for t in got if "restored" in t or "connected again" in t]
    g.step("3 dropped at once -> 1 aggregated drop toast",
           len(drops) == 1 and drops[0].startswith("3 tunnels disconnected"), " || ".join(got))
    g.step("restore toast(s) name every tunnel",
           len(restores) >= 1 and all(any(n in r for r in restores) for n in names), f"{len(restores)} restore toast(s)")

    before = len(toasts())
    for n in names[1:]:
        g.row(page, n).get_by_test_id("tunnel-toggle").click()
        g.wait_state(page, n, "stopped")
    got = new_toasts(before, 0, timeout=0)
    g.step("manual stop -> no toast", got == [], " || ".join(got))

    before = len(toasts())
    outage(page, names[:1])
    got = new_toasts(before, 2)
    g.step("single tunnel net-off/net-on -> exactly 2 toasts",
           len(got) == 2 and got[0].startswith("Tunnel disconnected") and got[1].startswith("Tunnel restored"),
           " || ".join(got))

    save_settings(page, notifications=False)
    before = len(toasts())
    outage(page, names[:1])
    got = new_toasts(before, 0, timeout=0)
    g.step("notifications off -> 0 toasts", got == [], " || ".join(got))

    log = (g.DATA / "logs" / "app.log").read_text(encoding="utf-8")
    g.step("no 'toast failed' in app.log", "toast failed" not in log)


def main() -> int:
    if not g.EXE.exists():
        print(f"missing {g.EXE}; run wails3 build EXTRA_TAGS=qa first")
        return 2
    shutil.rmtree(g.DATA, ignore_errors=True)
    original_run = run_value()
    app = g.start_app()
    refs: list[str] = []
    try:
        with sync_playwright() as pw:
            browser, page = g.connect(pw)
            page.set_default_timeout(10_000)
            run_steps(page, refs)
            browser.close()
    except AssertionError:
        pass
    finally:
        if app.poll() is None:
            app.kill()
        subprocess.run(["powershell", "-NoProfile", "-File", SIM, "net-on"], capture_output=True, timeout=60)
        restore_run_value(original_run)
        for ref in refs:
            if g.credential_exists(ref):
                subprocess.run(["cmdkey", f"/delete:{ref}"], capture_output=True)
    failed = [r for r in g.results if not r[1]]
    print(f"\n{len(g.results) - len(failed)}/{len(g.results)} steps passed")
    return 1 if failed or not g.results else 0


if __name__ == "__main__":
    sys.exit(main())
