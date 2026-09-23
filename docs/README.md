# Baton documentation

Baton runs AI coding agents — Claude Code, Codex, and others — as isolated,
long-lived **nodes** that you create, watch, snapshot, restore and remove from
one command line.

| Start here | |
|---|---|
| [Install](install.md) | Get the `baton` command on your machine |
| [Quickstart](quickstart.md) | Create an agent, keep it running, save and restore its work |
| [Concepts](concepts.md) | Agents, nodes, networks, and what Baton does and does not do |
| [CLI reference](cli.md) | Every command, grouped by what you are trying to do |

**What `0.1` does not include:** Baton does not host anything for you. Nodes
run on machines you provide, and moving an agent means moving it between two of
your own machines. There is no hosted or managed node in `0.1`.

Baton is `0.x` software. Commands, file formats and defaults may change between
minor versions, and nothing is promised to be backward compatible until `1.0`.
