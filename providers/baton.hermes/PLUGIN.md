# Hermes

**Purpose:** this office runs Hermes.

**Provides**

- the runtime itself
- an instruction line in `$HOME/.hermes/SOUL.md` pointing at
  `/workspace/BATON.md`
- skills discovery through Hermes' own `skills.external_dirs`, pointed at
  BATON's aggregation directory

**Keeps** — `$HERMES_HOME`, classed `secret`: config, auth, `SOUL.md`,
`memories/`, `sessions/`.

---

## Why the skills readout says `external-dirs` and not `symlink`

BATON places the same per-skill links either way. What differs is **why Hermes
reads that directory**: only because a config write happened during install —
a write BATON performed on the template's instruction and **cannot verify took
effect**. Same bytes, weaker claim, and the readout says which.

## One measurement that cost something

Hermes' first start **merged** the instruction sentence into its default
persona line. A whole-line guard stops matching at that moment and appends a
duplicate on every rebuild after. That is why the instruction check is a
substring, everywhere, for every facility.
