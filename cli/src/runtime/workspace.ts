// SPDX-License-Identifier: Apache-2.0























import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';










export const WORKSPACE_CONTRACT = 'baton-workspace/3';


export const BATON_DIR = '.baton';



















export const ROOT_NORMS = ['public'] as const;














export const BATON_PAGE = 'BATON.md';










export const ZONES = {





  portable: ['self', 'library', 'ext'],
  local: ['mail', 'net', 'cache', 'ext'],
} as const;




















export const RUNTIME_WRITABLE = { position: 'local', zone: 'mail', path: 'outbox' } as const;

export type Position = keyof typeof ZONES;


export const POSITIONS = Object.keys(ZONES) as Position[];













export const TRAVELS: Position = 'portable';


















export function pruneNonTravelling(workspaceRoot: string): string[] {
  const removed: string[] = [];
  for (const position of nonTravelling()) {
    const dir = join(workspaceRoot, BATON_DIR, position);
    if (!existsSync(dir)) continue;
    rmSync(dir, { recursive: true, force: true });
    removed.push(`${BATON_DIR}/${position}`);
  }
  return removed;
}











export function nonTravelling(): Position[] {
  return POSITIONS.filter((p) => p !== TRAVELS);
}

















export const NEVER_PORTABLE = ['mail', 'inbox', 'outbox', 'pki', 'revoked', 'enrollment'];

export interface ZoneAudit {

  compared: number;
  problems: string[];
}

















export function auditZones(workspaceRoot: string): ZoneAudit {
  const problems: string[] = [];
  let compared = 0;

  const baton = join(workspaceRoot, BATON_DIR);
  if (!existsSync(baton)) {
    return { compared: 0, problems: [`${baton} does not exist`] };
  }


  const top = readdirSync(baton, { withFileTypes: true });
  for (const e of top) {
    if (e.isDirectory() && !POSITIONS.includes(e.name as Position)) {
      problems.push(
        `${BATON_DIR}/${e.name} is neither portable/ nor local/ — there is no third position, ` +
          'so a zone that belongs nowhere cannot be created without deciding: a thing goes in portable/ if it travels with the agent, local/ if it does not',
      );
    }
  }

  for (const position of POSITIONS) {
    const dir = join(baton, position);
    if (!existsSync(dir)) {
      problems.push(`${BATON_DIR}/${position}/ is missing`);
      continue;
    }

    const declared = ZONES[position] as readonly string[];
    const onDisk = readdirSync(dir, { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => e.name);


    for (const name of onDisk) {
      compared += 1;
      if (!declared.includes(name)) {
        problems.push(`${BATON_DIR}/${position}/${name} is on disk and not declared`);
      }
      if (position === 'portable' && NEVER_PORTABLE.includes(name)) {
        problems.push(
          `${BATON_DIR}/portable/${name}: ${JSON.stringify(name)} is on the never-portable list (identity material, enrollment tokens, ` +
            'inbox, outbox, revocation marker) — it may never be carried: ' +
            'a restored node is a different identity, and an unsent message would go out ' +
            'signed by whoever restored it',
        );
      }
    }



    for (const name of declared) {
      if (!onDisk.includes(name)) {
        problems.push(`${BATON_DIR}/${position}/${name} is declared and not on disk`);
      }
    }
  }

  return { compared, problems };
}









export function auditSummary(a: ZoneAudit): string {
  const total = POSITIONS.reduce((n, p) => n + ZONES[p].length, 0);
  return `contract declares ${total} zones, the tree has ${a.compared}`;
}


export function contractDocument(): Record<string, unknown> {
  return {
    contract: WORKSPACE_CONTRACT,
    root: {
      public: {
        holds: 'what this agent is willing to give out — not in here = not taken',

























        read_by: ['baton-resource publish'],
        readers:
          'one reader exists: `baton-resource publish` takes what you hand to the company from here, and refuses any path outside it. Putting something here is consent, not delivery — nothing leaves until you run that verb.',
      },
    },
    portable: {
      self: { holds: 'agent identity, network membership, held attestations' },








      library: { holds: 'references and provenance for adopted resources — kept here because they cannot be fetched again' },
      ext: { holds: 'provider namespaces that travel — parcels, reports, and what a provider built here; a provider\'s machine-bound state goes in local/ext/<provider>/' },
    },
    local: {
      mail: {
        why: 'identity-addressed; mail is never carried in a snapshot or move — a restored node is a different identity',
        writable_by: { [RUNTIME_WRITABLE.path]: 'runtime' },



        delivered_from: null,
        delivery_reads: "the node's data directory, not this workspace — nothing is delivered from here today; mail is sent with `baton-inbox send`, and its directory comes from `baton-inbox contract`",
      },
      net: {
        why: 'a projection; it goes stale',
        rebuilt_from: 'master',













        projects: {
          'members.json': 'who is on this network',
          'resources.json': 'what the network shares — the catalogue, not the bodies',
        },
        absent_means:
          'a projection named here that is not on disk at all means this daemon never ran. A projection that could not be REBUILT says so in band — see `resources.json`\u0027s `state`.',
      },
      cache: { why: 're-fetchable by hash', rebuilt_from: 'content-address' },
      ext: { why: 'machine-bound provider state — device bindings and provider caches', rebuilt_from: 'provider' },
    },
  };
}


































function batonPage(): string {
  const line = (position: Position) => `  ${position}/${' '.repeat(10 - position.length)}${ZONES[position].join(', ')}`;
  return `# This is a BATON workspace

Everything here is yours. \`${BATON_DIR}/\` is BATON's.

\`${BATON_DIR}/\` has exactly two positions, and everything in it is in one:

${POSITIONS.map(line).join('\n')}

\`${TRAVELS}/\` travels with this workspace; the other one belongs to this node
and is rebuilt on arrival. A snapshot, a migrate, a clone and a restore all read
that from **which directory a thing is in** — not from a field somewhere saying
whether it should travel.

\`public/\` is the sharing boundary. What you put there you are willing to give
out; what is not in there is not taken. **One thing reads it**:
\`baton-resource publish\` hands a file from here to the company, and refuses
any path outside it. Ask \`baton-resource contract\` rather than this page —
it answers for the node you are actually on. Putting something here is consent,
not delivery: nothing leaves until you run that verb.

**What you put here survives a restart.** Deleting the directory itself is not
refused today, and not detected either — the next start recreates it, empty.

\`${BATON_DIR}/contract.json\` carries this in machine-readable form, and it
carries more than this page does: it is where each zone says what it holds.

You may write to exactly one path under \`${BATON_DIR}/\`:
\`${RUNTIME_WRITABLE.position}/${RUNTIME_WRITABLE.zone}/${RUNTIME_WRITABLE.path}/\`.
It is yours: the daemon hands it to your uid at every start.

**That is a permission, and not a way to send mail.** Mail is sent with
\`baton-inbox send\`, and the directory it uses comes from \`baton-inbox contract\` —
ask that rather than this page, because it answers for the node you are actually
on. The boxes here are named after a mailbox and owned by you, and today nothing
delivers from them: a message left here is not sent, not refused, and not logged.

Writing anywhere else under \`${BATON_DIR}/\` is **not refused today, and not
detected either**. That is a gap, not a permission.

An empty directory here means "nothing adopted yet". It does not mean the
feature is missing — that is why they are created empty rather than on demand.

## Facilities

This office may have facilities installed — a memory store, a reporting
routine, whatever this node was set up with. Each one is a directory here:

    /opt/baton/plugins/<id>/PLUGIN.md

That page says what the facility is and what it keeps for you. **Read those
pages to learn what exists**; you do not invoke them. The way you USE a
facility is a skill, and your runtime picks those up on its own.

Anything a facility keeps for you is under \`${BATON_DIR}/${TRAVELS}/ext/<id>/\`
when it travels with you, and \`${BATON_DIR}/local/ext/<id>/\` when it belongs
to this machine. Which one is the facility's declaration, not its choice at
write time — and removing a facility does not remove what it declared as
yours.

**\`/opt/baton/plugins/\` may be empty, and that means no facilities**, not a
missing feature.
`;
}































export function stageWalls(dir: string): string[] {
  const built: string[] = [];
  for (const position of POSITIONS) {
    for (const zone of ZONES[position]) {
      mkdirSync(join(dir, BATON_DIR, position, zone), { recursive: true });
      built.push(`${BATON_DIR}/${position}/${zone}/`);
    }
  }




  for (const norm of ROOT_NORMS) {
    mkdirSync(join(dir, norm), { recursive: true });
    built.push(`${norm}/`);
  }
  writeFileSync(join(dir, BATON_DIR, 'contract.json'), JSON.stringify(contractDocument(), null, 2) + '\n');
  built.push(`${BATON_DIR}/contract.json`);
  writeFileSync(join(dir, BATON_PAGE), batonPage());
  built.push(BATON_PAGE);
  return built;
}


















export type ContractVerdict = 'current' | 'stale' | 'newer' | 'unreadable';

export function contractVerdict(found: unknown): ContractVerdict {
  if (typeof found !== 'string') return 'unreadable';
  const mine = versionOf(WORKSPACE_CONTRACT);
  const theirs = versionOf(found);
  if (mine === undefined || theirs === undefined) return 'unreadable';
  if (theirs === mine) return 'current';
  return theirs < mine ? 'stale' : 'newer';
}

function versionOf(contract: string): number | undefined {
  const m = /^baton-workspace\/([0-9]+)$/.exec(contract.trim());
  if (!m) return undefined;
  const n = Number(m[1]);
  return Number.isSafeInteger(n) && n > 0 ? n : undefined;
}


export function readContract(workspaceRoot: string): Record<string, unknown> | undefined {
  try {
    return JSON.parse(
      readFileSync(join(workspaceRoot, BATON_DIR, 'contract.json'), 'utf8'),
    ) as Record<string, unknown>;
  } catch {
    return undefined;
  }
}
