"""Window QA: drives the real built exe through WebView2's DevTools port.

Needs: a QA build (wails3 build EXTRA_TAGS=qa), Docker container stm-sshd on
127.0.0.1:2222, Python Playwright. Rotates the container host key (step 8).
Run: python test/qa_gui.py
"""

import json
import os
import random
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

from playwright.sync_api import Error as PWError
from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[1]
EXE = ROOT / "bin" / "ssh-tunnel-manager.exe"
DATA = Path(tempfile.gettempdir()) / "stm-gui-qa"
CDP_PORT = random.randint(20000, 29999)
PASSWORD = "testpass"
CONTAINER = "stm-sshd"

results: list[tuple[str, bool, str]] = []


def step(name: str, ok: bool, detail: str = "") -> None:
    results.append((name, ok, detail))
    print(("PASS " if ok else "FAIL ") + name + (f" - {detail}" if detail else ""), flush=True)
    if not ok:
        raise AssertionError(name)


def sh(*args: str) -> str:
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=60).stdout


def server_fp() -> str:
    return sh("docker", "exec", CONTAINER, "ssh-keygen", "-lf", "/etc/ssh/ssh_host_ed25519_key.pub").split()[1]


def config() -> dict:
    return json.loads((DATA / "config.json").read_text(encoding="utf-8"))


def tunnel_id(name: str) -> str:
    return next(t["id"] for t in config()["tunnels"] if t["name"] == name)


def credential_exists(ref: str) -> bool:
    out = subprocess.run(["cmdkey", f"/list:{ref}"], capture_output=True, text=True).stdout
    return any(l.strip().startswith("Target:") and ref in l for l in out.splitlines())


def our_clients() -> list[str]:
    ps = ("Get-CimInstance Win32_Process -Filter \"Name='ssh.exe' OR Name='plink.exe'\" | "
          "ForEach-Object { $_.CommandLine }")
    lines = sh("powershell", "-NoProfile", "-Command", ps).splitlines()
    return [l for l in lines if str(DATA) in l or DATA.as_posix() in l or "-load ssh-tunnel-manager" in l]


def start_app() -> subprocess.Popen:
    env = dict(os.environ, STM_QA_CDP_PORT=str(CDP_PORT))
    return subprocess.Popen([str(EXE), "-data", str(DATA), "-show"], env=env)


def connect(pw):
    deadline = time.time() + 30
    while True:
        try:
            browser = pw.chromium.connect_over_cdp(f"http://127.0.0.1:{CDP_PORT}")
            break
        except PWError:
            if time.time() > deadline:
                raise
            time.sleep(0.5)
    deadline = time.time() + 30
    while time.time() < deadline:
        for ctx in browser.contexts:
            for p in ctx.pages:
                if p.url.startswith("http"):
                    return browser, p
        time.sleep(0.5)
    seen = [(p.url, ctx is not None) for ctx in browser.contexts for p in ctx.pages]
    raise RuntimeError(f"no app page over CDP; contexts={len(browser.contexts)} pages={seen}")


def main() -> int:
    if not EXE.exists():
        print(f"missing {EXE}; run wails3 build first")
        return 2
    shutil.rmtree(DATA, ignore_errors=True)
    app = start_app()
    created_refs: list[str] = []
    try:
        with sync_playwright() as pw:
            browser, page = connect(pw)
            page.set_default_timeout(10_000)
            run_steps(page, created_refs, app)
            browser.close()
    except AssertionError:
        pass
    finally:
        if app.poll() is None:
            app.kill()
        for ref in created_refs:
            if credential_exists(ref):
                subprocess.run(["cmdkey", f"/delete:{ref}"], capture_output=True)
    failed = [r for r in results if not r[1]]
    print(f"\n{len(results) - len(failed)}/{len(results)} steps passed")
    return 1 if failed or not results else 0


def row(page, name):
    return page.locator(f'[data-testid="tunnel-row"][data-name="{name}"]')


def open_new_form(page):
    page.get_by_test_id("add-tunnel").click()
    expect(page.get_by_test_id("tunnel-form")).to_be_visible()


def fill_tunnel(page, *, name, backend="openssh", user="pwuser", bind_port, secret=PASSWORD, target_port="9"):
    page.get_by_test_id("form-name").fill(name)
    page.get_by_test_id("form-backend").select_option(backend)
    page.get_by_test_id("form-host").fill("127.0.0.1")
    page.get_by_test_id("form-port").fill("2222")
    page.get_by_test_id("form-user").fill(user)
    page.get_by_test_id("form-auth-method").select_option("password")
    page.get_by_test_id("form-secret").fill(secret)
    page.get_by_test_id("form-forward-type").select_option("R")
    page.get_by_test_id("form-bind-address").fill("127.0.0.1")
    page.get_by_test_id("form-bind-port").fill(bind_port)
    page.get_by_test_id("form-target-host").fill("127.0.0.1")
    page.get_by_test_id("form-target-port").fill(target_port)


def wait_state(page, name, state, timeout=20_000):
    expect(row(page, name)).to_have_attribute("data-state", state, timeout=timeout)


def hostkey_dialog(page, changed: bool):
    d = page.get_by_test_id("hostkey-dialog")
    expect(d).to_be_visible(timeout=20_000)
    expect(d).to_have_attribute("data-changed", "true" if changed else "false")
    return d


def run_steps(page, refs, app):
    fp = server_fp()

    # 1. add valid tunnel
    open_new_form(page)
    fill_tunnel(page, name="qa-ssh", bind_port="18310")
    page.get_by_test_id("form-save").click()
    expect(row(page, "qa-ssh")).to_be_visible()
    expect(page.get_by_test_id("tunnel-form")).to_be_hidden()
    raw = (DATA / "config.json").read_text(encoding="utf-8")
    refs.append("ssh-tunnel-manager:" + tunnel_id("qa-ssh"))
    step("add tunnel -> listed, saved, secret not in config",
         "qa-ssh" in raw and PASSWORD not in raw and credential_exists(refs[-1]))

    # 2. invalid port -> validation, no save
    open_new_form(page)
    fill_tunnel(page, name="qa-invalid", bind_port="70000")
    page.get_by_test_id("form-save").click()
    expect(page.locator('[data-testid="field-error"]').first).to_be_visible()
    expect(page.get_by_test_id("tunnel-form")).to_be_visible()
    step("invalid port -> validation message, not saved", len(config()["tunnels"]) == 1)
    page.get_by_test_id("form-cancel").click()

    # 3. edit -> config updated
    row(page, "qa-ssh").get_by_test_id("tunnel-edit").click()
    page.get_by_test_id("form-name").fill("qa-ssh2")
    page.get_by_test_id("form-save").click()
    expect(row(page, "qa-ssh2")).to_be_visible()
    step("edit -> config.json updated", [t["name"] for t in config()["tunnels"]] == ["qa-ssh2"])

    # 4. connect -> new host key dialog -> trust -> Up
    row(page, "qa-ssh2").get_by_test_id("tunnel-toggle").click()
    d = hostkey_dialog(page, changed=False)
    shown = d.get_by_test_id("hostkey-fingerprint").inner_text().strip()
    step("new host key dialog shows server fingerprint", fp in shown, shown)
    d.get_by_test_id("hostkey-accept").click()
    wait_state(page, "qa-ssh2", "up")
    step("trust -> state up", True)

    # 5. logs: live lines, verbose filter
    row(page, "qa-ssh2").get_by_test_id("tunnel-logs").click()
    panel = page.get_by_test_id("logs-panel")
    expect(panel).to_be_visible()
    expect(panel.get_by_test_id("log-line").filter(has_text="connected (pid")).to_have_count(1)
    debug_quiet = [t for t in panel.get_by_test_id("log-line").filter(has_text="debug1:").all_inner_texts()
                   if "remote forward success" not in t]  # Up marker is promoted to a normal line on purpose
    panel.get_by_test_id("logs-verbose").check()
    expect(panel.get_by_test_id("log-line").filter(has_text="debug1:").first).to_be_visible()
    step("verbose toggle reveals debug1 lines", debug_quiet == [], "; ".join(debug_quiet[:5]))

    # 6. AC-9 leak check while up
    leak = subprocess.run(["powershell", "-NoProfile", "-File", str(ROOT / "test" / "leak-check.ps1"), "-DataDir", str(DATA)],
                          env=dict(os.environ, STM_LEAK_SECRET=PASSWORD), capture_output=True, text=True)
    step("AC-9 leak check while up", leak.returncode == 0, leak.stdout.strip())

    t0 = time.time()
    row(page, "qa-ssh2").get_by_test_id("tunnel-toggle").click()
    expect(panel.get_by_test_id("log-line").filter(has_text="stopped")).to_have_count(1, timeout=2_000)
    wait_state(page, "qa-ssh2", "stopped", timeout=5_000)
    step("disconnect -> stopped, new log line within 2 s", True, f"{time.time() - t0:.1f}s")
    page.get_by_test_id("logs-close").click()

    # 7. plink: unknown key -> reject -> review -> accept
    open_new_form(page)
    fill_tunnel(page, name="qa-plink", backend="plink", bind_port="18311")
    page.get_by_test_id("form-save").click()
    expect(row(page, "qa-plink")).to_be_visible()
    refs.append("ssh-tunnel-manager:" + tunnel_id("qa-plink"))
    row(page, "qa-plink").get_by_test_id("tunnel-toggle").click()
    d = hostkey_dialog(page, changed=False)
    step("plink unknown key -> dialog with fingerprint", fp in d.get_by_test_id("hostkey-fingerprint").inner_text())
    d.get_by_test_id("hostkey-reject").click()
    wait_state(page, "qa-plink", "error")
    row(page, "qa-plink").get_by_test_id("tunnel-review-hostkey").click()
    d = hostkey_dialog(page, changed=False)
    d.get_by_test_id("hostkey-accept").click()
    wait_state(page, "qa-plink", "up")
    saved = next(t for t in config()["tunnels"] if t["name"] == "qa-plink")["hostKey"].get("plinkFingerprint")
    step("plink reject -> error, review -> accept -> up, fingerprint saved", saved == fp, saved or "")
    row(page, "qa-plink").get_by_test_id("tunnel-toggle").click()
    wait_state(page, "qa-plink", "stopped")

    # 8. changed host key (rotates the container key)
    sh("docker", "exec", CONTAINER, "rm", "-f", "/etc/ssh/ssh_host_ed25519_key", "/etc/ssh/ssh_host_ed25519_key.pub")
    sh("docker", "restart", CONTAINER)
    time.sleep(2)
    new_fp = server_fp()
    step("container host key rotated", new_fp != fp)

    row(page, "qa-ssh2").get_by_test_id("tunnel-toggle").click()
    d = hostkey_dialog(page, changed=True)
    old_shown = d.get_by_test_id("hostkey-old-fingerprint").inner_text()
    new_shown = d.get_by_test_id("hostkey-fingerprint").inner_text()
    replace = d.get_by_test_id("hostkey-replace")
    disabled_before = replace.is_disabled()
    d.get_by_test_id("hostkey-replace-confirm").check()
    step("changed key dialog: old+new shown, replace needs confirmation",
         fp in old_shown and new_fp in new_shown and disabled_before and replace.is_enabled())
    replace.click()
    wait_state(page, "qa-ssh2", "up")
    step("replace key -> openssh up", True)

    row(page, "qa-plink").get_by_test_id("tunnel-toggle").click()
    d = hostkey_dialog(page, changed=True)
    d.get_by_test_id("hostkey-reject").click()
    wait_state(page, "qa-plink", "error")
    expect(row(page, "qa-plink").get_by_test_id("tunnel-review-hostkey")).to_be_visible()
    step("plink changed key -> keep blocked -> error + review button", True)

    # 9. settings validation
    page.get_by_test_id("nav-settings").click()
    page.get_by_test_id("settings-ssh-path").fill(r"C:\nope\ssh.exe")
    page.get_by_test_id("settings-save").click()
    expect(page.get_by_test_id("settings-error")).to_be_visible()
    page.get_by_test_id("settings-ssh-path").fill("")
    page.get_by_test_id("settings-save").click()
    expect(page.get_by_test_id("settings-error")).to_be_hidden()
    step("settings reject missing exe, accept default", config()["settings"].get("sshPath", "") == "")
    if page.get_by_test_id("settings-panel").is_visible():
        page.keyboard.press("Escape")

    # 9b. server setup: caddy validation, sshd script with pasted key
    row(page, "qa-plink").get_by_test_id("tunnel-edit").click()
    page.get_by_test_id("form-server-mode").select_option("sshd+caddy")
    page.get_by_test_id("form-save").click()
    expect(page.locator('[data-testid="field-error"][data-field="serverDomain"]')).to_be_visible()
    page.get_by_test_id("form-server-domain").fill("app.example.com")
    page.get_by_test_id("form-save").click()
    expect(page.get_by_test_id("tunnel-form")).to_be_hidden()
    server = next(t for t in config()["tunnels"] if t["name"] == "qa-plink")["server"]
    step("caddy mode needs a domain; saved mode+domain", server == {"mode": "sshd+caddy", "domain": "app.example.com"}, str(server))

    row(page, "qa-plink").get_by_test_id("tunnel-server-setup").click()
    d = page.get_by_test_id("server-setup-dialog")
    expect(d.get_by_test_id("server-script")).to_contain_text("PUBKEY=''")
    key = (ROOT / "test" / "keys" / "client_test.pub").read_text(encoding="utf-8").strip()
    d.get_by_test_id("server-pubkey").fill(key)
    d.get_by_test_id("server-pubkey-apply").click()
    expect(d.get_by_test_id("server-script")).to_contain_text("PUBKEY='" + " ".join(key.split()[:2]) + "'")
    script = d.get_by_test_id("server-script").inner_text()
    caddy = d.get_by_test_id("server-caddyfile").inner_text()
    step("server setup dialog: script with permitlisten + pasted key, caddy block",
         'permitlisten="127.0.0.1:18311"' in script and "reverse_proxy 127.0.0.1:18311" in caddy)
    d.get_by_test_id("server-close").click()
    expect(d).to_be_hidden()

    # 10. delete -> gone from list and Credential Manager
    plink_ref = refs[-1]
    row(page, "qa-plink").get_by_test_id("tunnel-delete").click()
    page.get_by_test_id("confirm-delete").click()
    expect(row(page, "qa-plink")).to_have_count(0)
    names, cred = [t["name"] for t in config()["tunnels"]], credential_exists(plink_ref)
    step("delete -> removed from list, config and Credential Manager",
         names == ["qa-ssh2"] and not cred, f"config={names} credential_left={cred}")

    # 11. AC-10: kill the app while a tunnel is up
    wait_state(page, "qa-ssh2", "up")
    step("our ssh.exe running before kill", len(our_clients()) == 1, str(our_clients()))
    subprocess.run(["taskkill", "/F", "/PID", str(app.pid)], capture_output=True)
    time.sleep(2)
    step("AC-10: no ssh/plink left 2 s after killing the app", our_clients() == [], str(our_clients()))


if __name__ == "__main__":
    sys.exit(main())
