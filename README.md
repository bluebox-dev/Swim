<div align="center">

**English** · [ไทย](README.th.md)

# SWIM — Staged Windows Image Manager

**Reimage a whole Windows computer lab over the network. No PXE, no USB sticks.**

[![License: AGPL v3](https://img.shields.io/badge/license-AGPL%20v3-blue.svg)](LICENSE)
![Go 1.24](https://img.shields.io/badge/Go-1.24-00ADD8?logo=go&logoColor=white)
![Server: Ubuntu 24.04](https://img.shields.io/badge/server-Ubuntu%2024.04-E95420?logo=ubuntu&logoColor=white)
![Clients: Windows 11](https://img.shields.io/badge/clients-Windows%2011-0078D4)
![Docker Compose](https://img.shields.io/badge/deploy-Docker%20Compose-2496ED?logo=docker&logoColor=white)

[Quick start](#quick-start) · [Screenshots](#screenshots) · [Architecture](#architecture) · [Installation guide](docs/INSTALL.md) · [Operations guide](docs/OPERATIONS.md)

</div>

SWIM builds a Windows image from a golden master VM on the server and installs it on client
machines one at a time, a room at a time, or on a schedule. Each client downloads the image
itself, verifies it, boots into WinPE, installs Windows and comes back online without anyone
walking to the machine.

![SWIM home page with the system map and a Windows installation in progress](docs/images/home.png)

## Contents

- [Features](#features)
- [Screenshots](#screenshots)
- [Architecture](#architecture)
  - [Components](#components)
  - [Image pipeline](#image-pipeline)
  - [Reimage flow](#reimage-flow)
  - [Trust and security model](#trust-and-security-model)
- [Quick start](#quick-start)
- [Repository layout](#repository-layout)
- [What is not in this repository](#what-is-not-in-this-repository)
- [Documentation](#documentation)
- [License](#license)

## Features

- **Build images** from a golden master VM that runs on the server, or import an edition straight
  from a Windows ISO. SWIM runs sysprep, performs six offline checks and holds the image for
  approval before it can be installed.
- **Reimage over the network.** The client downloads the image, verifies its SHA-256, boots into
  WinPE, installs Windows and re-enrolls on its own.
- **Deploy at scale.** Select many machines or a whole room, schedule jobs on a calendar, and
  pre-stage images on clients ahead of time.
- **Manage machines** with 26 built-in tools (power, local users, programs, services, Windows
  Update, network, disk), in-browser Remote Desktop, Wake-on-LAN and an install-lock policy.
- **Audit everything.** Every action lands in a tamper-evident activity log, kept for 90 days.
- **Secure by design.** Sign-in uses a password plus an authenticator app (TOTP), and every command
  sent to a client is signed with Ed25519.
- **Bilingual console** in English and Thai, with dark and light themes.

## Screenshots

All screenshots are taken from the running application. Machine names, IP addresses and user
names were replaced with sample values.

| | |
|---|---|
| **Sign-in** with password and authenticator code | **Machine map** grouped by room, drag a machine to move it |
| ![Sign-in page](docs/images/login.png) | ![Machine map grouped by room](docs/images/hosts-map.png) |
| **Machine list** with online status and installed Windows | **Images** and the golden master capture steps |
| ![Machine table](docs/images/hosts-table.png) | ![Images page](docs/images/images.png) |
| **Machine page** with install policy and pre-staged image | **Tools** whose scripts are built and checked on the server |
| ![Windows tab of a machine](docs/images/host-windows.png) | ![Built-in tools](docs/images/host-tools.png) |
| **Schedule** reimage jobs ahead of time | **Activity log** of who did what, where and when |
| ![Schedule calendar](docs/images/schedule.png) | ![Activity log](docs/images/logs.png) |

Light theme:

![Home page in the light theme](docs/images/home-light.png)

## Architecture

### Components

One server runs everything. The control plane lives in Docker Compose, and KVM/libvirt on the same
host runs the golden master VM. Only port 443 is exposed to the network (port 80 redirects to it);
internal services are not reachable from outside.

```mermaid
flowchart LR
    admin["Administrator<br/>(browser)"]

    subgraph server["SWIM Server · Ubuntu 24.04"]
        nginx["nginx<br/>TLS, security headers,<br/>sign-in rate limiting"]
        backend["backend (Go)<br/>REST API, command signing,<br/>scheduler, audit log"]
        pg[("PostgreSQL")]
        builder["builder<br/>image capture<br/>(privileged)"]
        guac["Guacamole + guacd<br/>VM console and RDP"]
        waker["waker<br/>Wake-on-LAN"]
        storage[("/srv/swim/storage<br/>images, WinPE, agent")]
        gm["Golden master<br/>(KVM/libvirt VM)"]
    end

    subgraph lab["Computer lab"]
        agent["SWIM Agent<br/>(Windows service)"]
        pe["SWIM WinPE<br/>(swim-pe)"]
    end

    admin -- "HTTPS 443" --> nginx
    nginx --> backend
    nginx --> guac
    nginx -. "large files<br/>(X-Accel-Redirect)" .-> storage
    backend --> pg
    backend -- "capture tasks" --> builder
    backend -- "Unix socket" --> waker
    builder --> gm
    builder --> storage
    guac -- "VNC" --> gm
    guac -- "RDP" --> agent
    agent -- "HTTPS 443, long-poll<br/>pinned server certificate" --> nginx
    pe -- "HTTPS 443<br/>per-job token" --> nginx
    waker -. "magic packet" .-> agent
```

| Service | Role | Network |
|---|---|---|
| `nginx` | Single entry point. Serves the console, proxies the API and serves images the backend has authorised | 80, 443 |
| `backend` | The whole API: signs commands, runs schedules, writes the activity log | `127.0.0.1:8090` |
| `postgres` | Database. The application role cannot alter or delete activity log rows | Docker internal network |
| `builder` | Captures images and manages golden master snapshots | no ports |
| `guacamole`, `guacd` | Golden master console (VNC) and client Remote Desktop in the browser | `:8080` (blocked by the firewall), `127.0.0.1:4822` |
| `waker` | Sends Wake-on-LAN packets from the host network | Unix socket |

### Image pipeline

No image can be installed before an administrator approves it. Capture works on a temporary copy,
so the golden master itself is never modified.

```mermaid
flowchart LR
    iso["Windows ISO<br/>(/srv/swim/vms/iso)"]
    gm["Golden master<br/>install programs and updates"]
    overlay["Temporary copy<br/>(qcow2 overlay)"]
    sysprep["sysprep<br/>/generalize /oobe"]
    unwof["Expand<br/>CompactOS files"]
    checks{"6 offline checks<br/>S-01 to S-06"}
    wim["Capture to WIM<br/>+ SHA-256"]
    pending["Awaiting approval"]
    published["Published"]
    prestage["Pre-staged<br/>on clients"]
    deploy["Reimage<br/>and install"]
    fail["Task fails<br/>with a log"]

    iso --> gm --> overlay --> sysprep --> unwof --> checks
    iso -- "import directly from ISO" --> wim
    checks -- "pass" --> wim --> pending
    checks -- "S-01 or S-02 fails" --> fail
    pending -- "administrator approves" --> published
    published --> prestage --> deploy
    published --> deploy
```

### Reimage flow

Everything is reversible until WinPE starts to partition the disk. If something goes wrong before
that point, the machine returns to its existing Windows with nothing changed.

```mermaid
sequenceDiagram
    autonumber
    actor A as Administrator
    participant S as SWIM Server
    participant G as Agent (old Windows)
    participant P as SWIM WinPE
    participant N as New Windows

    A->>S: Start reimage (image, disk layout, local admin password)
    S->>S: Sign the command and seal the secrets to that machine
    G->>S: Receive the command (long-poll)
    G->>G: Verify signature, validity window and replay protection
    G->>G: Create a 20 GB staging partition
    G->>S: Download image and WinPE, verify SHA-256
    G->>G: Add this machine's drivers, set a one-time boot into WinPE
    G-->>P: Restart
    Note over G,P: If the old Windows comes back, the agent retries by itself (3 attempts in total)
    P->>S: Report progress (per-job token)
    Note over P: Point of no return: disk partitioning starts
    P->>P: Partition, apply image, inject drivers, agent and answer file
    P-->>N: Restart
    N->>S: Re-enroll with a one-time ticket
    S->>A: Job completed, machine online
```

Job states:

```mermaid
stateDiagram-v2
    [*] --> staging: reimage requested
    staging --> handoff: download and preparation done
    staging --> cancelled: cancelled by an administrator
    handoff --> pe: booted into WinPE
    handoff --> handoff: old Windows came back, automatic retry (max 3 attempts)
    handoff --> aborted: all attempts missed, nothing was changed
    pe --> completed: new Windows re-enrolled
    pe --> failed: error inside WinPE
    failed --> retry: administrator presses retry
    retry --> pe: WinPE downloads the image again
    completed --> [*]
    aborted --> [*]
    cancelled --> [*]
```

### Trust and security model

Clients run commands as SYSTEM, so there are several independent layers between the administrator,
the server and the clients.

```mermaid
flowchart TB
    subgraph adminside["Administrator"]
        login["Password (Argon2id)<br/>+ TOTP + recovery codes"]
        session["Session cookie<br/>HttpOnly, Secure, SameSite=Strict<br/>+ CSRF header"]
        login --> session
    end

    subgraph serverside["Server"]
        tools["Tool registry<br/>scripts are built and validated on the server<br/>no script text is accepted from outside"]
        seal["Secrets sealed<br/>to the machine's X25519 key"]
        sign["Command signed with Ed25519<br/>bound to one machine, with a validity window"]
        audit["Activity log<br/>hash-chained<br/>application role cannot alter or delete it"]
        tools --> sign --> audit
        seal --> sign
    end

    subgraph agentside["Client"]
        pin["Server certificate pinned<br/>(SHA-256)"]
        verify["Verify signature, validity window<br/>and machine identity"]
        replay["Each command runs at most once"]
        hash["Verify SHA-256 of the image,<br/>WinPE and agent updates"]
        pin --> verify --> replay --> hash
    end

    session --> tools
    sign -- "HTTPS 443" --> pin
```

| Layer | Protection |
|---|---|
| Sign-in | Password plus TOTP. 5 failures suspend the account for 15 minutes, 20 failures from one IP suspend that IP for 15 minutes |
| Console | Content-Security-Policy blocks injected scripts, HSTS, no framing by other sites |
| Commands | Every command is signed, bound to one machine and expires. The agent never runs a command twice |
| Secrets | The local admin password is sealed to the target machine. Scheduled jobs store it encrypted (AES-256-GCM) |
| Files | Images, WinPE and agent updates are verified by SHA-256 before use |
| Audit | The log cannot be edited or deleted directly, and the console can verify the hash chain |

More detail: [docs/spec/08-security.md](docs/spec/08-security.md) and [docs/OPERATIONS.md](docs/OPERATIONS.md).

## Quick start

Requirements: Ubuntu 24.04 LTS with hardware virtualisation, 16 GB RAM or more, 100 GB of free disk
and a static IP address.

```bash
sudo mkdir -p /opt/swim && sudo chown "$USER" /opt/swim
git clone https://github.com/bluebox-dev/Swim.git /opt/swim
cd /opt/swim
./install.sh
```

The script installs the host packages, generates fresh secrets and a TLS certificate, configures
the firewall, builds and starts the stack, and prints how to sign in for the first time. The full
walkthrough, including building the first image, is in [docs/INSTALL.md](docs/INSTALL.md).

## Repository layout

```text
.
├── backend/                  Control plane (Go)
│   ├── cmd/server/           Entry point: API server, builder worker, waker, CLI
│   ├── internal/api/         REST API: auth, hosts, commands, deploy, schedules, tools, audit
│   ├── internal/builder/     Image capture: reseal (sysprep), offline checks, ISO import
│   ├── internal/audit/       Hash-chained activity log
│   ├── internal/signing/     Ed25519 command-signing key
│   ├── internal/vm/          libvirt wrapper for the golden master VM
│   ├── internal/wol/         Wake-on-LAN sender
│   ├── internal/msi/         Per-site agent installer (MSI) builder
│   ├── migrations/           PostgreSQL schema, applied automatically at start
│   └── tools/ntfs-unwof/     Helper that expands CompactOS (WOF) files before capture
├── agent/                    Windows agent (Go)
│   ├── cmd/swim-agent/       Service entry point
│   ├── internal/agent/       Enrollment, command loop, reimage, pre-staging, policy, remote session
│   └── install/              install.ps1 and Windows trust policy script
├── pe/cmd/swim-pe/           WinPE worker: partition, apply image, inject drivers and agent
├── pkg/
│   ├── manifest/             Signed command and job manifest formats shared by server and agent
│   └── wof/                  CompactOS (WOF) decompression
├── frontend/                 Admin console: plain HTML, CSS and JavaScript, no build step
├── builder-infra/
│   ├── docker-compose.yml    postgres, backend, builder, waker, nginx, guacd, guacamole
│   ├── nginx/                TLS, security headers, rate limits
│   ├── postgres/             Database role bootstrap
│   └── scripts/              Host setup, agent build, WinPE build, golden master, smoke test
├── docs/                     Installation and operations guides, specification, screenshots
├── install.sh                One-command installation for a fresh Ubuntu 24.04 server
├── LICENSE                   GNU AGPL v3
└── NOTICE                    Copyright and third-party notices
```

| Component | Language | Runs on | Purpose |
|---|---|---|---|
| `backend` | Go | Server (Docker) | API, signing, scheduling, audit log, image builder |
| `frontend` | HTML/CSS/JS | Browser | Administrator console |
| `agent` | Go | Client Windows (service) | Executes signed commands, stages and hands off reimage jobs |
| `pe` | Go | Client WinPE | Partitions the disk and installs the image |
| `pkg` | Go | Shared | Wire formats and CompactOS handling |

## What is not in this repository

Every secret is generated on the server at install time and never stored in Git.

| Not included | Location on the server | Created by |
|---|---|---|
| Database passwords, admin tokens, Guacamole secret | `builder-infra/.env` | `scripts/setup-host.sh` |
| Command-signing key (Ed25519) and schedule encryption key | `/srv/swim/keys/` | backend, on first start |
| TLS certificate and private key | `/srv/swim/certs/` | `scripts/setup-host.sh` |
| Database, images, Windows ISOs, built agent binaries | `/srv/swim/` and a Docker volume | normal use |

An example settings file is at [builder-infra/.env.example](builder-infra/.env.example).
You must supply your own Windows ISO under your organisation's licence.

## Documentation

| Document | Language |
|---|---|
| [Installation guide](docs/INSTALL.md): from an empty server to the first Windows install | English · [ไทย](docs/INSTALL.th.md) |
| [Operations guide](docs/OPERATIONS.md): updates, agent releases, backup, security, known issues | English · [ไทย](docs/OPERATIONS.th.md) |
| [Specification](docs/spec/README.md): 12 chapters covering architecture, agent, manifest, security, data model and API (the target design) | English · [ไทย](docs/spec/README.th.md) |
| [Windows trust policy](docs/deploy/windows-trust-policy.md): Group Policy settings so clients trust the server | Thai |
| [Builder internals](builder-infra/README.md): golden master and image capture details | Thai |

## License

Copyright (C) 2026 Thanakorn Kriangudom

Designed and developed by Thanakorn Kriangudom, with Claude (Anthropic) assisting with code,
security review and documentation.

Released under the [GNU Affero General Public License v3.0](LICENSE) (AGPL-3.0-or-later). See
[NOTICE](NOTICE) for the copyright notice. In short: you may use, modify and redistribute SWIM, but
if you run a modified version for others over a network, you must offer them the source of that
version under the same licence. The `LICENSE` file is the binding text.

The IBM Plex Sans Thai and IBM Plex Mono typefaces in `frontend/fonts/` are Copyright IBM Corp. and
used under the SIL Open Font License 1.1. Windows is a Microsoft product and requires its own licence.
