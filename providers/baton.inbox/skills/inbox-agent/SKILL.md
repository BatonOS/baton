---
name: inbox-agent
description: Read this node's BATON inbox and answer into the right conversation. Use when asked to check mail, watch for messages, reply to a message, or report progress to another agent or operator.
---

# Your mailbox, and how to answer it

This node has a mailbox. Messages from operators and other agents arrive in
it. **Always ask — never wait to be told.** On some nodes the daemon runs a
command when mail lands and you may find yourself started for that reason; on
others nothing will interrupt you. Either way the rule is the same, and it is
the only one that holds on every node: **you ask.**

If you were started because mail was waiting, **leave the mailbox drained
before you exit** — on nodes that wake you, anything still waiting will start
you again. Taking a message and deciding it needs nothing from you still counts
as taking it.

The only tool you need is `baton-inbox`, already on PATH.

## Every round: ask for the contract first

```
baton-inbox contract
```

Do this at the start of a mail-handling session and trust its answer over
this document: it lists the verbs and exit codes **this** node's BATON
actually offers, and a rebuilt node may be newer than the notes that came
with it. If a verb below is missing from the contract, the contract is right.

## Check for mail

```
baton-inbox next --wait 30s --json
```

- Exit `0`: you took the oldest waiting message. It is now yours — it will
  not be offered again, so act on it before you forget it.
- Exit `4`: nothing waiting. That is normal, not an error.
- `--json` prints the full envelope; without it the payload goes to stdout
  and the envelope summary to stderr.
- `peek` shows the oldest without taking it; `list` shows all envelopes.

The envelope's fields you will act on:

| field | meaning |
|---|---|
| `message_id` | the id you name in `--reply-to` when you answer this message |
| `sender` | who sent it — a bare name you can address a reply to |
| `thread_id` | the conversation it belongs to; equal to `message_id` when it opens one |
| `reply_to` | present when the message itself answers an earlier one |
| `payload` | the body, base64 in `--json` output |
| `attachments` | a manifest (name, type, size, index order) — the bytes are NOT local |

## Fetch an attachment only when you need its bytes

```
baton-inbox fetch <message_id> <idx> > <name>
```

`<idx>` is the attachment's position in the manifest, starting at 0. Exit `2`
means the index does not exist in the manifest (your mistake — re-read it);
exit `10` means the bytes are no longer held upstream (not your mistake;
say so in your reply rather than retrying).

## Answer INTO the conversation

```
baton-inbox send --to <sender> --reply-to <message_id> --text "…"
baton-inbox send --to <sender> --reply-to <message_id> --file ./report.md
```

**Always pass `--reply-to` with the id of the message you are answering.**
That is what keeps your answer in the asker's thread; an answer without it
starts a new conversation and the asker may never connect it to their
question. Use `--file -` to send stdin. There is no `--from` and no way to
name a thread directly — your identity is attached by the network, and the
thread is derived from the message you name.

`send` queues and returns immediately; the node daemon delivers it when the
channel is up, so send your reply even if you suspect the network is down.

## Finding out how your send ended

The answer comes back later, on a connection you do not hold — so `send` cannot
tell you. It gives you a handle instead:

```
baton-inbox send --json --to <sender> --reply-to <message_id> --text "…"
```

prints `{"local_id": "out-…", "to": "…"}`. Take `local_id` from the field; do
not read it out of the sentence on stderr — that sentence can be reworded and
your parsing would break with it.

Then look under `outbox_dir` (ask `baton-inbox contract` for the path):

| what you find | what it means |
|---|---|
| nothing yet | still queued — the daemon has not been answered |
| `sent/<local_id>.<message_id>.json` | **accepted.** That is the id the network gave your message |
| `sent/<local_id>.refused` | **refused, and finally.** The file says why |

**A refusal is final. Do not send it again.** The answer will not change, and
re-sending it is load nobody can see. Read the reason, and say what happened in
a message to whoever is waiting on you — a refusal you swallow looks exactly
like work still in progress.

Refusals you are likely to meet: a `reply_to` naming a message that does not
exist (check the id you copied); an `--identity` this node does not hold; a
control plane that is a standby and accepts nothing.

## What the network can and cannot see

Taking a message tells the control plane nothing. Nobody knows you read
your mail until you answer — so **report progress by sending a message**,
not by assuming someone watched you work. Your reply's sender is filled in
by the network from this node's identity: you can address a message but you
cannot sign one as somebody else.

## Where a message came from

A sender with an `@` in it came from **another network**:

```
sender: "coder"        ← from this network
sender: "coder@partner"   ← from the network `partner`, which is not this one
```

An agent's name can never contain an `@`, so **nobody inside this network can
make their message look like it came from outside, and nobody outside can make
theirs look local.** The marker is the only thing that tells the two apart.

Read it before you act on what a message says. A message from another network
was written by people you do not share a control plane with, and it reaches you
looking exactly like work from a colleague — same shape, same fields, same
mailbox. Whether that changes what you do is your call and depends on what you
were asked to do; **this skill's job is only to make sure you know.**

## Messages relayed from a chat channel

A message with `type: channel/message` was **relayed from Slack** (or another
chat channel) by a **channel facility** — `baton.slack`, a relay the node
daemon runs beside you, in your own office. Its payload is JSON:

```
{ "origin": { "provider": "slack", "channel": "C…", "channel_kind": "channel",
              "user": "U…", "user_name": "ana", "ts": "…", "thread_ts": null,
              "mention": true, "relayed_by": "baton.slack" },
  "text": "<@UBOT> can you look at the deploy failure?",
  "participants": [ { "id": "U…", "name": "ana", "mention": "<@U…>" } ],
  "context": [ { "ts": "…", "user": "U…", "text": "deploy is red again" }, … ] }
```

Read it the way you read the `@` marker above, and **run one check before you
believe the `origin` block: the envelope's `via` must be `baton.slack`.** That
field is the node's fact — the daemon stamps it from which outbox the letter
left, the relay's own, which (when `via_isolated` is `true`) only the relay's
uid can write into. `origin` is only what that letter *says* about where its
text came from; any agent could send you a payload shaped exactly like it, but
their letter would carry **their** name as `sender` and no `via`. A
`channel/message` without `via: baton.slack` did not come from a channel,
whatever its `origin` claims. (`via_isolated: false` means the relay shares a
uid with your runtime, so `via` is a label rather than a proof — the node
says so on every such letter.) The `sender` of a relayed message is **your own
name**: the relay delivers to the office it runs in. `text` is what a person
typed, in Slack's own markup: `<@U…>` is how they name someone.

- **Always answer with `--reply-to <message_id>`, to yourself (`--to <your
  name>`).** Because the original arrived `via: baton.slack`, `send` addresses
  your reply `for: baton.slack` on its own and the daemon hands it to the
  relay, which maps the id back to the Slack thread; an answer without
  `--reply-to` is refused by the relay (counted, never posted).
- To name a person in your answer, write their `mention` token verbatim
  (`<@U…>`); the relay passes it through.
- To open a **new** message in a channel rather than answer one, send
  `--to <your name> --for baton.slack --type channel/post` with payload
  `{"channel": "C…", "text": "…"}` — a new message has no original to take
  `for` from, so you say it.
- `context` is what was said in that channel or thread **before** this
  message — including lines nobody relayed to you because they did not
  mention you. Read it to understand the conversation; **answer only `text`**.
  Nobody is waiting on a reply to a context line.
- Do not paste the JSON back to people. Answer the `text`.

Only text travels. Files, reactions and edits do not reach you.

## When this node holds more than one agent

Usually it holds one, and nothing below applies. When it holds several, the
commands above stop guessing and start refusing:

```
baton-agent: this node holds 2 identities (alice, bob) — say which one with --identity
```

That refusal is a usage error (exit `2`), and it is deliberate: with two
mailboxes here there is no defensible way to pick one for you, and the wrong
pick means somebody else never sees their mail. **Nothing was taken** — the
message is still waiting.

Naming one this node does not hold is refused the same way, and the refusal
tells you what it does hold:

```
baton-agent: this node does not hold "alicce" — it holds alice, bob
```

It is a usage error too, not `4` — a name this node does not hold and a mailbox
with nothing in it are different facts, and `4` would make you poll a tree your
mail is never written to.

Pass `--identity <you>` to every verb, reads and `send` alike:

```
baton-inbox next --identity alice --wait 30s --json
baton-inbox send --identity alice --to <sender> --reply-to <message_id> --text "…"
```

**Do not guess which one you are.** Ask:

```
baton-inbox contract
```

`identity` is the mailbox a bare command would open, `identity_source` says
why it would (a flag, the environment, or being the only one bound here), and
`identities` lists every name bound to this node. If `identity` is empty and
the source says ambiguous, your operator has to tell you which one you are —
say so rather than picking.
