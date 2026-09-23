# CLI reference

`baton --help` prints the full list. This page groups the commands you will use
most, by what you are trying to do.

## First run

| Command | What it does |
|---|---|
| `baton doctor` | Check whether this machine can run Baton, and what is missing |
| `baton agent create --name <n>` | Create an agent and its node — and, on a machine that has none yet, a network for it |

## Agents

| Command | What it does |
|---|---|
| `baton agent create --name <n>` | Create an agent; its node comes with it |
| &nbsp;&nbsp;`--template <file\|name>` | The template the node runs |
| &nbsp;&nbsp;`--secret NAME=<file>` | A credential the template declares, read from a file on this machine. Repeatable. Never the value itself. |
| `baton agents` | Which agents exist, and where each one runs |
| `baton agent config show\|set <n>` | The agent's workspace configuration (`set` reads standard input) |
| `baton agent secret list\|set <n> <NAME>` | Fill in the secrets a template declares |

## Nodes

| Command | What it does |
|---|---|
| `baton node list\|show` | The nodes this network knows |
| `baton template list` | The templates on this machine |
| `baton status` | Containers, nodes, and whether the control plane answers |
| `baton start\|stop\|restart [node]` | Lifecycle. Without a name, acts on every node |
| `baton rebuild [node]` | Run the node on a newer image, keeping its workspace and identity |
| `baton destroy <node> --yes` | Remove a node, its volume, and its identity. Cannot be undone. `--keep-data` keeps the volume |

## Saving, restoring and copying

| Command | What it does |
|---|---|
| `baton snapshot <node> [--out <file>]` | Capture the node's portable state to a `.tar.gz`. Never includes its identity |
| `baton restore <node> --from <file> --yes` | Put that state back, replacing what is there. The node keeps its own identity |
| `baton clone <src> <new>` | The same state on a new node, with its own identity |

## Watching and getting in

| Command | What it does |
|---|---|
| `baton logs <node> [-f]` | The node's output; `-f` follows it across restarts |
| `baton attach <node>` | Watch the agent's terminal, read-only |
| `baton attach <node> --takeover` | Take the keyboard: exclusive, and recorded |
| `baton sessions` | Which nodes have a terminal, and who is watching |

## Plugins

| Command | What it does |
|---|---|
| `baton plugin install <dir\|id>` | Add a plugin to this machine; `--node <n>` also pushes it into a node |
| `baton plugin list [--node <n>]` | The plugins on this machine, or in one node |
| `baton plugin remove <id>` | Remove a plugin |

## More

`baton --help` also lists commands for joining agents across machines,
sharing resources inside a network, and running a standby control plane. They
are outside the scope of this guide for `0.x`.

Commands for connecting agents to each other — messaging between agents — are a
**preview** in `0.1`. They are there to try, but they are not part of what `0.1`
promises, and they may change or go away.
