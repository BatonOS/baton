# Claude Code

**Purpose:** this office runs Claude Code. The facility installs it, tells it
where it is, and hands it the company's skills.

**Provides**

- the runtime itself — image, command, TTY, health, restart policy
- skills discovery: BATON links each skill into `$HOME/.claude/skills`, one per
  skill, so your own skills sit beside the company's and neither prunes the other
- an instruction line in `$HOME/.claude/CLAUDE.md` pointing at
  `/workspace/BATON.md`
- the model credential, through `apiKeyHelper` — **the way to get the value,
  never the value**. The key stays in the mounted file and is not copied into
  any config

**Keeps** — `.claude` in the node's persistence tree, classed `secret`: it holds
the interactive login as well as session state.

**Does not ask you before running tools.** Claude Code in this office is told
it is inside a sandbox (`IS_SANDBOX=1`) and runs tool calls without a
confirmation prompt — this node *is* that sandbox: its own filesystem, its own
workspace, nothing of the host's. Everything it can reach is what this office
was fitted with.

---

## One thing worth knowing before you rotate a key

The helper is **not hot**. Measured three ways (default, TTL 0, TTL 1) in the
shape this actually runs in — one long process under tmux, two turns, value
swapped between them — and **all three sent the old value on the second turn**.
The first reading said otherwise because it was taken with `claude -p`, where
every call is a fresh process. A new key takes effect at the next rebuild.
