# BATON node channel protocol — `v1alpha1`

The long-lived link between an agent (or standby) and the control plane.

**Transport:** WebSocket over TLS with mutual authentication, established by
the node to `wss://<control>/api/v1alpha1/agent/channel` on port 8443.

**Why WebSocket and not gRPC:** a single port that survives corporate proxies
and reverse proxies, no protobuf toolchain in the agent, and frames a human can
read in a log during an incident. The cost — sequencing, replay detection, and
flow control have to be specified rather than inherited — is paid explicitly
below.

## Direction of establishment

The node dials out. The control plane never connects to a node.

This is not a preference; it is what allows a laptop behind NAT and a NAS
behind a home router to participate without port forwarding, and it means a
compromised control plane cannot reach into a network it was never given a path
into.

## Frame envelope

Every frame is a single JSON text message:

```json
{
  "v": 1,
  "type": "heartbeat",
  "msg_id": "01J8...",
  "seq": 42,
  "ts": "2026-08-04T20:00:00Z",
  "payload": {}
}
```

| Field | Meaning |
|---|---|
| `v` | Envelope version. A node that receives an unknown `v` closes the connection with `1002` rather than guessing. |
| `type` | Frame type, from the table below. Unknown types are ignored by the receiver and logged — this is what lets a newer peer add a frame type without breaking an older one. |
| `msg_id` | Unique per frame. Used to correlate a response to its request. |
| `seq` | Monotonic counter, node→control only. See *Sequencing*. |
| `ts` | RFC3339 UTC at the sender. |
| `payload` | Type-specific object. |

Maximum frame size is **1 MiB**. A capability returning more than that must
return a reference, not the bytes.

## Sequencing

`seq` starts at 1 on a fresh enrollment and increments by exactly 1 per
node→control frame. It persists across reconnects — a reconnecting node
continues its sequence and states where it resumed from in `hello`.

The control plane rejects a frame whose `seq` is not `last + 1`:

- **lower or equal** — a replay or a confused client. The control plane writes
  a `channel.seq_violation` event and closes the connection. It does not
  process the frame.
- **higher** — frames were lost. The control plane accepts the frame, records
  the gap in the event log, and continues. Losing heartbeats is survivable;
  silently accepting replays is not.

Control→node frames are not sequenced; they are correlated by `msg_id`.

## Frame types

### `hello` — node → control

First frame after the connection opens.

```json
{
  "agent_version": "0.1.0-rc.1",
  "platform": "linux",
  "arch": "amd64",
  "resume_from_seq": 41,
  "leader_epoch": 3,
  "capabilities": [
    {
      "name": "sys.echo",
      "version": "1.0.0",
      "risk": "low",
      "input_schema": {"type": "object"},
      "output_schema": {"type": "object"},
      "allow_from": ["local"]
    }
  ],
  "remote_shell": false,
  "workspace": {
    "name": "codex01",
    "image": "batonos/agent-node:0.1.0",
    "type": "codex",
    "session": "tty",
    "enterable": true,
    "skills_mount_path": "/workspace/.skills"
  }
}
```

`remote_shell` is whether this node allows the operator's remote
shell. It is a **top-level** field, not part of `workspace`: the shell is the
node's own (the daemon spawns it, PID 1), so it exists even on a node that
supervises nothing, where `workspace` is absent. **The node is the authority** —
it enforces the switch in `pty_open` and refuses when off; the control plane
caches this only to fail a forbidden `/pty` fast. Default `false`: a node that
never enabled it refuses the shell. It is re-stated on every `heartbeat` so a
live local toggle (`baton agent shell <name> on|off`) reaches the control plane
within one beat rather than only on the next reconnect.

The capability list is a full declaration, not a delta: what is absent has been
withdrawn. The control plane diffs it against the registry, and **preserves any
`disabled` flag an operator set** — a node reconnecting is not permission to
re-enable what a human switched off.

`runtime` describes what this agent-node supervises. **It is absent on a node
that supervises nothing**, which is a legitimate way to run and must not be
shown as a runtime in a bad state.

| Field | Meaning |
|---|---|
| `name` | the runtime's own name, from its spec |
| `image` | the agent-node image it was started from |
| `type` | runtime family, e.g. `codex` — advisory, never branched on |
| `session` | `tty` or `headless` |
| `enterable` | whether an interactive session can reach it — `true` only for `tty` |
| `skills_mount_path` | where a skill can be unpacked — `adapter.skills.mountPath` from the spec, **`""` when the spec declares none** |

It carries **identity, never arguments**. argv is where an operator ends up
putting a path, a project name, or occasionally something that should not
travel, and the control plane has no use for it.

`enterable` is the field a client branches on to decide whether `baton attach`
can work at all, so a reimplementation of this protocol has to send it.

`skills_mount_path` is sent **whether or not it is set**, and the empty string
is the load-bearing value: it is how the control plane learns that this node
has nowhere to put a skill, and refuses the install instead of accepting one
that can only fail on the far side. A reimplementation that omits the field
when it has no path is telling the control plane "nothing is known here", which
is a different sentence.

Sent once per connection, not per heartbeat: what a runtime *is* changes only
when its spec does.

`leader_epoch` is the highest epoch this node has ever seen a master present in
a `hello_ack` — the epoch that fences masters: every promotion increments it,
and a stale one is refused. `0` means this node has never seen one — a fresh node, or
a daemon older than this field. **`0` is not a claim about anything.**

**It travels so that a superseded master can find out.** The epoch used to flow
one way only, master → node, and the node fenced alone: it saw a stale epoch,
refused the connection and hung up. The node knew; the one party who needed to
know did not. A master replaced while it was down — or whose operator never ran
the third command of a transfer — went on accepting writes with nothing to tell
it otherwise. Both addressing paths (a DNS TXT record, the cloud registry) are
eventually consistent, so a client reaching the old seat is not an anomaly but a
certainty while the record propagates, which is what makes the old master's own
refusal part of correctness rather than a backstop.

**A reimplementer should send it and expect nothing back.** It is a report, not a
demand: the master may act on it or ignore it, and there is no reply frame and no
error for it.

> ⚠ **A master must not act on this field alone.** Any node holding a valid
> client certificate can put any integer here, so a master that stepped down
> because one node said so would be handing every node the ability to close the
> cluster's write gate — an authenticated denial of service, and a worse defect
> than the one the field exists to fix. BATON's control plane combines it with a
> fact no node can write: whether that master itself ever signed a
> `master-transfer-offer`. An epoch above its own is acted on only where its own
> records say it handed the seat away; otherwise it is recorded, surfaced, and
> **not** acted on.

### `hello_ack` — control → node

```json
{
  "heartbeat_interval_sec": 15,
  "leader_epoch": 1,
  "accepted_capabilities": ["sys.echo@1.0.0"],
  "rejected": [{"name": "fs.write", "reason": "risk 'blocked' requires explicit registration"}],
  "identities": ["reviewer", "coder"]
}
```

A rejected capability is named with a reason. Silent rejection would leave a
node advertising something that can never be called, with nothing to look at.

`identities` (optional) are the agent identities bound to this node; the
control plane binds at most one.
A node needs them for one decision: a runtime that did not say whose mailbox it
is opening can be served when exactly one identity is bound here, and must be
refused when several are.

**Absent and empty are different answers.** Absent means the control plane could
not read them and the node has not been told; empty means it was told, and none
are bound. An implementation that collapses them makes the second answer for the
first.

**Do not infer this from which mailboxes have mail.** Those two facts coincide
right up until an identity is bound and nothing has been written to it yet —
the ordinary state just after binding — and that is exactly when the inference
hands a runtime the other identity's message. It is sent here because inferring
it was tried and measured wrong.

A binding made while the node is connected is not pushed; the node learns it on
the next connect. Until then, a runtime that names no identity may still open
the previously bound mailbox — an accepted window, confined to one owner's own
mailboxes and closed by reconnecting.

### `heartbeat` — node → control

```json
{
  "uptime_sec": 3600,
  "inflight_calls": 0,
  "cert_not_after": "2026-08-05T20:00:00Z",
  "remote_shell": false,
  "workspace": {
    "state": "running",
    "runtime_status": "not-declared",
    "restart_count": 0,
    "started_at": "2026-08-05T19:02:11Z",
    "last_exit_code": 137,
    "oom_killed": false,
    "last_error": "",
    "probe": "passing",
    "memory": "2Gi"
  },
  "instruction_blocks": [
    { "plugin": "baton.claude-code", "path": "/home/agent/.claude/CLAUDE.md",
      "sha256": "9f2b…" }
  ]
}
```

Sent every `heartbeat_interval_sec`. The node reports its own certificate
expiry so that a clock disagreement between the two sides is visible in the
data rather than surfacing later as an unexplained handshake failure.

`workspace` is the node's current scene, and is **absent when the
node supervises nothing**. An absent `workspace` is not a report of absence: a
control plane must leave any stored runtime state alone rather than clearing it.

**`state` and `runtime_status` are two different kinds of claim and must not be
merged.**

| | `state` — observed | `runtime_status` — reported |
|---|---|---|
| Source | the node daemon reading a process | the runtime's own claim about itself |
| Values | `pending` `starting` `running` `restarting` `exited` `failed` `stopped` `paused` | `not-declared` `unknown` `idle` `working` `needs-input` `done` `errored` |
| Standing | fact | assertion |

`instruction_blocks` is which plugin instruction blocks are in place on this
node, **by SHA-256 digest only**, so each new block is audited once and the
audit log never carries its text. Absent when there are none.

It is a **state, not an event queue**, and a reimplementer must build it that
way. The node keeps no pending list and waits for no acknowledgement: it
reports what is in place on every beat, and the control plane recognises the
ones it has not seen before. A queue would have to survive a crash between
writing a block and reporting it; this cannot lose one, because the block is
still there on the next beat.

**The digest is sent; the block never is.** An audit trail that carried the
text would become a second way to read it — a path around the gate that refuses
an untrusted plugin's block in the first place. A block whose bytes change has
a different digest and is therefore a different block, which is what an audit
of *what was put in front of the agent* has to record.

**`errored` and `failed` are on different rows on purpose.** `state: failed` is
the observation that the process exited non-zero with no restart left;
`runtime_status: errored` is the runtime's claim that a ROUND failed. A runtime
can report `errored` while its process is happily `running`, and can report
`done` and then be killed. A reader must not have to remember which column they
are in to tell two identical spellings apart — so they are not identical.

**`probe` is a third kind of claim, and it is the one a renderer may act on.**

| value | means |
|---|---|
| `""` | nothing was reported — an older node daemon, or a runtime not asked yet |
| `not-declared` | the spec asked for no probe |
| `pending` | a probe is declared and has produced no verdict yet |
| `passing` / `failing` | it ran, and this is what it said |

`state` says the process is alive; `probe` says what it serves is working. **A
process can be alive and useless**, which is the whole reason a probe exists —
so `probe` may only ever lower a rendered health, never raise it.

**`memory` is a measurement, not an echo.** It is the ceiling in force, read by
the node daemon from inside the very node the ceiling applies to — never the
value the spec asked for. Reporting the request back would describe a limit that
was silently dropped exactly as confidently as one that took effect, and those
two being indistinguishable is what the field exists to end.

| value | means |
|---|---|
| `"2Gi"` | that ceiling is in force, in BATON's notation and never the engine's |
| `"unlimited"` | there is no ceiling — a real answer |
| absent | the node could not determine it |

**Absent is not `unlimited`.** A ceiling rendered as "none" when the answer was
never obtained is the same class of bug as an unreported probe rendered green.

**Four of the five are not a pass — which is not the same as unhealthy.** A
consumer that folds silence into green renders a failed probe as fine, which
`docs/architecture/first-principles.md` and CLAUDE.md rule 7 call a bug; a
consumer that folds "nothing to check" into red calls a working node broken.
Both are wrong and only the first one is famous.

**The rule (PM ruling, 2026-08-13 — the CLI's implementation is the standard):**
the probe may only ever LOWER a rendered health. `failing` lowers it, `pending`
makes it `starting`, and **`not-declared` lowers nothing** — a workspace running
with no probe declared is a legitimate way to run. A workspace that is not
running has already lowered the verdict before the probe is consulted, so
`not-declared` on a dead workspace is not green either, and that is the state's
doing rather than the probe's.

As with `runtime_status`, an unrecognised value is recorded rather than
believed — see each field for which value it falls back to.

`runtime_status` carries **two silences, and they are not interchangeable**:

| value | means |
|---|---|
| `not-declared` | the runtime's spec declares no status bridge. **This is knowledge**: no claim will ever arrive. A STRUCTURAL absence |
| `unknown` | a bridge is declared and nothing is known right now — it has not spoken yet, or what it said stopped being true when it exited or was paused. A TEMPORAL absence |
| `idle` | alive, with no round running. **Not the same as `done`** — `done` means a round finished and there is something to look at |
| `working` | a round is running |
| `needs-input` | stuck waiting for a person |
| `done` | the round finished |
| `errored` | the round failed (the process may still be running) |

**Folding the two silences together is a defect, not a simplification.** "It
does not support this" and "it has not said yet" call for different responses,
and a reader who is told `unknown` about a runtime BATON knows will never report
has been told something false. `probe` splits the same way, for the same reason.

**A consumer must never render a silence as `idle`, and must never infer status
from CPU load.** An unrecognised value is recorded as **`unknown`** rather than
stored verbatim — and never as `not-declared`, which would assert knowledge on
the strength of a word that was not understood. A node is not trusted to invent
states. BATON cannot see whether an opaque runtime is busy or idle; activity is
only what the runtime itself reports.

**`runtime_status` is a claim, and a claim has an author who may be gone.** Once
a node is no longer online, every `runtime_*` value is a sentence from a process
this control plane can no longer reach — the one party able to correct it is the
one that is absent. A consumer **must not render any of them as current** while
the node is offline. The control plane does not clear them: the last reading is
a fact that happened, and blanking it would trade a stale truth for an invented
gap.

`last_exit_code` follows the shell convention for a signalled death — 128 plus
the signal number, so a SIGKILL is 137. `oom_killed` is set only when the
kernel's own cgroup counter says so, never inferred from the signal: `kill -9`,
an escalated stop, and the OOM killer are indistinguishable from `waitpid`, and
a wrong cause in front of someone debugging is worse than none.

`paused` is distinct from `stopped`. A paused runtime still holds its memory,
its open files, and its connections; a stopped one is gone. The two call for
opposite responses.

### `heartbeat_ack` — control → node

```json
{"server_ts": "2026-08-04T20:00:00Z", "next_expected_seq": 43}
```

`server_ts` lets the node detect clock drift against the control plane.

### `call_request` — control → node

```json
{
  "call_id": "c_01J8...",
  "capability": "sys.echo",
  "version": "1.0.0",
  "input": {"message": "hello"},
  "deadline_ms": 30000,
  "trace_id": "t_01J8..."
}
```

A node that is paused, or that does not have the capability enabled, responds
with a `call_result` carrying the appropriate error code. It does not close the
connection and it does not stay silent.

### `call_result` — node → control

```json
{
  "call_id": "c_01J8...",
  "status": "succeeded",
  "output": {"message": "hello"},
  "duration_ms": 3
}
```

or

```json
{
  "call_id": "c_01J8...",
  "status": "failed",
  "error_code": "CAPABILITY_PAUSED",
  "error_message": "node is paused by operator",
  "duration_ms": 0
}
```

### `console_request` / `console_response` — control ↔ node

```json
{"request_id": "r_01J8...", "command": "runtime.pause", "args": {}}
```

```json
{
  "request_id": "r_01J8...",
  "result": "ok",
  "output": "runtime paused (SIGSTOP): it holds its memory, files, and connections but is not executing.",
  "data": {}
}
```

`result` is one of `ok`, `denied`, or `error`. A node with no runtime answers
**`denied`** to a `runtime.*` verb — reporting success for an operation that
reached nothing is how someone comes to believe a runtime is paused when there
is no runtime.

The command set is closed, and **each verb names the layer it acts on**:

| Scope | Verbs | Acts on |
|---|---|---|
| read | `status` · `capabilities` · `tasks` | — |
| node | `pause` · `resume` · `restart` | the daemon's channel and capability calls |
| runtime | `runtime.pause` · `runtime.resume` · `runtime.restart` | the supervised runtime, via the supervisor |

The scope is in the **verb**, never in `args`. An earlier draft of this document
showed `{"command": "pause", "args": {"scope": "remote"}}`; that design was
considered and rejected, because a scope hidden in a flag needs a default, and
either default misleads half the people who type it.

`runtime.pause` is SIGSTOP to the runtime's process group. `runtime.restart`
outranks the spec's restart policy — the policy describes what to do about
failures nobody asked for — and resumes a paused runtime first, since a stopped
process cannot act on SIGTERM.

There is no frame that carries an arbitrary command string — that is
`baton shell`, which is audited as a high-risk action, not this.

### `cert_rotate_required` — control → node

```json
{"reason": "expiring", "grace_sec": 3600}
```

The node rotates **over the existing connection** and only then reconnects if
it must. Rotating by dropping the link first would turn a routine certificate
refresh into a reconnect storm across every node at once.

### `revoked` — control → node

```json
{"reason": "revoked by operator"}
```

Terminal. The node writes a local revocation marker and exits. On restart it
reads that marker and refuses to re-enroll, so a node whose supervisor restarts
it automatically cannot loop its way back into the network.

### `goodbye` — node → control

```json
{"reason": "agent stopping"}
```

Sent by a node that is shutting down on purpose, immediately before it stops
reading. The node then enters `draining`; the control plane writes a
`channel.goodbye` event and closes the connection with `1000`.

It is advisory, and its value is in the event log rather than in timing:
**a clean goodbye lets the control plane record a shutdown instead of inferring
a failure from silence.** A node that dies without sending one is not in error
— that is the case the heartbeat timeout exists for, and the node is marked
`offline` by the same path either way.

### `changed` — control → node

```json
{"kind": "skills"}
{"kind": "endpoint", "endpoint": "https://newhost:8443"}
{"kind": "contacts"}
```

**The one "something you hold changed, go look" frame** (it
replaced the former `skills_changed` and `endpoint_changed` when a third
notify kind arrived; a frame type, once shipped, exists forever, so the family
generalised instead of growing). A nudge carries nothing beyond the kind: an
archive is bounded at 64 MiB decompressed, a frame at 1 MiB — the bytes travel
the node face's HTTPS instead.

- `kind: "skills"` — the node's skill set changed; fetch over the node face
  (`GET /agent/skills`, `GET /agent/skills/{id}/archive`), verify the digest,
  extract.
- `kind: "endpoint"` — the control plane moved to another host; the old master
  sends this to its connected nodes before it demotes, and `endpoint` is the
  new entry point. The node rewrites what it stored at enrolment and redials.
- `kind: "contacts"` — the node's address-book projection changed; re-pull
  `GET /agent/contacts`, the address book (member projection plus the grants
  this node's identities hold).

**`kind` is a CLOSED enum, and an unknown kind is refused** — logged loudly,
acted on never. This protocol has no backward compatibility: a receiver that
silently tolerates unknown kinds is a receiver whose behaviour every
implementation guesses differently, which is worse than a loud refusal on both
ends.

### `pty_open` / `pty_data` / `pty_resize` / `pty_close` — an interactive shell

```json
{"session": "pty-6f1c…", "cols": 120, "rows": 40}          // pty_open   control → node
{"session": "pty-6f1c…", "data": "bHMgLWwK"}               // pty_data   both directions
{"session": "pty-6f1c…", "cols": 100, "rows": 30}          // pty_resize control → node
{"session": "pty-6f1c…", "reason": "operator left"}        // pty_close  both directions
```

The node opens a **PTY running a shell inside itself** — the daemon is PID 1
there and spawns the shell directly — and streams it over the channel it
already holds. It opens no port and ships no sshd; the node still
never listens.

**A node may refuse this.** `pty_open` is answered with `pty_close`
and no shell is spawned when the node's remote-shell switch is off (its default).
The node is the authority — this check holds even if the control plane's cached
`remote_shell` is stale — and the switch is reported in `hello`/`heartbeat`.

- `session` is minted by the control plane per terminal, so one node can serve
  more than one and the relay never crosses them.
- `data` is **base64**: the channel is one JSON-frame stream, and a binary
  side-channel would be a second wire protocol for one feature.
- Write is gated by the takeover lease the control plane holds, so two
  operators cannot type into one terminal. A viewer's input is dropped, not
  refused — read mode is a mode, not an error.

**This is the exception to "no arbitrary command execution" below, and it is a
deliberate one.** `console_request` remains a closed verb set; `pty_*` is a
human at a terminal, authenticated as an operator, holding an exclusive lease,
and every session is audited. What is bounded is *who*, not *what they type* —
a shell that only ran approved commands would not be a shell.

### `message_deliver` — control → node

```json
{
  "message_id": "msg_8392",
  "source_agent": "leader",
  "source_network": "net_7f93a2b1c4d5e6f7",
  "destination_agent": "reviewer",
  "destination_network": "net_7f93a2b1c4d5e6f7",
  "thread_id": "msg_8390",
  "reply_to": "msg_8390",
  "type": "task",
  "source_address": "sales.leader@example.org",
  "created_at": "2026-08-09T14:00:00Z",
  "expires_at": "2026-08-16T14:00:00Z",
  "content_type": "text/plain",
  "payload_size": 21,
  "payload": "cGxlYXNlIHJldmlldyBQUiAjMTMy"
}
```

`thread_id` is always present — a message that answers nothing is its own
thread. `reply_to` is present only on an answer, and `type` only when the
sender said one; **`type` carries no execution meaning and the control plane
never branches on it**.

`via` and `via_isolated` are present only on a message a
facility's service sent — `"via": "baton.slack"`, with `via_isolated` saying
whether the sending node could vouch for the stamp (`true`: the service's uid
differed from the runtime's; `false`: same uid, `via` is a label). Carried as
the sending node stamped them; the runtime reading its mailbox sees both.

`for` is the sender's half of that pair: the facility in
the RECIPIENT's office the message is for. The receiving daemon delivers a
`for: baton.slack` message into that service's own inbox; when no such service
is running there it acks with `reason: "no facility baton.slack running on
this node"` and the message stays queued — never the occupant's mailbox.

One message for a runtime this node hosts, addressed by **identity** rather than
by node: the control plane resolves `destination_agent` to whichever node the
identity is bound to at the moment of delivery, so a message sent before a
migration and delivered after it still arrives.

**What a node does after acknowledging it.** The frame is unchanged
by this paragraph — no field, no type, no new frame — but the observable
behaviour is part of the protocol, so a reimplementation that skipped it would
differ from this one in a way nothing on the wire would reveal:

- The node stores the message, acknowledges it, and **then**, if the runtime's
  spec declares `adapter.inbox.onMessage`, runs that command. A spec that
  declares nothing runs nothing — that is the default and it is not a
  degradation.
- The trigger is **the mailbox being non-empty, not this frame arriving.** The
  command is retried while mail is still waiting (5s, doubling, capped at 5m; a
  new arrival resets it) and stops when the mailbox drains. An implementation
  that fired once per frame would lose the wake in four ordinary situations —
  the runtime was busy, it started and did not take the message, it crashed on
  start, or the daemon restarted between arrival and wake — and in each of them
  the message would sit unread until some LATER message happened to arrive.
- **The command is given nothing about the message.** No payload, no sender, no
  id, on argv or in the environment. The node hands mail over; it does not read
  it — BATON carries a message but never reads or interprets its content.
- **Waking is running that command, never telling the runtime.** There is no
  path from a delivery into a model's context: for an interactive runtime the
  only mechanism would be writing to its terminal, which accepts input only from
  a human who has taken it over, exclusively and on the audit record; and
  a retried notice would accumulate in the context as text the model then reads
  as fact. This is also what makes retrying safe — a process run twice does not
  know about the first run.

The `message_ack` still means only "these bytes arrived and are durable here".
It does not mean the runtime read them, and none of the above changes that: the
ack is sent before the command runs, and its success or failure is not reported
back to the control plane.

**An address is an agent AND a network** (D-32 §27). Both networks are always
present — in the example above they are the same value, because that message is
between two agents on one network. **Local delivery is not a special case**, and
an empty field meaning "here" would be the invariant this shape exists to
remove, hiding in a blank.

`source_address` is present only when the sender is on another network **and**
this network recorded what that network is called (in the delivery grant it
issued that sender). It is the canonical address,
`<network>.<agent>@<domain>` — the network's hosted name with the agent's name
inserted before the `@` — and it is what a reply is sent to.

It is a second field beside `source_network` rather than a better spelling of
it, because they answer different questions and have different provenance:

| field | answers | comes from | absent means |
|---|---|---|---|
| `source_network` | who signed this | the signature, verified | never absent |
| `source_address` | where a reply goes | the receiver's own record of the peer | not repliable |

So a node renders the sender in one of three ways, and the third must stay
visibly different from the second:

| | rendered | |
|---|---|---|
| local (`source_network == destination_network`) | `alice` | reachable by name |
| foreign, named | `sales.alice@example.org` | repliable |
| foreign, no name recorded | `alice@net_5003ecf01cbb426b` | readable, **not** repliable |

**`source_address` is never filled in from `source_network`.** A registration
number in an address field would make every message look answerable and fail at
the moment of answering, rather than at the moment of reading.

Deciding all of this from fields that are already on the frame is the point:
**the delivery path must never parse an address out of a string** — resolution
belongs to a resolver, and messaging asks it rather than doing it. The control plane holds the grant that records the peer's name; the
node renders what it was handed.

**Every field except `payload` is envelope.** The control plane reads, indexes,
orders and audits those. `payload` is opaque: base64 on the wire, carried and
handed over, never parsed, never logged, never written into a snapshot or an
audit body. Bounded
by the 1 MiB frame limit like everything else here.

**Reserved envelope fields**, fixed in the envelope before they were
implemented. Of the four it reserved, **two are now on the wire** — `thread_id`
and `reply_to`, which landed with threads (migration 0032); `type`, which the
envelope declared from the start rather than reserved, is on the wire as well
(migration 0045). Their shapes are in the frame examples above. Two remain
reserved and **not yet on the wire**:

```json
{
  "correlation_id": "req_77",
  "metadata": { "source": "baton" }
}
```

They are written here before they are implemented because what becomes expensive
is the **shape**, not the code: a message id that is not stable across
federation, a thread that turns out to mean workflow, or a `type` the control
plane started acting on are all things that cannot be taken back. The Go frame
structs do not carry these two yet — **a reader implementing against this
document today should treat them as absent, not as optional-and-empty**, and
the two `protocol.go` files gain them in the same commit as the behaviour.

Two of them carry a rule rather than a format:

- **`thread_id` is correlation and nothing else** — not a workflow, a team, a
  channel, a project, a memory or a task. A runtime may read it as a
  conversation, as a job, or ignore it.
- **`type` has no execution meaning.** `type: request` does not cause anything
  to run. The runtime decides; an unknown type must be forwardable unchanged.
  The day the control plane branches on it, it has started deciding who should
  act on what — and it will look like a routing optimisation.

A third-party node implementation needs to do exactly two things with this
frame: give the bytes to its runtime, and reply.

How the reference implementation does the first half, for anyone reimplementing
it: the daemon writes the message to `<data>/inbox/<id>.json` under a temporary
name and renames it, so a runtime watching the directory never sees half a
message. The runtime **asks** for it (`baton-agent inbox next`), which moves it
to `<data>/inbox/read/`. Taking a message sends nothing back over this
connection, and there is no frame here for it to send: that a message was *read*
is not something the protocol can carry, because a message read and ignored and
a message read and acted on are the same event at this layer. An agent that
wants to report progress does it the only honest way — by sending a message.

### `message_send` — node → control

```json
{"to": "leader", "content_type": "text/plain", "payload": "REVJX0NPTVBMRVRF",
 "message_id": "msg_0190f3c1-6a2e-7c11-9f0a-2b7d5e4c8a91",
 "reply_to": "msg_8392", "type": "status", "identity": "reviewer"}
```

`to` is who it is for: a bare agent name on **this** network, or the canonical
address of an agent on another — `<network>.<agent>@<domain>`, the network's
hosted name with the agent's name inserted before the `@`; for
example `dns.coder@batonnetwork.org`. **One field and one grammar**, the same
one the operator face takes: two spellings would mean an agent and a person
address the same recipient differently, and one of them would be wrong wherever
it was written down.

The `@` tells the two apart and cannot be ambiguous, because an agent name may
not contain one — the same rule that makes the cross-network marker on a
recipient's disk unforgeable. One character, two jobs, one
reason.

A cross-network `to` is matched against relationships **already established**
and is never resolved at send time, so addressing a domain
this network has no relationship with is answered `NO_DELIVERY_GRANT` — a
refusal, not a lookup. A delivery path that resolves is one where whoever can
answer for a name decides where the message goes.

Cross-network refusals a runtime may see here are the destination's own words,
passed through rather than translated: `NOT_ADMITTED` (that network has not
endorsed us to write to that agent), `RECIPIENT_UNKNOWN` (the network is right
and the agent is not), `SIGNATURE_INVALID`, `NETWORK_UNREACHABLE` (transient),
`ATTACHMENTS_NOT_RELAYED` (this delivery has attachments and that network is
offline — the mailbox stores the envelope, not the files).

That last one is the only refusal here whose two answers differ, and the
difference is the point: **terminal for the mailbox leg, transient for the
message.** Sending it to the mailbox again will always fail; the same message
goes through when that network is online, because the direct leg carries
attachment bytes. A sender told only "failed" would retry the one that cannot
work.

`reply_to` (optional) names the message this one answers; the control plane
derives the thread from it, so a node cannot place a message into a
conversation by asserting a thread id. `type` (optional, 128 bytes at most) is
the sender's label, carried and never interpreted.

`message_id` is minted by the **node daemon**, written into the outbox file
before the frame goes out, and reused on every re-send. Re-sending a
message the control plane already accepted is answered with the **same ack**
rather than creating a second message — which is what lets a node hold a message
in its queue until the ack arrives instead of discarding it on send.

It is **not** minted by the runtime. The outbox's `local_id` is the name of the
file the runtime wrote, so it is runtime-chosen: two runtimes can pick the same
string, and one can pick another agent'"'"'s deliberately.

A message a node's **runtime** asked to send. Sequenced like any other
node-originated frame, and carrying a `msg_id` because an answer is coming.

**There is no `sender` field, and adding one would be a mistake** — and neither
`message_id` nor `identity` is one. The control plane fills the sender from an
identity bound to the connection this frame arrived on, so an agent can address
a message but cannot sign one as somebody else.

`identity` (optional) says **which** of them. The control plane binds at most
one identity to a node, so today there is only ever one name it can select.
The difference from a `sender` field is the whole point: the control plane
refuses a name that is not bound to this connection, so the frame **selects
among names it has already verified** rather than introducing one.

Its two absent cases are not the same:

| bound to this node | `identity` absent | why |
|---|---|---|
| one | that one is used | nothing to disambiguate; every single-identity node keeps working unchanged |
| several | cannot occur | the control plane binds at most one identity to a node, so there is never a second name to choose between |
| none | `node:<name>` | a node with no agent identity still has a name, and a message from it is attributable to the node |

The receiving agent's decision about whether to trust an instruction rests
on a name the control plane wrote — that property is worth more than any field a
node could supply.  The `node:<name>` row above is that
rule: "an agent nobody has named yet cannot report a failure" is exactly
backwards.

This is the frame that closes the loop. Delivery alone gives an agent no way to
say what it did, and the control plane may not infer it — a message read and
ignored and a message read and acted on are the same event at the transport
layer. So
progress is reported the only honest way: by something that knows what it did
saying so.

In the reference implementation the runtime writes a file to `<data>/outbox/`
and the daemon carries it, which is what lets an agent report a finished task
while the control plane is restarting underneath it.

`via` (string) and `via_isolated` (boolean) are stamped by the node daemon
from WHICH outbox directory it read the letter — the occupant's (no `via`) or
a facility service's own `BATON_OUTBOX_DIR` (`via` = the plugin id) — and
from the uid measurement it made when it started that service.
A runtime cannot set them: they are not in the outbox file format, and a
letter written into a service's outbox by another process carries the
service's `via` with `via_isolated: false` when the uids are shared. The
control plane stores both as sent and drops `via_isolated` when there is no
`via`.
`for` is the sender's: `baton-inbox send --for <id>`, or copied from the
original's `via` on a `--reply-to`. Carried, not interpreted.

### `message_send_ack` — control → node

```json
{"message_id": "msg_8393"}
```

```json
{"error": "IDENTITY_NOT_FOUND", "reason": "no agent is registered under that name"}
```

A refusal is **final**, not a transport error: an unknown recipient does not
become known by being asked again, and a node that retried would turn one
mistake into permanent load while hiding it from the runtime that made it.

### Attachments — a manifest here, the bytes over HTTP

`message_deliver` and `message_send` may carry `attachments`: a list, in the
order the sender chose, of

```json
{"name": "report.pdf", "content_type": "application/pdf", "size": 812345,
 "sha256": "9f2c…"}
```

**The bytes are not in the frame and never will be.** An attachment is up to
10 MiB and a frame is bounded at 1 MiB, so they are fetched over the node face:

```
POST /agent/messages/attachments                       → {"sha256", "size"}
GET  /agent/messages/{message_id}/attachments/{idx}    → the bytes
```

Same face, same reason as a skill archive. `sha256` is both the integrity check
and the address — one value, so the two cannot disagree.

**The name is in the envelope on purpose.** A recipient can see what they were
sent — its name, its type, how big it is — without fetching anything, and
without the sender having to describe it in the payload.

**Two ceilings, and which applies depends on where the message is going**:
10 MiB within a network, 2 MiB across one. The check happens when the message is
SENT, not when the bytes are uploaded — an upload does not yet know who the
message is for. So an attachment can upload, send locally, and be refused when a
message carrying it is addressed to another network; that refusal names the
destination, because the reader's next action is to send it elsewhere rather
than to shrink the file.

**A manifest can outlive its bytes.** Attachment bytes are deliberately not
carried in a standby snapshot, so a control plane that was transferred keeps the
list and loses the files. That is `410 ATTACHMENT_BYTES_GONE`, and it is not
`404`: a reader told 404 concludes there never was an attachment.

### `message_ack` — node → control

```json
{"message_id": "msg_8392"}
```

```json
{"message_id": "msg_8392", "reason": "no runtime"}
```

Confirms the bytes arrived. **It is not a statement that the runtime read them**
— that happens on the far side of a boundary the control plane does not cross,
and a protocol that claimed otherwise would be claiming to know something it
cannot observe.

`reason` names a node-side refusal (no runtime, payload too large). It is
envelope data: a refusal explains itself in terms of the node's own state,
never in terms of what the message said.

**Acking twice is legal.** An ack whose response was lost gets retried, and
failing the retry would strand a message that did in fact arrive.

## Reconnection

Exponential backoff starting at 1s, doubling to a 60s ceiling, with **±20%
jitter**. The jitter is not decoration: without it, every node that lost a
control plane reconnects in the same instant it returns, and the recovery
itself becomes the next outage.

A node is marked `offline` after **3 missed heartbeat intervals**. Marking
`offline` records a state change; it never triggers an action on its own.

## Errors and close codes

| Close code | Meaning |
|---|---|
| `1000` | Normal shutdown (node draining, or control plane stopping) |
| `1002` | Protocol violation — unknown envelope version, malformed frame |
| `1008` | Policy violation — sequence violation, revoked node |
| `1011` | Control plane internal error |

Error codes carried in `call_result`:

| Code | Meaning |
|---|---|
| `CAPABILITY_NOT_FOUND` | Not advertised by this node |
| `CAPABILITY_DISABLED` | Registered but switched off by an operator |
| `CAPABILITY_PAUSED` | Node is paused via console |
| `CAPABILITY_TIMEOUT` | Exceeded `deadline_ms` |
| `INPUT_INVALID` | Failed the declared input schema |
| `OUTPUT_TOO_LARGE` | Result exceeded the frame limit |
| `EXECUTION_FAILED` | The capability itself failed |
| `NODE_OFFLINE` | The node holds no open channel, so the call was never delivered. Produced by the control plane rather than by a node — the only code in this table that a node cannot emit |

Error codes produced when addressing an identity:

| Code | Meaning |
|---|---|
| `IDENTITY_NOT_FOUND` | No agent by that name. Distinct from the next row on purpose: "nobody by that name" and "known, currently unreachable" need different answers from a sender |
| `IDENTITY_UNBOUND` | The address exists and is not currently placed on a node. **The message is queued, not lost** — this is a status, not a failure, and it is the normal case during a restart or a migration |

## What this protocol deliberately does not do

- **No agent→agent *data plane*.** Direct paths between nodes — streams, file
  transfer, RPC — do not tunnel through the control channel. What does travel
  here is `message_deliver`: a bounded, store-and-forward envelope addressed by
  identity. **Carrying a message is not the same as connecting two agents**, and
  the distinction is the line between delivering a message, which BATON owns,
  and acting on its meaning, which it never does.
- **No routing decisions.** The protocol resolves an address the sender wrote.
  It has no frame for asking *which* agent should receive something, and adding
  one would move the boundary rather than extend the protocol.
- **No message semantics.** `payload` is bytes. There is no type field for
  workflow states, no status enum a sender can rely on the control plane to
  interpret — those live inside the payload, where BATON cannot see them.
- **No file transfer.** Frames are bounded at 1 MiB and results reference
  artifacts rather than carrying them. A skill archive is fetched over the
  node-face HTTPS after a `changed{kind:"skills"}` nudge, and shared resources are read
  over that same face with the member certificate — the node certificate the target network
  issued — neither crosses
  this channel.
- **No arbitrary command execution *by the control plane*.** `call_request`
  invokes a declared capability and `console_request` is a closed verb set:
  neither carries a command string. **`pty_*` is the one place a shell is
  reached**, and it is a person at a terminal — an operator certificate, an
  exclusive lease, an audited session — not the control plane deciding to run
  something. The bound is on *who*, not on what they type.
