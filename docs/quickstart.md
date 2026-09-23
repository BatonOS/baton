# Quickstart

This walks through the everyday life of one agent: create it, leave it running,
check on it, restart it without losing work, save it, bring it back, copy it,
move it to another machine, and remove it.

You need the `baton` command installed — see [Install](install.md).

## 1. Check this machine

```bash
baton doctor
```

`doctor` checks that Docker and Compose are reachable and that the ports Baton
uses are free, and tells you what to fix if they are not. Nothing is created
yet. It does not check the Docker version; Baton needs Docker Engine 26 or
later — see [Install](install.md).

## 2. Create an agent

```bash
baton agent create --name coder --template claude-code
```

This creates an agent called `coder` and the **node** it runs on: an isolated
container with its own filesystem, workspace and identity.

Run it as root. `claude-code` gives each node its own disk image and
loop-mounts it, which needs root on Linux; as any other user this step stops
before creating anything and says `node.volumeSize is 20G, and executing it
needs root`.

If this machine has never run Baton before, the same command also founds a
**network** for it — the local control plane that keeps track of your nodes —
and tells you that it did.

By default that network listens on this machine only. If you will later move
agents to another machine (step 10), the network has to be reachable from
there, and that is decided now, when it is founded:

```bash
baton agent create --name coder --template claude-code \
  --bind <interface> --advertise-url https://<address>:8443
```

`<address>` is a name or IP the other machine can reach; it is written into the
network's certificate. `--bind` is the interface the control plane listens on.

`--template` picks what runs inside the node. `claude-code` ships with Baton,
so you can name it on a machine that has never run Baton before. To see the
templates stored on this machine:

```bash
baton template list
```

On a fresh machine this can list nothing even though the templates that ship
with Baton can still be named.

If a template declares credentials — an API key, say — see which ones it
needs, then fill each one from a file on this machine. Credentials are never
passed as a value on the command line:

```bash
baton agent secret list coder
baton agent secret set coder <NAME>
```

You can also supply them at creation time with `--secret <NAME>=<file>`, once per
credential.

## 3. Leave it running

A node keeps running after the command that created it exits. Close your
terminal, log out — the agent is still there.

## 4. See how it is doing

```bash
baton status        # containers, nodes, and whether the control plane answers
baton agents        # which agents exist, and where each one runs
baton logs coder -f # the node's output, following it across restarts
```

To watch the agent's own terminal without touching it:

```bash
baton attach coder
```

`attach` is read-only. To take the keyboard, add `--takeover`: it is exclusive,
and it is recorded.

## 5. Restart without losing work

```bash
baton restart coder
```

Restarting stops and starts the node's container. The workspace lives on the
node's own volume, so what the agent was working on is still there afterwards.

To move the node onto a newer image while keeping its workspace and identity:

```bash
baton rebuild coder
```

## 6. Save it

```bash
baton snapshot coder                       # writes coder-<timestamp>.tar.gz
baton snapshot coder --out ./coder.tar.gz  # or choose the file yourself
```

A snapshot captures the node's portable state — its workspace and the runtime's
own state. It never contains the node's identity: identity belongs to the node,
not to its data.

**Treat a snapshot file as a secret.** The runtime's own state includes whatever
it is logged into, so anyone who can read a snapshot can act as that agent
wherever it was signed in. Baton writes the file readable only by its owner
(mode 600); keep it that way, and do not share it the way you would share code.

A snapshot does not carry the node's data volume: after a restore that volume
starts empty and the runtime refills it.

## 7. Put it back

```bash
baton restore coder --from ./coder.tar.gz --yes
```

`restore` puts a snapshot's state back into a node, replacing what is there now
— which is why it asks for `--yes`. The node keeps its own identity. Restart the
node afterwards so the runtime reads what it was given:

```bash
baton restart coder
```

## 8. Make a copy to experiment on

```bash
baton clone coder coder-experiment
```

The copy starts from the same state on a new node, with **its own** identity.
Changing one does not change the other.

## 9. Remove it

```bash
baton destroy coder-experiment --yes
```

`destroy` removes the node, its volume, and its identity. This cannot be
undone, so it asks for `--yes`. Take a snapshot first if you might want the work
back, or add `--keep-data` to remove the node but keep its volume.

## 10. Move it to another machine

Before the first move, three things must be true:

- **The network is reachable from the destination** — it was founded with
  `--advertise-url` (see step 2).
- **The destination is ready**: Docker Engine 26 or later, `bash`, and the
  `baton` command installed; and this machine can reach it over SSH with a key,
  not a password.
- **The destination has declared itself** to the network. On the destination:

  ```bash
  baton agent create --name <n> --no-network --template claude-code \
    --labels driver=container,host=<destination host>
  baton agent join --name <n> --network https://<address>:8443
  ```

  and on this machine, admit it:

  ```bash
  baton --master https://<address>:8443 network add --agent <n>
  ```

Then move the agent:

```bash
baton --master https://<address>:8443 migrate --name coder --to ops@second-host --yes
```

`<destination host>` must be written exactly as it appears after `@` in `--to`.

Both machines must already be in the same network — migration moves an agent
between machines inside one network, and Baton refuses the move otherwise,
telling you to join first. Run `migrate` on the machine the agent is on now,
and name the destination with `--to`.

The agent keeps its address: whatever address reached `coder` before the
move reaches it after, because what moves is the agent, not the node: the source node is
revoked, the destination builds a new node from the same template and applies
under the same name, enrols with a **new key, a new certificate and a new node
id**, and the identity is rebound to it. The private key never travels. The
inbox is untouched — messages sent before, during and after the move are read
from the same queue.

The order is enforced, not merely recommended: the destination cannot enrol
until the source is revoked, because the master refuses a name that is still
held.

If the move fails partway, the source is revoked and the destination does not
yet exist. Baton says so rather than hiding it: the source's container is
stopped but **its volumes are kept**, and the archive stays in the staging
directory — its path is printed — until the restore on the destination
succeeds. Only then is the source destroyed. Run `migrate` again toward the
source host to come back.

**What this is, in `0.1`:** moving between **two machines you run yourself**,
over SSH. Baton does not offer hosting — there is no managed node to move to,
and no command that provisions one for you. Hosted nodes are not part of
`0.1`.

## Next

- [Concepts](concepts.md) — what an agent, a node and a network are
- [CLI reference](cli.md) — everything else the `baton` command can do
