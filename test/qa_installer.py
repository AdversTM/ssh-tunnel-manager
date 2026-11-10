"""Installer QA: silent per-user install, upgrade blocked while the app runs,
uninstall cleanup, and autostart handling (only this installation's Run value is removed).

Needs: bin/ssh-tunnel-manager-<version>-setup.exe (wails3 task windows:package VERSION=<version>).
Run: python test/qa_installer.py
"""

import os
import subprocess
import sys
import time
import winreg
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SETUP = next(iter(sorted((ROOT / "bin").glob("ssh-tunnel-manager-*-setup.exe"))), None)
INSTDIR = Path(os.environ["LOCALAPPDATA"]) / "Programs" / "SSH Tunnel Manager"
EXE = INSTDIR / "ssh-tunnel-manager.exe"
SHORTCUT = Path(os.environ["APPDATA"]) / "Microsoft" / "Windows" / "Start Menu" / "Programs" / "SSH Tunnel Manager.lnk"
UNINST_KEY = r"Software\Microsoft\Windows\CurrentVersion\Uninstall\SshTunnelManager"
RUN_KEY = r"Software\Microsoft\Windows\CurrentVersion\Run"
RUN_NAME = "SshTunnelManager"

results: list[bool] = []


def step(name: str, ok: bool, detail: str = "") -> None:
    results.append(ok)
    print(("PASS " if ok else "FAIL ") + name + (f" - {detail}" if detail else ""), flush=True)


def reg_get(key: str, name: str) -> str | None:
    try:
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, key) as k:
            return winreg.QueryValueEx(k, name)[0]
    except FileNotFoundError:
        return None


def reg_set_run(value: str | None) -> None:
    with winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, RUN_KEY, 0, winreg.KEY_SET_VALUE) as k:
        if value is None:
            try:
                winreg.DeleteValue(k, RUN_NAME)
            except FileNotFoundError:
                pass
        else:
            winreg.SetValueEx(k, RUN_NAME, 0, winreg.REG_SZ, value)


def install() -> int:
    return subprocess.run([str(SETUP), "/S"], timeout=180).returncode


def uninstall() -> bool:
    subprocess.run([str(INSTDIR / "uninstall.exe"), "/S"], timeout=60)
    deadline = time.time() + 30
    while time.time() < deadline and INSTDIR.exists():
        time.sleep(0.5)
    return not INSTDIR.exists()


def main() -> int:
    if SETUP is None:
        print("missing bin/ssh-tunnel-manager-*-setup.exe; run wails3 task windows:package first")
        return 2
    if INSTDIR.exists():
        print(f"{INSTDIR} already exists; uninstall the existing copy first")
        return 2
    original_run = reg_get(RUN_KEY, RUN_NAME)
    try:
        code = install()
        step("silent install exits 0", code == 0, f"exit={code}")
        step("exe, uninstaller and Start menu shortcut installed",
             EXE.exists() and (INSTDIR / "uninstall.exe").exists() and SHORTCUT.exists())
        version = reg_get(UNINST_KEY, "DisplayVersion")
        step("Apps & features entry (HKCU) with version", version is not None and SETUP.name.endswith(f"-{version}-setup.exe"), str(version))

        # An open handle without delete sharing fails Delete the same way a running exe does;
        # launching the app would just hand over to an already running instance.
        with open(EXE, "rb"):
            code = install()
        step("upgrade while the exe is in use is refused (silent: aborts, exe untouched)", code != 0 and EXE.exists(), f"exit={code}")
        code = install()
        step("upgrade after exit succeeds", code == 0, f"exit={code}")

        reg_set_run(f'"{EXE}"')
        step("uninstall removes files, shortcut and Apps entry",
             uninstall() and not SHORTCUT.exists() and reg_get(UNINST_KEY, "DisplayName") is None)
        step("uninstall removes this installation's autostart entry", reg_get(RUN_KEY, RUN_NAME) is None)

        install()
        reg_set_run(r'"C:\Portable\ssh-tunnel-manager.exe"')
        uninstall()
        step("uninstall keeps an autostart entry of another copy", reg_get(RUN_KEY, RUN_NAME) == r'"C:\Portable\ssh-tunnel-manager.exe"')
    finally:
        if INSTDIR.exists():
            uninstall()
        reg_set_run(original_run)
    print(f"\n{sum(results)}/{len(results)} steps passed")
    return 0 if results and all(results) else 1


if __name__ == "__main__":
    sys.exit(main())
