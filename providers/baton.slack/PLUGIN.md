# Baton Slack

**Purpose:** put the occupant of this office on Slack. One Slack app is
relayed into this node's own inbox, and the occupant's replies are posted
back into Slack. The occupant never learns the Slack API; this facility never
reads what it carries.

**Provides**

- a **service**: the relay (`facility/bin/baton-slack.cjs`), one
  long-lived process holding a Socket Mode connection, supervised by the node
  daemon **beside** the runtime — not a runtime, not a second node

**Keeps** (under the home the daemon gives this service,
`/var/lib/baton/plugins/services/baton.slack/home/`; deleted with the service
on `plugin remove`)

- `threads/` — where each relayed message came from, so a reply finds its thread
- `seen/` — the idempotence key per Slack message
- `users/` — a 24 h cache of user names
- `context/` — the room as heard, per channel and per thread, 20 lines / 24 h
- `state/counters.json` — the only readout; it counts envelopes, never content

---

## This is a facility, not a node

Until 2026-09-04 this was the runtime of a separate **channel node**, and a
template (`slack-channel`) named the agent it delivered to. Both are gone:
one Slack app is one office with this line in its template —

```yaml
plugins:
  - baton.claude-code
  - baton.inbox
  - baton.slack
```

— and the relay delivers to **the identity of the office it runs in**. There
is no `config.agent`. Nobody lives in the relay; the occupant lives in the
runtime beside it, and `baton attach` reaches the occupant, not the relay.
The relay writes its log to stderr, every line prefixed `[baton.slack]`, and
the daemon carries it into the node's container output (`baton logs <node>`);
its state is `baton node show` → `plugins[].service`.

One Slack app = one office with one `baton.slack`: one Slack app serves exactly
one agent.

## Where this facility ends

This facility defines **one edge**: one Slack app ↔ this node's own mailbox.
Everything past that edge is core mail, the same verbs every node uses:

| Leg | Who defines it |
|---|---|
| Slack → this node's mailbox, this service's outbox → Slack | **this facility** (the relay) |
| the relay ↔ the occupant, in one office | **core mail** — the daemon stamps `via: baton.slack` on what leaves the relay's outbox, and delivers what the occupant addresses `for: baton.slack` into the relay's own inbox |
| the occupant → any other agent | **core mail** — never here |
| how the occupant works its mail | **`baton.inbox`** (a skill), not here |

When the occupant talks to another agent, that traffic does not pass through
the relay: nothing is posted, nothing is counted, and this relay cannot see
it. The only bytes that reach Slack are the ones the occupant sent with
`--reply-to` to a relayed message, or with `--type channel/post`. Swap this
facility for another channel's and the agent-to-agent leg is untouched; the
rule cuts the other way too — a channel facility does not get to define how
agents reach each other. Agent identity, its resolution and message delivery
between agents belong to BATON's core, and a manifest has no field
that could say otherwise (its sections are `runtime` or `service`,
`instructions`, `state`, `skills`, `hooks`; none is a route). The
`baton.inbox` page draws the same three lines from the mailbox side.

## Setting up the Slack app

1. Create an app at api.slack.com. **Socket Mode: on.**
2. App-level token with the `connections:write` scope → `xapp-…`. This is
   `SLACK_APP_TOKEN`.
3. Bot token scopes: `app_mentions:read`, `chat:write`, `im:history`,
   `channels:history`, `users:read`. Install to the workspace → `xoxb-…`. This
   is `SLACK_BOT_TOKEN`.
4. Event subscriptions (bot events): `app_mention`, `message.im`,
   `message.channels`.
5. **App Home → Show Tabs → Messages Tab: on**, with "Allow users to send
   Slash commands and messages from the messages tab" ticked. Without it a
   person opening a DM with the app sees "Sending messages to this app has
   been turned off" — the first real-Slack run hit exactly this.
6. Invite the app into every channel it should hear (`/invite @App`).

Put each token in its own file, one line, and hand the files to `create` the
way every other secret is handed over — the two tokens are listed by
`baton template list` beside the runtime's own, with `plugin: baton.slack`:

    baton template show claude-code > agent-a.yaml       # add `- baton.slack` under plugins:
    baton agent create --name agent-a --template ./agent-a.yaml \
        --secret SLACK_BOT_TOKEN=./bot.token --secret SLACK_APP_TOKEN=./app.token
    baton agent join --name agent-a --network <master-url>

or fill them in after the office is open: `baton agent secret set agent-a
SLACK_BOT_TOKEN < bot.token` (the panel's credential card does this).

Tokens are files, never arguments and never environment variables. The daemon
copies each to a file the relay's own uid can read before every start of the
service, and the relay re-reads them on every connection attempt: to rotate,
replace the file and `baton restart agent-a`.

**Adding Slack to an office that already exists means making that office
again**: the tokens are read-only binds decided at create,
a running container cannot be given a new mount, and `baton rebuild` re-creates
the container from the manifest that already exists — it swaps the image, not
the contract. So `baton plugin install baton.slack --node agent-a` refuses, and
the path it names is:

    baton snapshot agent-a                      # pack the work up first
    baton destroy agent-a --yes
    baton agent create --name agent-a --template ./agent-a.yaml \
        --secret SLACK_BOT_TOKEN=./bot.token --secret SLACK_APP_TOKEN=./app.token
    baton agent join --name agent-a --network <master-url>

Removing needs none of that: `baton plugin remove baton.slack --node agent-a`
stops the relay, waits for it to exit, and deletes its home; the occupant's
runtime is not touched. A facility whose service declares **no** secrets also
installs into a running node with no re-creation.

## What lands in the occupant's inbox

A message whose **sender is this node's own identity** and whose envelope
carries **`via: baton.slack`** — stamped by the daemon because the letter left
this service's outbox, not the occupant's — with `type: channel/message` and a
JSON payload:

```json
{
  "origin": {
    "provider": "slack", "team": "T…", "channel": "C…", "channel_kind": "channel",
    "user": "U…", "user_name": "ana", "ts": "1725000000.000100",
    "thread_ts": null, "mention": true, "relayed_by": "baton.slack"
  },
  "text": "<@UBOT> can you look at the deploy failure?",
  "participants": [{ "id": "U…", "name": "ana", "mention": "<@U…>" }],
  "context": [
    { "ts": "…", "user": "U…", "text": "deploy is red again", "thread_ts": null },
    { "ts": "…", "user": "UBOT", "text": "<@U…> looking", "thread_ts": "…", "self": true }
  ]
}
```

`context` is **the room as this relay heard it**, oldest first, not including
the message itself: for a top-level channel message, the last 20 lines said in
that channel in the last 24 h — **including lines that were never relayed**
because nobody mentioned the app; for a message inside a thread, the whole
thread (fetched from Slack at delivery, up to 200 lines) preceded by those
channel lines. Empty for a DM. It is the same kind of claim as `origin`:
what the relay says was said. Read it to understand; **answer only the
message** — the lines in `context` were deliberately not delivered, and
nobody is waiting on them.

`origin` is **what this relay says**. `via` is what the **node** says: the
daemon stamps it from which outbox the letter left, and the envelope's
`via_isolated` says how strong that is — `true` when the relay runs as its own
uid (the shipped manifest asks for `65533`) so the occupant cannot write into
the relay's outbox; `false` when the two share a uid and `via` is a label.
**The check the occupant can run is "`via == baton.slack`"**; a letter another
agent sends it, however its payload is shaped, carries that agent's name as
sender and no `via`. Treat `origin.user` the way you treat a sender's own
description of themselves.

## What is relayed, and what is only heard

Relayed into the inbox:

- direct messages to the app
- channel messages that @-mention the app
- follow-ups in a thread the app is already in (no fresh @ needed). "Already
  in" means both doors: a thread it was called into, **and a thread it opened
  itself with `channel/post`** — a person answering under the app's own
  message is answering the app (operator, 2026-09-02).

Heard and **not** relayed: every other channel message. They are acknowledged,
counted (`dropped_unaddressed`) and kept in the channel's context window
(`context/`, 20 lines, 24 h) so that the next message that IS relayed there
carries them as `context` — the occupant reads the conversation it was called
into without being woken for every line (operator, 2026-09-02).

## How the occupant answers

- **Reply into the thread it came from:** `baton-inbox send --reply-to <message_id> --file -`.
  Because the original arrived `via: baton.slack`, `send` addresses the reply
  `for: baton.slack` on its own, and the daemon delivers it into the relay's
  inbox rather than the occupant's. The relay maps the id
  back to the Slack thread (a DM reply is a plain DM). `<@U…>` in the text is
  passed through verbatim — that is how you address a person.
- **Open a new message in a channel:** `--for baton.slack --type channel/post`
  with payload `{"channel": "C…", "text": "…"}`. A new message has no original
  to take `for` from, so it is said explicitly.
- Anything else that reaches the relay's inbox (no `--reply-to`, not a
  `channel/post`) is **not posted**. It is counted (`refused_outbound`) and
  its envelope is logged; the body is not.

Text longer than Slack's 40 000-character limit is cut and marked. Attachments
are not carried in either direction (Phase 0).

## What the node reports (`baton node show` → `plugins[].service`)

| field | meaning |
|---|---|
| `status` | `pending` · `starting` · `running` · `restarting` · `exited` · `failed` · `stopped` · `unknown` (process there, probe not answering) · `refused (door)` (this facility did not come through a trusted door, so the daemon did not start it) — the daemon's word, from the process and the probe |
| `reason` | non-empty for `failed` / `unknown` / `refused` — a missing token mount says which |
| `isolation` | `isolated` · `shared` · `unknown` — measured when the service starts; what `via_isolated` on every relayed envelope is set from |
| `outbox` · `pid` | where its outgoing mail leaves from; the process, while there is one |

The relay's own account of what it is doing — `needs-input` (a token file is
missing or empty, or this office has not joined a network yet, so there is
nobody to deliver to), `idle`, `working`, `errored` (socket down, reconnecting
with backoff) — is in its log, not in a status word of its own: a service has
no status bridge: the daemon derives a service's status from the process and
its health probe.

Health (`probe: exec`) reads the heartbeat the relay touches only while a
socket is in service: **alive and not connected is unhealthy**, not green —
`status: unknown` with the probe's reason.

## Phase 0 limits

- **Who may make the occupant work: everyone in the Slack workspace.**
  There is no allowlist here and none is planned here — the
  receive-but-do-not-act switch lives in the Inbox policy, a per-recipient policy in the control plane that
  decides whose mail an agent acts on (held mail is received but not delivered;
  `channels: hold` holds every letter that arrived `via` a channel facility,
  `held_reason: channel`, or `channel (unverified)` when `via_isolated` is
  false), the same door for every source. Until an operator turns it on, the
  inbound authorisation surface is the whole workspace.
- Socket Mode only. The Events API (public HTTPS + request signing) is not
  supported: it would be a separate inbound surface, and every way a message
  enters needs its own authorisation decision.
- `via` is as strong as uid isolation inside one container: it
  guards against the occupant's own mail being mistaken for Slack's, not
  against a hostile process in the same container — there is no boundary
  there to begin with.
- Text only. No files, no reactions, no edits/deletes (those arrive with a
  `subtype` and are dropped).

## ⚠ 它对宿主 runtime 有一个要求，而这里是唯一写着它的地方

中继跑在**住户的容器**里，用的是那个 runtime 自带的 WebSocket。

**观测（2026-09-04，真 Slack，两台两向两个 App）**：住户镜像是 **node 20** 时，
收到的帧 `data` 是 `null` —— socket 开着、`hello` 收到、**一条消息都进不来**；
同一份中继在 **node 24** 上正常。

⊘ **为什么，今天是未知的。** 当时记的原因是 permessage-deflate，而 2026-09-05 实测
推翻了它（node 20 **根本不提**这个扩展；服务端单方面压缩时它给 `error` 事件而不是
`data: null`）。⇒ **观测是真的，解释不是** —— 这里只写观测。

⇒ 中继**自己测那件事本身**：第一帧空 `data` 就退非零、原因写 stderr，daemon 因此报
`failed` + `exit N`。**它检查症状不检查病因**，所以上面那个解释被推翻也不影响它。

⚠ **出厂四份官方 runtime 里，`claude-code` / `codex` / `hermes` 是 node 20，
只有 `openclaw` 是 node 24。** 「模板里加一行 `- baton.slack`」对前三个
**尚未被证明可用** —— 真 Slack 那次跑通用的是 node 24 的镜像。

★ **「未被证明可用」不是「已证明不可用」，而两句话要求读者做的事不同**（BE-lead）：
前者是**没人试过**，你试了可能就通了，通了请说一声；后者是**别试**。
今天成立的只有前者 —— 真 Slack 只在 node 24 上被走过一次，另外三份**没有人试过**。
⊘ 而万一它不通，中继会**当场退非零并把原因写 stderr**（见上），
所以试的代价是一次说得出原因的 `failed`，不是一间看起来正常、信一条都不进来的办公室。
