# Install

## Requirements

| | |
|---|---|
| Node.js | 20.17 or later in the 20 line, 22.6 or later in the 22 line, or 24. Earlier releases of 20 and 22 cannot start the CLI at all. |
| Docker | Docker Engine 26 or later, with the Compose v2 plugin. Each agent runs in its own container. |
| OS | Linux, as root. The `claude-code` template that ships with Baton gives each node a loop-mounted disk image (`mkfs.ext4`, `mount -o loop`), which needs root. macOS is not supported in this release. |

Run `baton doctor` after installing. It checks that Docker and Compose are
reachable, that the ports Baton uses are free, and your Node.js version. It does
not check the Docker version: confirm `docker version` reports 26 or later
yourself. On a Node.js release older than the ones above, `baton` — including
`doctor` — fails at startup with a `SyntaxError`.

## Install the CLI

```bash
npm install -g @batonos/cli
```

This puts a single command, `baton`, on your `PATH`.

Check it:

```bash
baton --version
baton doctor
```

> The package is `@batonos/cli`. Other packages on npm with similar names —
> including the bare name `baton` — are not this project.

## Where Baton keeps its data

Baton keeps everything for this machine in one data directory: the control
plane's database, node identities, templates, plugins and snapshots. Back that
directory up and you have backed up Baton.

## Uninstall

```bash
npm uninstall -g @batonos/cli
```

Uninstalling the CLI does not remove your nodes or your data directory. Remove
nodes first with `baton destroy <node> --yes` if you want them gone.
