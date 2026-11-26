# SWIM installation guide

**English** · [ไทย](INSTALL.th.md)

From an empty server to the first Windows installation on a client machine.

## 1. Prerequisites

| Item | Minimum | Notes |
|---|---|---|
| Operating system | Ubuntu 24.04 LTS | A user with sudo rights (not root) |
| CPU | Hardware virtualisation (VT-x / AMD-V) | On a VM, enable nested virtualisation or the Builder will not work |
| RAM | 16 GB | The golden master uses 8 GB while it is running |
| Disk | 100 GB free | One image is about 12 GB, a golden master 30 to 80 GB |
| Network | Static IP address | Clients must reach port 443 of the server |
| Windows 11 ISO | 1 file | Used for the golden master and for WinPE |

Only ports 22, 80 and 443 are open to the network (the script configures ufw).

## 2. Install the server

```bash
sudo mkdir -p /opt/swim && sudo chown "$USER" /opt/swim
git clone https://github.com/bluebox-dev/Swim.git /opt/swim
cd /opt/swim
./install.sh
```

`install.sh` runs five steps and is safe to run again; it never overwrites what already exists.

1. Installs the host packages: Docker, KVM/libvirt, OVMF, swtpm, wimlib and disk tools.
2. `builder-infra/scripts/setup-host.sh` creates `/srv/swim`, a `builder-infra/.env` file with fresh
   random secrets, a self-signed TLS certificate and the firewall rules.
3. `docker compose up -d --build` builds the backend (`go vet` and `go test` run during the build)
   and starts every service. The database is created and migrated when the backend starts.
4. `builder-infra/scripts/build-agent.sh` builds the Windows agent and publishes it for clients.
5. `builder-infra/scripts/build-winpe.sh` builds SWIM WinPE if an ISO is already in `/srv/swim/vms/iso/`.

Log out and back in once so your user picks up the `docker` and `libvirt` groups.

## 3. First sign-in

1. Open `https://<server IP>/`. The browser warns about the certificate because it is self-signed.
2. While no account exists, sign in with an admin token:

   ```bash
   sudo grep ^SWIM_ADMIN_TOKENS /opt/swim/builder-infra/.env
   ```

3. Create the first administrator account under Settings → Admins → Add admin.
   You get a one-time setup link. Open it, choose a password, scan the QR code with an
   authenticator app such as Google Authenticator, and store the 10 recovery codes.
4. From then on, sign in with user name, password and the 6-digit code from the app.

The same can be done from the command line:

```bash
sudo docker exec swim-backend-1 swim-server user add <username>
```

## 4. Copy a Windows ISO to the server

The console deliberately has no ISO upload. Copy the file over SSH:

```bash
scp Windows11.iso <user>@<server>:/srv/swim/vms/iso/
```

Then build WinPE, if no ISO was present during installation:

```bash
cd /opt/swim/builder-infra
./scripts/build-winpe.sh /srv/swim/vms/iso/Windows11.iso
```

## 5. Build the first image

There are two ways.

**A. Import directly from the ISO.** Images → choose the ISO → choose the Windows edition →
Import → Approve. This gives a clean Windows as shipped, and is the quickest start.

**B. Capture a golden master.** Use this when programs and settings should be part of the image.

```bash
cd /opt/swim/builder-infra
ISO=/srv/swim/vms/iso/Windows11.iso DISK_GB=80 ./scripts/create-golden-master.sh gm-win11
```

1. Images → start the golden master → open its console in the browser, then install Windows,
   programs and updates.
2. Shut the golden master down.
3. Press Capture, choose the image channel and a version name. SWIM runs sysprep in a temporary
   copy, checks the result and captures it. The golden master is not modified.
4. When the status is "Awaiting approval", approve it. The image is then ready to install.

Never remove a golden master with `virsh undefine --remove-all-storage`: it also deletes the ISOs
in `/srv/swim/vms/iso/`.

## 6. Add client machines

The Add machine page offers two installers:

- **MSI file.** Download it and double-click it on the client.
- **PowerShell command.** Paste it into a PowerShell window opened as Administrator.

Both carry a short-lived enrollment token and the fingerprint of the server certificate, so the
agent talks to this server only. The machine appears on the Machines page within seconds.

For many domain-joined machines see [deploy/windows-trust-policy.md](deploy/windows-trust-policy.md) (Thai).

## 7. Install Windows on a client

1. Open the machine → Windows tab → Reimage and install.
2. Choose the image and the disk layout, and set the name and password of the local administrator.
3. Confirm. The machine downloads the image, verifies its SHA-256, boots into WinPE, installs
   Windows and comes back online by itself.

To reimage several machines at once, select rows on the Machines page, or plan it on the Schedule page.

## 8. Verify the installation

```bash
cd /opt/swim/builder-infra
sudo docker compose ps                     # every service must be Up
curl -sk https://127.0.0.1/v1/health       # {"status":"ok"}
sudo bash ./scripts/smoke-test.sh          # end-to-end test (creates test records: lab servers only)
```

Next: [OPERATIONS.md](OPERATIONS.md)
