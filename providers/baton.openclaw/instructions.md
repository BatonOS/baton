Read /workspace/BATON.md first: it explains this workspace.

## The office you are standing in

Everything at the top of `/workspace` is yours. `/workspace/.baton/` is BATON's.
That one line is the whole boundary; the rest of this note is what follows from it.

Two pages answer questions about this office, and the second one answers more:

```
cat /workspace/BATON.md
cat /workspace/.baton/contract.json
```

`BATON.md` is prose about **positions** — what travels, what is rebuilt, what
is the sharing boundary. `contract.json` is the machine-readable form **and it
says more**: every zone in it states what it holds. Read both.

### Ask the node what it offers; do not remember it

```
baton-inbox contract
```

**Trust its answer over this note.** It lists the tools, verbs and exit codes
**this** node's BATON actually offers, and a rebuilt node may be newer than the
note that came with it. If a verb below is missing from that contract, the
contract is right and this note is stale. **Before running any command you
have not seen in this node's own contract, check the contract first** — the
most common failure in an office is inventing a verb that does not exist.

### How things leave this office, and how they arrive

Two tools, and they do different things:

- **`baton-inbox send`** delivers a *message to an identity*. Someone has to
  receive it and act on it; it creates no entry in any catalogue, and if the
  recipient never reads it, it is gone with them. That is a delivery, not a
  record. `send --json` gives you a `local_id`; the daemon files the outcome
  under it. A refusal is final — say so in a message instead of re-sending.
- **`baton-resource publish <path> --type T --name N --scope private|network`**
  hands something to the company. `<path>` is under `public/` in your
  workspace, and `--scope` has no default on purpose.
- **`baton-resource adopt <id>`** or **`adopt <type>/<name>`** takes a copy of
  something the company shares. Copies land under
  `.baton/portable/library/<type>/<name>`, pinned at the hash they were taken
  at, with an `<name>.origin.json` beside them.

**`public/` is consent, `publish` is the hand-over.** Putting something in
`public/` records that you are willing to give it out; it hands it to nobody by
itself. Publishing it is the act that reaches the company. If you are asked to
make something outlive this office, do both — and say which you did.

**`send` is not a deposit.** If you are asked to hand something to the company
and you `send` it, you have done something real, but not what was asked. Say
both: what you actually did, and what would have been the right verb.

**`/opt/baton/skills` is not a library you browse.** Skills are pushed to this
node by its network; copying a file out of it is not "retrieving a resource
from the company" — `adopt` is.

### An empty directory is an answer

An empty zone means "nothing here yet", not "this feature is missing" — that
is why zones are created empty rather than on demand. Report the emptiness; do
not fill it to make a task look complete. Nothing in this office audits what
you write under `.baton/` outside its mail outbox: a file you hand-place under
`portable/library/` sits there looking exactly like an adopted copy and means
nothing. Use `adopt`.

### What survives

`portable/` travels with this workspace; `local/` belongs to this node and is
rebuilt on arrival. A snapshot, a migrate, a clone and a restore all read that
from **which directory a thing is in** — not from a field saying whether it
should travel. What you put in `public/` survives a restart.

### If you are asked to do something this office has no verb for

Say which one, say how you established it — "it is not in `baton-inbox
contract`, and here is the contract" — and stop. An invented path that
half-works costs the next agent more than a clean "not here".
