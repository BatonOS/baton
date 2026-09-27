# Baton

Baton runs AI coding agents — Claude Code, Codex, and others — as isolated,
long-lived **nodes** that you create, watch, snapshot, restore and remove from
one command line.

```sh
npm install -g @batonos/cli
baton --help
```

Start with [Install](docs/install.md) and the [Quickstart](docs/quickstart.md).

## Repository layout

```text
.
├── cli/          The `baton` command line (npm package @batonos/cli, TypeScript)
├── core/         Server side, written in Go and shipped as container images
│   ├── control-api/   Control plane: enrollment, certificates, revocation, delivery, HTTP API
│   ├── agent/         Node agent: identity, capabilities, inbox, plugins, process supervision
│   └── pkg/           Shared Go packages and extension interfaces
├── providers/    Official plugins: Claude Code, Codex, Hermes, OpenClaw, Inbox, Slack
├── templates/    Official node templates and the official template registry
├── specs/        Interface contracts: OpenAPI, WebSocket protocol, JSON Schemas
├── docs/         User documentation
└── scripts/      Build helpers
```

| Directory | What it holds |
|---|---|
| `cli/` | The `baton` command: creating and removing nodes, networks, migration, snapshots, plugins, templates |
| `core/control-api/` | The control plane that admits nodes, issues and revokes their certificates, and routes messages between them |
| `core/agent/` | The agent that runs inside every node and keeps its identity, capabilities and inbox |
| `core/pkg/` | Code shared by the Go modules, including the extension interfaces |
| `providers/` | One directory per official plugin, each with a `manifest.yaml` and a `PLUGIN.md` |
| `templates/` | The templates `baton agent create` builds nodes from |
| `specs/` | The contracts the CLI and the control plane agree on |
| `docs/` | Install, quickstart, concepts and the CLI reference |
| `scripts/` | Scripts the build uses |

## Documentation

| | |
|---|---|
| [Install](docs/install.md) | Get the `baton` command on your machine |
| [Quickstart](docs/quickstart.md) | Create an agent, keep it running, save and restore its work |
| [Concepts](docs/concepts.md) | Agents, nodes, networks, and what Baton does and does not do |
| [CLI reference](docs/cli.md) | Every command, grouped by what you are trying to do |

Baton is `0.x` software. Commands, file formats and defaults may change between
minor versions, and nothing is promised to be backward compatible until `1.0`.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
