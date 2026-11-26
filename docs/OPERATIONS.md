# SWIM operations guide

**English** · [ไทย](OPERATIONS.th.md)

## Where things live on the server

| Path | Contents |
|---|---|
| `/opt/swim` | The code (this repository) |
| `/opt/swim/builder-infra/.env` | System secrets (mode 0600, not in Git) |
| `/srv/swim/storage/` | Images, WinPE, agent binaries, task logs |
| `/srv/swim/keys/` | Command-signing key and schedule encryption key |
| `/srv/swim/certs/` | TLS certificate |
| `/srv/swim/vms/` | Golden master disks, `iso/` for Windows ISOs |
| `/srv/swim/audit-archive/` | Archived activity log files |
| Docker volume `swim_pgdata` | PostgreSQL database |

## Updating the system

Before every update, check that no installation or capture is running (console home page).
Restarting the backend interrupts running installations, and the `builder` container must never be
restarted during a capture.

```bash
cd /opt/swim && git pull
cd builder-infra

# backend changed
sudo docker compose build backend && sudo docker compose up -d --no-deps backend

# nginx.conf changed
sudo docker exec swim-nginx-1 nginx -t && sudo docker exec swim-nginx-1 nginx -s reload

# console changed: nothing to do, nginx serves ../frontend directly
# (bump the ?v= value in frontend/index.html whenever app.js or app.css changes)
```

Database migrations run automatically when the backend starts.

## Releasing a new agent

1. Raise the version in `agent/internal/agent/state.go` (`const Version`).
2. Run `./builder-infra/scripts/build-agent.sh`. It runs vet and tests, then publishes the binary
   and `latest.json`.
3. Online clients update themselves within minutes. They verify the SHA-256 before switching and
   roll back if the new version does not start. Machines with an open installation wait until it ends.

If `pe/` changed, also run `./builder-infra/scripts/build-winpe.sh`.

## Backup

What you need to restore the system completely:

```bash
# database
sudo docker exec swim-postgres-1 pg_dump -U swim_owner -Fc swim > swim-$(date +%F).dump

# secrets and keys (store them somewhere safe, outside Git)
sudo tar czf swim-secrets-$(date +%F).tgz /opt/swim/builder-infra/.env /srv/swim/keys /srv/swim/certs
```

If the keys in `/srv/swim/keys/` are lost, installed agents will not trust commands from the new
server and every agent must be reinstalled. If the TLS certificate changes, the fingerprint pinned
by the agents no longer matches and they must be reinstalled as well. Images in
`/srv/swim/storage/images/` can be rebuilt from the golden master, so backing them up is optional.

## Security

- **Sign-in.** User name, password and an authenticator code (TOTP). 5 failures suspend the account
  for 15 minutes; 20 failures from one IP address suspend that address for 15 minutes.
- **Admin tokens in `.env`.** They sign in without the second factor. Keep them for emergencies and
  for scripts on the server only.
- **Lost password or phone.** Another administrator presses "Reset sign-in", or run on the
  server `sudo docker exec swim-backend-1 swim-server user reset-2fa <username>` to get a new setup link.
- **Commands to clients.** Every command is signed with Ed25519. The agent checks the signature,
  the validity window and replays.
- **Tools on clients.** The script of every built-in tool is written and validated on the server
  (`backend/internal/api/tools.go`). The API accepts no script text from outside, and saving new
  scripts through the API is closed.
- **Activity log.** It cannot be edited or deleted directly. Entries are kept for 90 days and then
  removed automatically (`SWIM_AUDIT_KEEP_DAYS`, minimum 30). Failed sign-ins record the IP address,
  the name that was typed and the browser, never the password that was typed.
- **nginx.** TLS 1.2 or newer, security headers (CSP, HSTS, no framing) and rate limiting on sign-in.

Recommended on a real server (the install script does not do this, because it could lock you out):

```bash
# disable SSH password login (make sure your key is in ~/.ssh/authorized_keys first)
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\n' | sudo tee /etc/ssh/sshd_config.d/00-swim-hardening.conf
sudo sshd -t && sudo systemctl reload ssh
```

Replace the self-signed TLS certificate with one from your organisation's CA before production use.

## Troubleshooting

| Symptom | Where to look |
|---|---|
| The console does not load | `sudo docker compose ps`, `sudo docker compose logs nginx backend` |
| A client does not appear | On the client: `C:\ProgramData\SWIM\logs\agent.log` and `"C:\Program Files\SWIM\swim-agent.exe" status` |
| A capture task fails | The task's log button on the Images page, or `/srv/swim/storage/logs/tasks/<task_id>.log` |
| An installation fails | Machine page → Windows tab → job details (boot diagnostics are attached) |
| Server storage is filling up | The Server storage page |

## Known issues

**KubeVirt virtual machines return to the old Windows instead of booting WinPE.** Seen on test VMs
on 26 Nov 2026: at the moment the VM starts WinPE, the platform recreates the whole VM (the TPM is
cleared, the clock and the Secure Boot variables are reset), so it boots the existing Windows with
nothing changed. The cause is on the platform side and has not been identified. Since agent 0.5.23
the agent retries the boot into WinPE by itself, three attempts in total, before giving the job up.
This has not been tested on physical PCs yet.
