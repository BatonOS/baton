# Concepts

## The pieces

```
Agent          Claude Code · Codex · …        what does the work
                     │ runs inside
Node           one workspace · one terminal · one identity
                     │ managed by
Baton          create · watch · attach · snapshot · restore · clone · destroy
                     │ runs nodes on
Provider       Docker today
```

**Agent** — the thing that does the work: an AI coding tool such as Claude Code
or Codex. Baton does not look inside it and does not decide what it does.

**Node** — one isolated place for one agent to live: its own filesystem, its own
workspace, its own dependencies, its own identity, and a terminal a person can
walk into. Most commands take a node's name.

**Template** — the recipe for a node: which runtime it carries, what it needs
(credentials, resources), and which plugins come with it. `baton template list`
shows the ones on this machine.

**Network** — the set of nodes one control plane looks after. The first
`baton agent create` on a machine founds one. Agents on other machines can
apply to join, and the network decides whether to admit them.

**Provider** — what actually runs a node. Today that is Docker on this machine.

## What Baton does not do

- It does not read what your agent is thinking or decide what it should do.
- It does not move identities around. A snapshot, a restore or a clone carries
  state; each node keeps an identity of its own.

## Identity

Every node has an identity that belongs to it alone and never travels in a
snapshot. That is why:

- `restore` puts state back into a node but keeps that node's identity;
- `clone` gives the copy a new identity;
- `destroy` removes the identity along with the node.

## Watching versus taking over

`baton attach <node>` shows the agent's terminal read-only. `--takeover` hands
you the keyboard: only one person holds it at a time, and it is recorded.
