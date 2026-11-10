# Server setup

SSH Tunnel Manager never connects to your server to change anything. For each tunnel it **generates** what the server needs; you review it and run it yourself.

Open it from the tunnel row: **Server setup** button. Choose the mode in the tunnel form (**Server setup** section).

## Modes

| Mode | Server changes | Use when |
|---|---|---|
| `none` | nothing | The account can already forward ports, and a `-R` tunnel binds to `127.0.0.1` (works with the default `GatewayPorts no`). |
| `sshd` | dedicated user + sshd drop-in + `authorized_keys` entry, all limited to this tunnel's forward | You want a locked-down tunnel account, a public bind (`0.0.0.0`), or dead sessions to free the port quickly. |
| `sshd+caddy` | as `sshd` + a Caddy site block `domain { reverse_proxy 127.0.0.1:<port> }` | You publish a local web app over HTTPS on a domain. Needs a `-R` forward. |

## What the `sshd` script does

The script is named `stm-setup-<tunnel>.sh`. Run it as root on a Debian/Ubuntu/RHEL-like server with OpenSSH 7.8 or newer. It is idempotent: running it again changes nothing.

1. Creates the tunnel user if missing: no login shell, no password. Key logins only, unless you run `passwd` for it.
2. Writes `/etc/ssh/sshd_config.d/ssh-tunnel-manager-<user>.conf` with a `Match User <user>` block:
   - `AllowTcpForwarding remote` for `-R` tunnels, or `local` for `-L` tunnels.
   - `PermitListen <bind>:<port>` for `-R`, or `PermitOpen <target>:<port>` for `-L`. Running the script for another tunnel of the same user adds to this list instead of replacing it.
   - `GatewayPorts clientspecified` only when some allowed bind is not loopback.
   - `PermitTTY no`, `X11Forwarding no`, `AllowAgentForwarding no`.
   - `ClientAliveInterval 15`, `ClientAliveCountMax 3`. A dropped client's session dies within about 45 s, so the remote port is freed and the reconnect does not hit "port busy".
3. Adds `Include /etc/ssh/sshd_config.d/*.conf` to `sshd_config` if it is missing. A backup is saved as `sshd_config.ssh-tunnel-manager.bak`.
4. Runs `sshd -t`. If the config is rejected, the previous file is restored and the script stops.
5. Adds your public key to `~<user>/.ssh/authorized_keys` as `restrict,port-forwarding,permitlisten="<bind>:<port>" <key> ssh-tunnel-manager`. For `-L` tunnels the option is `permitopen=…`. If the key is already there for another tunnel, its option list is extended.
6. In `sshd+caddy` mode:
   - If `/etc/caddy/Caddyfile` exists, appends the site block, runs `caddy validate` (restoring the file on failure), and reloads Caddy.
   - Otherwise it prints the block for you to add.
7. Reloads sshd. Existing SSH sessions stay open.

The public key comes from the tunnel's key file: `<key>.pub`, or the public part of a `.ppk`. For agent or password auth, paste the key in the dialog, or set `PUBKEY='ssh-ed25519 AAAA…'` in the script before running it. For password auth, set a password with `sudo passwd <user>` and make sure `PasswordAuthentication` allows it.

## Steps

```sh
# on your PC: save the script from the dialog, then
scp stm-setup-dev-web.sh <admin>@<server>:
# on the server
sudo bash stm-setup-dev-web.sh
```

Connect the tunnel in the app, then check on the server:

```sh
sudo ss -ltnp | grep ':8080 '        # the -R port is listening
sudo sshd -T -C user=tunnel,host=x,addr=127.0.0.1 | grep -Ei 'permitlisten|allowtcpforwarding|gatewayports'
```

For `sshd+caddy`, point the domain's DNS A/AAAA records at the server and open ports 80 and 443. Caddy obtains the certificate itself.

## Troubleshooting

| Symptom in the app | Cause / fix |
|---|---|
| `remote port forwarding failed for listen port …` ("port busy") | The port is outside `PermitListen`; run the script generated for this tunnel. Or an old session still holds the port: it frees within ~45 s with the script's `ClientAlive*` settings. Or another process uses it: check with `ss -ltnp`. |
| Public bind `0.0.0.0` but the port only listens on `127.0.0.1` | `GatewayPorts` is not `clientspecified` for the user; use mode `sshd`. |
| `Permission denied (publickey)` | The key was not added: check `~<user>/.ssh/authorized_keys` (mode 600, owned by the user; `.ssh` mode 700). Or sshd's `AllowUsers`/`AllowGroups` exclude the user. |
| Script stops with `sshd -t rejected the new config` | OpenSSH older than 7.8 (no `PermitListen`), or an error elsewhere in your sshd config; run `sudo sshd -t` to see it. |
| Script stops with `key is already in … without the ssh-tunnel-manager marker` | The key is already used for normal logins. Use a separate key for tunnels so the tunnel restrictions do not lock that login down. |
| Caddy domain shows a TLS error | DNS not pointing at the server yet, or ports 80/443 blocked; see `journalctl -u caddy`. |

## Undo

```sh
sudo rm /etc/ssh/sshd_config.d/ssh-tunnel-manager-<user>.conf
sudo userdel -r <user>
sudo systemctl reload ssh
```

Also remove the site block from `/etc/caddy/Caddyfile` if you used `sshd+caddy`.

## Testing (developers)

- `go test ./internal/serverkit` compares golden files for the 3 modes × bind `127.0.0.1`/`0.0.0.0`, plus `-L`. Regenerate them with `-update`.
- `docker build -t stm-serverkit-test test/serverkit`, then `go test -tags integration ./internal/serverkit`. This runs the script twice in a clean Debian container and checks:
  - exit code 0, `sshd -t` passes, and entries are not duplicated;
  - a tunnel outside `permitlisten` is refused and an allowed one comes up;
  - a second tunnel extends the allowed list;
  - `caddy validate` accepts the Caddy block.
