# Baton Inbox

**Purpose:** how you work your mail. Messages from operators and other agents
arrive on their own; this is the facility that teaches you what to do with them.

**Provides**

- `inbox-agent` — a skill your runtime invokes when you are asked to check mail,
  reply, or report progress to somebody

**Keeps nothing.** The mailbox is not part of this facility — see below.

---

## The mailbox is not in here, and that is deliberate

`.baton/local/mail/` is part of the **workspace contract**, not of this
facility. The control plane delivers there whether or not anybody installed
anything: *carrying* a message between two identities is BATON's job — it owns
transport and delivery, never the message's meaning — and a workspace with no mailbox would leave "send this to that agent" with
nowhere to land.

So the mailbox is a guarantee. What varies — and therefore what is a facility —
is whether the occupant has been **told how to use it**.

## Three layers: who carries a message, who teaches, who bridges

When agent A writes to agent B, nothing in any plugin carries that message.
Three layers are involved and the line between them is the point:

| Layer | Defined by | What it owns |
|---|---|---|
| **core mail** | the control plane (delivery + store) and the node daemon; `baton-inbox` is the daemon binary under a second name | the mailbox at `.baton/local/mail/`, `send` → control plane → the recipient's daemon → its mailbox, `next`/`peek`/`--reply-to`/threads, delivery records. Carrying a message between two identities is BATON's job: it owns transport and delivery, never the message's meaning. |
| **this facility** (`baton.inbox`) | one skill | teaching the occupant how to work that mailbox. It defines no route, no envelope, no verb. |
| **a channel facility** (`baton.slack`, and whatever follows it) | a **service** — a relay the daemon runs beside the occupant, in the occupant's own office | one edge only: an outside channel ↔ *this node's own* mailbox. It sends and receives with the same core `baton-inbox` verbs the occupant uses, from an outbox and inbox of its own; the daemon marks what it relays in as `via: baton.slack` and routes what the occupant addresses to it. It never reads what it carries. |

So agent-to-agent traffic is the middle line of this picture, and it never
touches the outer edges:

    Slack ─[baton.slack]─▶ A's mailbox (via: baton.slack) ─▶ A ─[core mail]─▶ B ─[core mail]─▶ A ─[core mail, for: baton.slack]─▶ [baton.slack]─▶ Slack

Two consequences you can rely on. Replace `baton.slack` with another channel
facility and the A ↔ B leg does not change by a byte. Replace this skill with
a different one and the delivery path does not change either. The converse is
the rule: **no facility defines how two agents reach each other**. That is
core, and a manifest has no place to say otherwise: it is parsed strictly and
its only sections are `runtime` or `service`, `instructions`, `state`,
`skills` and `hooks` — none of them is a route.

## Why it sits beside the runtime facility, not inside it

You can install this next to `baton.claude-code`, `baton.codex`, or anything else.
It is not any runtime's property.

If it were nested inside the runtime facility, **swapping the harness would take
the mailbox skill with it** — and keeping the company's things across a harness
swap is precisely what that swap is supposed to preserve.

## One thing to do rather than read

    baton-inbox contract

Ask that at the start of a mail-handling session and trust its answer over any
page, including this one: it lists the verbs and exit codes **this** node
actually offers, and a rebuilt node may be newer than the notes that came with
it. If a verb you read about is missing from the contract, the contract is right.
