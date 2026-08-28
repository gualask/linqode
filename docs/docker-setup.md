# Docker setup for development

_Last updated: 2026-08-08_

Linqode itself needs no Docker: the offline suite (`go test ./...`) runs
without it. Docker is required only for the end-to-end fixture described in
[tests.md](tests.md) — sshd + docker-in-docker validating status, logs, stats,
actions, and compiled machine commands against live Docker.

This document covers getting a Docker engine on **macOS**, where it is less
obvious than on Linux, and keeping it off when it is not in use. On Linux,
install Docker Engine and the Compose plugin from your distribution's
official packages and skip to [Disk space](#disk-space-and-cleanup); the
`systemd` unit can be kept out of the boot path with
`systemctl disable --now docker` and started per session with
`systemctl start docker`.

## Why macOS needs a VM

Linux containers *are* Linux kernel features: namespaces, cgroups,
overlayfs. macOS runs the XNU kernel, which has none of them, so `dockerd`
cannot run natively. Every Docker solution on macOS therefore ships a Linux
VM and runs the engine inside it:

```
macOS (XNU)
  └─ Linux VM (Apple Virtualization.framework)
       └─ dockerd
            └─ containers
```

The `docker` binary installed on the host is only the CLI client; it talks
to the daemon inside the VM over a socket. This is true of Docker Desktop,
Colima, Podman, and OrbStack alike — they differ in how much of the VM they
expose, not in whether one exists.

## Colima (recommended here)

[Colima](https://github.com/abiosoft/colima) runs the same Linux VM as any
other option, but as an explicit CLI-managed instance rather than a
background service. Nothing runs until `colima start`, and nothing is left
running after `colima stop` — no login agent, no resident helper. For a
dependency needed only when running the e2e fixture, that is the right
trade-off.

### Install

```bash
brew install colima docker docker-compose
```

Three separate formulae: `colima` is the VM, `docker` is the CLI client
only (not the daemon), `docker-compose` is the Compose plugin.

Homebrew does not wire the plugin into the Docker CLI automatically. Add
its directory to `~/.docker/config.json`, creating the file if absent:

```json
{
  "cliPluginsExtraDirs": [
    "/opt/homebrew/lib/docker/cli-plugins"
  ]
}
```

On Intel Macs the prefix is `/usr/local` instead of `/opt/homebrew`.
Without this, `docker compose` (the plugin form the fixture and
`internal/compose` both use) is not found, even though a standalone
`docker-compose` binary exists on `PATH`.

### Daily use

```bash
colima start --disk 20   # first run only; the config is remembered
docker compose ...       # ordinary Docker workflow
colima stop              # frees everything
```

Values passed on first start are persisted, so later sessions need only
`colima start`. First start downloads the VM image (~1 GB, cached
afterwards) and takes about a minute; subsequent starts are a few seconds.

The defaults are 2 CPUs / 2 GiB / 100 GiB disk. CPU and memory are ceilings
rather than reservations — an idle VM running only dockerd sits at a few
hundred MB — so they can be left alone until something actually needs more,
and raised later with `colima stop && colima start --memory N`.

Disk is worth setting explicitly at creation time, for the reason in
[Disk space](#disk-space-and-cleanup) below.

The mapping to the Linux service model:

| Linux | macOS + Colima |
| ----- | -------------- |
| `systemctl start docker` | `colima start` |
| `systemctl stop docker` | `colima stop` |
| `systemctl status docker` | `colima status` |
| `systemctl disable docker` | not needed — never enabled |

Colima installs no launchd agent and does not start at login. Autostart
would have to be added deliberately.

## Docker Desktop (alternative)

The [official installation](https://docs.docker.com/desktop/setup/install/mac-install/)
is the `.dmg`: drag the icon into Applications, launch `Docker.app`, accept
the Subscription Service Agreement. Compose is bundled. A command-line
install also exists:

```bash
sudo hdiutil attach Docker.dmg
sudo /Volumes/Docker/Docker.app/Contents/MacOS/install
sudo hdiutil detach /Volumes/Docker
```

Two points matter for occasional use:

- **"Start Docker Desktop when you sign in to your computer"** is disabled
  by default. Launch with `open -a Docker`, quit from the whale menu when
  done.
- **Resource Saver** shuts the Linux VM down while idle and restarts it on
  demand, but the application process stays resident until you quit.

Licensing: Docker Desktop requires a paid subscription for organizations
with more than 250 employees or more than $10M in annual revenue. Colima is
MIT-licensed with no such threshold.

Everything Docker Desktop adds over Colima — the GUI, Docker Scout,
extensions, one-click Kubernetes, Hub integration — is unused by the e2e
fixture, which is driven entirely from the command line.

## Disk space and cleanup

### What persists

Images, volumes, stopped containers, and build cache all live in
`/var/lib/docker` **inside the VM**, on a virtual disk stored on the host.
`colima stop` only powers the VM down; the disk survives untouched, so a
start/stop cycle does not rebuild fixture volumes or re-pull base images.

The disk is thin-provisioned: the configured size is a ceiling, not space
consumed on the host. A freshly created VM reports its full size but
occupies a few MB:

```console
$ ls -lh ~/.colima/_lima/_disks/colima/datadisk   # apparent size
-rw-r--r--  20G  datadisk
$ du -sh ~/.colima/_lima/_disks/colima/datadisk   # actually on disk
 10M  datadisk
```

### The disk file only grows

Sparse disk images grow and never shrink on their own. Deleting content
inside the VM frees space *for the VM*, but the host-side file stays at its
high-water mark — run `docker system prune -a`, reclaim 20 GB inside, and
the Mac's free space does not move.

This is why the disk ceiling is worth choosing deliberately. It costs
nothing while unused, but it bounds how much the file can silently grow
into before Docker complains: a small ceiling turns runaway accumulation
into an out-of-space error instead of a quietly vanishing host disk. 20 GiB
is ample for the e2e fixture (docker-in-docker is ~400 MB, plus the demo
project's images and build cache).

Note the asymmetry: the disk can be **grown** after creation but not
shrunk. Reducing it means recreating the VM, which is cheap while empty and
expensive once images have accumulated:

```bash
colima delete --data --force
colima start --disk 20
```

Colima handles this in two ways:

- **Automatic** (v0.5.0+): unused space is released on startup, so
  `colima stop && colima start` settles the accounting.
- **Manual**, without a restart: `colima ssh -- sudo fstrim -a`.

### Commands

Inside Docker — identical to Linux:

```bash
docker system df                    # what is consuming space
docker system prune                 # unused containers, networks, dangling cache
docker system prune -a --volumes    # aggressive: unused images and volumes too
docker builder prune                # build cache only
docker volume prune                 # orphaned volumes only
```

At the Colima level:

```bash
colima prune     # prune unused data in the VM
colima list      # profiles, resources, disk size
colima delete    # destroy the VM
```

**`colima delete` is not a full reset.** Since v0.9.0 container data lives
on a separate disk and is reinstated on the next `colima start` with the
same runtime. For a complete teardown:

```bash
colima delete --data
```

### Periodic cleanup

```bash
colima start
docker system prune -a --volumes
colima stop && colima start   # actually shrink the host-side disk file
```

## Notes for the e2e fixture

The fixture runs docker-in-docker, so it needs privileged containers; the
Colima VM supports them with no extra configuration. Note the nesting
depth — Linux VM → dockerd → privileged DinD container → inner dockerd →
demo project containers. On Linux there is one layer less.

Memory is the setting most likely to need attention because of that
nesting. The default 2 GiB is the current starting point; raise it with
`colima stop && colima start --memory 4` if the fixture proves cramped.
This note should be replaced with a measured figure once the fixture runs.

The fixture targets `docker compose` (the plugin form), matching the
commands `internal/compose` builds for the remote host.
