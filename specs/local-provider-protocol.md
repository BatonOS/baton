# The local Provider protocol

What Core and the local Provider say to each other. The Provider is a CLI-side
process with its own durable operation log, and Core asks it to execute or
report each step by `operation_id`. Two verbs keyed
by `operation_id` — execute and query — and one that is ⊘ keyed by anything,
because it has no side effect to be idempotent about: **environment facts** —
paths resolved on the Provider's side, which Core only compares. The fact names
are this local implementation's, not a long-term contract.

- **Transport:** HTTP/1.1, JSON bodies, over a unix socket at
  `<data-dir>/provider/run/provider.sock`. The directory `run/` — never the
  socket file itself — is bind-mounted into Core's container and into nothing
  else, so that no workload, including an agent, has a path it could dial. The
  path must be at most **107 bytes**; the Provider
  refuses to start otherwise, because Node binds a longer path truncated and
  reports success.
- **Implementations:** the Provider is `baton provider serve`
  (`apps/cli/src/provider/`). ⊘ This is not a runtime driver interface: one
  Provider, one consumer.

## Execute

```
POST /v1/operations/{operation_id}
{"step": "capture" | "place" | "enroll" | "materialize" | "file.write" | "test.run"
       | "provision" | "runtime.read",
 "subject": "<who this step is being run for>",
 "nonce": "<minted by Core for this dispatch>",
 "params": { … per step, strict … }}
```

| step | params |
|---|---|
| `capture` | `{source}` — the node whose state is captured |
| `place` | `{source, name}` — create `name` as a copy of `source`'s shape |
| `enroll` | `{name}` — `name` applies to this network |
| `materialize` | `{name, archive_operation_id}` — restore the archive the `capture` operation with that id produced |
| `provision` | `{name, harness, placement}` — create an EMPTY workspace; ⊘ no project and ⊘ no enrolment (Dogfood v1 §三) |
| `runtime.read` | `{workspace}` — read the runtime state of the one workspace this Provider serves |
| `file.write` | `{workspace, path, content, expected_sha_before}` — write only if the file still has that sha |
| `test.run` | `{workspace, path}` — run the test at `path` |

*`provision` and `runtime.read` landed with Dogfood v1 and this table was ⊘
updated with them; `file.write` and `test.run` were named in the enum and never
given a row. Corrected 2026-09-20, in the commit that added the third verb —
`scripts/check-step-name-twin.sh` compares Core's dispatched names against the
Provider's enum and has never read this page, so the drift was silent.*
| `file.write` | `{workspace, path, content, expected_sha_before}` — replace a file in the served workspace, only if it still has `expected_sha_before` (CAS) |
| `test.run` | `{workspace, path}` — run a `*.py` test file in the served workspace with `python3` |

`file.write` and `test.run` act on the one workspace the Provider serves
(`baton provider serve --workspace-dir <dir>`; its id is the directory's name).
Each execution first appends `<step> <operation_id>` to
`<workspace>/.baton/executions.log` (fsynced) — the world-side record a test
counts executions by, never the Provider's own log (Architecture Proof §四).

The Provider:

1. **Has a result for this id in its log** → returns it, `answered_from: "log"`.
   The step does not run again, and the `nonce` in the answer is the one
   recorded when it first ran.
2. **Has an intent and no result** (it died mid-step) → `status: "unknown"`,
   `answered_from: "log"`. The step does not run again: it may already have
   done its side effect. *(Asking the world instead — label, application,
   generation — is reconciliation, P1.)*
3. **Has nothing** → records the intent (fsynced), runs the step, and:
   - `succeeded` — recorded;
   - `failed` — recorded, for a refusal known to come before the step touched
     anything, or for a step that ran to the end and whose answer is no (a
     test that ran and failed);
   - `unknown` — **not** recorded, for every other error.

An id recorded as one step and asked for as another → `409 OPERATION_ID_REUSED`.
A body it cannot read → `400`, and nothing is recorded.

## Query

```
GET /v1/operations/{operation_id}
```

The recorded answer (cases 1 and 2 above), or — when the log has no record of
the id — an answer, not a status code:

```json
{"status": "not_found", "operation_id": "op_…", "provider_instance_id": "pi_…"}
```

⚠ **Never a bare 404.** A wrong path, a wrong method, a Provider that is not
running and a socket answered by another process all give 404; read as "not
executed", each would be dispatched again. "No record" is evidence only when
it is this body, it echoes the id asked about, and it names the same instance
as before (Architecture Proof §五, 前提二 · 前提三).

When the log has no record but a request for the id **has arrived and is still
in the Provider's hands** — its body being read, or queued behind another
operation — the answer says so instead:

```json
{"status": "received_no_result", "operation_id": "op_…", "provider_instance_id": "pi_…"}
```

`not_found` there would be less than the Provider knows, and the asker would
send a second request for an operation whose first is alive. The asker reads
`received_no_result` as unknown and does not send again.

This is the Provider's **in-memory** state, and it dies with the process: after
a restart the same id is `not_found`, and that is then true — the intent is
written before any side effect, and nothing that was waiting got to it. It is
never rebuilt from `requests.log`, which is the measuring side's record of
arrivals (Architecture Proof §四, D1) and not read by the Provider.

`subject` is **required**, and the local Provider does ⊘ read it today. ★ The
field belongs to the PROTOCOL, ⊘ to whoever happens to use it: a schema that
accepted a request without it would let Core stop sending it and nobody would
find out.

*Measured 2026-09-20, by the scan rather than by hitting it: this request did ⊘
carry `subject`, while the App framework's schema declared it with
`.default('')`. ⇒ An App's `ctx.subject` — one of the three things the
framework hands a developer — was the **empty string**, silently, and the
developer would have written it into his evidence. ☆ A value nobody produces is
a silent off switch, and **a default value is how it stays silent**.*

A request counts as received when it arrives — before its body is read and
before it waits its turn — and `requests.log` gets its `received` line then.

## Environment facts

```
POST /v1/environment/facts
{"destination": "<a path, as the proposal gave it>"}     ← optional
```

```json
{"facts": [
  {"name": "workspace_boundary",   "value": "/srv/ws",
   "source": "provider:local/pi_… --workspace-dir", "resolved_at": "2026-09-20T…Z"},
  {"name": "destination_resolved", "value": "/etc/x",
   "source": "provider:local/pi_… realpath",        "resolved_at": "2026-09-20T…Z"},
  {"name": "harnesses_available",  "value": "[\"alpha\",\"beta\"]",
   "source": "provider:local/pi_… templates",       "resolved_at": "2026-09-20T…Z"}
]}
```

★ **The authoritative resolution lives on this side, and the whole fact is
produced here** — ⊘ only its value. Core receives facts already resolved and
only COMPARES them; it holds no ability to resolve a path and must not acquire
one. A `source` Core filled in would be Core's claim
about who resolved the path rather than the resolver's own signature, which is
why Core refuses a fact whose `source` is empty or `system`.

- `workspace_boundary` is present **only** when the Provider was started with
  `--workspace-dir`. Its absence is the honest answer "I serve no workspace",
  and Core reads it as the condition being **uncomputable**, ⊘ as a boundary of
  `/` that everything is inside.
- `harnesses_available` is **always** present when the Provider has a data
  directory: which harnesses it can start, i.e. the Workspace Templates
  `provision` can find by name (readable, and the only template with that
  name). Its `value` is a string holding a JSON array of strings in ONE
  canonical form — sorted, deduplicated, no whitespace (`["alpha","beta"]`);
  an empty set is `"[]"`, which is a fact, ⊘ an absence. If the directory
  cannot be listed the fact is left out. Core parses the array and checks
  membership on the parsed list — never by substring — and refuses a
  submission whose `harness` is not in it; a missing or non-canonical value is
  read as "could not ask", and that is refused too (the opposite of
  `workspace_boundary` below, on purpose: not knowing here would let an unknown
  runtime run all the way to a built container).
- `destination_resolved` is present only when a `destination` was asked for.
  Core needs **both or neither**: with one of them it would be comparing a
  resolved path against nothing.
- Canonicalisation handles a path that does not exist yet — realpath the
  longest existing ancestor, then re-attach the components below it. ⚠ It
  climbs **only on ENOENT**: any other errno answers `400 UNRESOLVABLE` rather
  than climbing, because climbing past an unreadable `/ws/link` would re-attach
  the component literally and answer `/ws/link` — i.e. *inside* — for a path
  whose realpath nobody was able to look at.
- ⊘ On the operation log: nothing is recorded and asking twice is free.
  Recording it would make `requests.log` — D1's ruler for whether Core sends an
  operation twice — count questions as work.

## Answer

```json
{"operation_id": "op_…", "step": "enroll", "status": "succeeded",
 "nonce": "…", "evidence": {"node": "copy-a", "request_id": "jr_…"},
 "answered_from": "execution", "provider_instance_id": "pi_…"}
```

`status` is one of three values and no fourth; `unknown` is an answer, never an
error.

★★ **`provider_instance_id` is filled by an App executor too, and the name is
the local Provider's only for history.** Its definition is **the identity of
the log that answered**, whoever answered; the VALUE says which kind:

| value | who answered |
|---|---|
| `pi_…` | the local Provider's log |
| `ai_…` | an App executor's log |

⊙ Same shape this contract already uses for `steps[].executor`: one neutral
field, values that carry their own prefix. ⚠ It was ⊘ renamed, and the reason
is measured rather than economical: the name appears in 14 files and 58 places,
including `openapi.yaml` and **a verification report that has already been
signed off** — a signed piece of evidence citing a field name would then
point at a field that no longer exists. ☆ An already-judged cell can only be
made dirty afterwards, never clean.

*Measured 2026-09-20 (archive `ee28ea7d`): the App framework first called it
`app_instance_id` — a better name, and the third blocker of the same family.
Core read `provider_instance_id`, got nothing, and answered `it names no
provider instance`, so the step was never sent. ★ The Provider-specific
assumption was hiding **in a field name**.*

`provider_instance_id` is on every answer, executed or read from the log. It is
minted once per data directory (`<data-dir>/provider/instance-id`) and kept
beside the log, so it changes exactly when the log does: a Provider restarted
on another data directory says "no record" about every id, and the changed id
is how the asker knows that answer is not evidence.

## What each step's evidence says

| step | evidence | why it is there |
|---|---|---|
| `capture` | `archive`, `bytes` | `materialize` restores exactly this archive, by id, after `capture`'s call has returned |
| `place` | `container`, `label` | the node carries `dev.baton.operation-id=<id>` on its container and volume |
| `enroll` | `request_id` | **the application this call lodged.** `await_enrolled` watches this id and never the name: a same-name application from an earlier clone may still be pending, and a `409` about it is `failed`, never success |
| `materialize` | `from_operation_id` | which capture was restored |
| `file.write` | `path`, `sha_before`, `sha_after` | the CAS held: the file had the sha the Harness read, and now has the new content's |
| `test.run` | `path`, `exit_code`, `stdout_tail`, `stdout_chars`, `stderr_tail`, `stderr_chars`, `output_dir` | the tails are the last 4096 characters; `*_chars` is the full length, so a reader can tell a tail that is the whole output from one that was cut. The whole output is in `output_dir` (`stdout`, `stderr`), kept whether the test passed, failed or did not finish |
