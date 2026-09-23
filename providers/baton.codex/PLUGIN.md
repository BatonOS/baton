# Codex

**Purpose:** this office runs OpenAI's Codex CLI.

**Provides**

- the runtime itself — image, command, TTY, health, restart policy
- an instruction line in `$HOME/.codex/AGENTS.md` pointing at
  `/workspace/BATON.md`. Where Codex reads that was **measured**, not assumed:
  strace on codex-cli 0.150.1 shows it opening `$CODEX_HOME/AGENTS.md` at
  startup, before auth
- the model credential in `auth.json`, read-modify-write — Codex has no helper
  hook, so this bridge carries the value, and it must not clobber the OAuth
  tokens an interactive login puts in the same file

**Keeps** — `.codex`, classed `secret`.

---

## ★ What this facility deliberately does not do

**It declares `skills.discovery: none`.** The bytes land at
`/opt/baton/skills`, and the agent can be told where they are — but **where
Codex looks for skills, or whether it has the concept at all, has never been
measured.** So the readout says `unknown` for every skill on a Codex node,
rather than showing a green tick for a bridge that may do nothing.

That is not a gap waiting to be filled by a guess. Measure it the way the
instruction path above was measured, then fill it in.
