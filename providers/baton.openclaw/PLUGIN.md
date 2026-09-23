# OpenClaw

**Purpose:** this office runs OpenClaw.

**Provides**

- the runtime itself
- skills discovery: per-skill links into `$HOME/.openclaw/skills`, which is
  where the package joins `CONFIG_DIR` with `skills` (measured 2026-08-28: a
  probe skill showed as `openclaw skills list` → `✓ ready`, source
  `openclaw-managed`)
- an instruction line pointing at `/workspace/BATON.md`
- the provider key, written into `openclaw.json`

**Keeps** — `$HOME/.openclaw`, classed `secret`: it now holds the provider key.

---

## ⚠ This facility writes its instruction line into your workspace

`/workspace/AGENTS.md` — **not** into the runtime's own home, the way the other
three do. It is the only facility that puts BATON's sentence on the occupant's
desk rather than in the tool's config.

It works, and it is what OpenClaw reads. But it means a line you did not write
appears in a file that is yours, and it travels with your workspace. Recorded
here because you should not have to discover it by finding it.
