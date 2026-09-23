// SPDX-License-Identifier: Apache-2.0


















import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { defaultDataDir } from '../api/client.js';
import { flagBool, flagString, type ParsedArgs } from '../args.js';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { json, table } from '../output.js';
import {
  copyFileOutIfPresent, copyIn, execCapture, runState,
} from '../runtime/engine.js';
import {
  PLUGINS_REPORT, PLUGINS_ROOT, PROVENANCE_FILE, PLUGIN_MANIFEST,
  installPlugin, ledgerFor, ledgerLine, listPlugins, looksLikePluginPath, pluginsDir, readLedger, removePlugin, resolvePlugin,
  validatePluginID, type Door, type LedgerLine,
} from '../runtime/plugins.js';
import { resolveTarget, type Target } from '../runtime/target.js';
import { boundMounts } from '../runtime/template.js';

export async function plugin(args: ParsedArgs): Promise<number> {
  const sub = args.positionals[1] ?? 'list';
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const node = flagString(args, 'node');
  switch (sub) {
    case 'install': return install(args, dataDir, node);
    case 'list':    return node ? listOnNode(args, dataDir, node) : list(args, dataDir);
    case 'remove':  return remove(args, dataDir, node);
    default:
      throw usageError(`plugin ${sub} is not a subcommand`, 'Available: baton plugin install <dir|id> [--node <name>] | list [--node <name>] | remove <id> [--node <name>] [--force]');
  }
}




function install(args: ParsedArgs, dataDir: string, node: string | undefined): number {
  const ref = args.positionals[2];
  if (!ref) {
    throw usageError('install needs a plugin', 'Give the directory it was unpacked into (`baton plugin install ./example.learning`), or — with --node — the id of one already in the store.');
  }



  let id: string;
  let installed: { id: string; version: string; files: number; supplies_runtime: boolean; dir: string } | undefined;
  if (looksLikePluginPath(ref)) {
    installed = installPlugin(dataDir, ref);
    id = installed.id;
  } else {
    validatePluginID(ref, 'install');
    if (!node) {
      throw usageError(
        `${ref} is an id, and install without --node takes a directory`,
        `To put a plugin in the store, give its directory. To push one already in the store to a node: \`baton plugin install ${ref} --node <name>\`.`,
      );
    }
    id = ref;
  }
  const pushed = node ? pushToNode(dataDir, id, node) : undefined;

  if (args.global.output === 'json') {
    process.stdout.write(json({ kind: 'PluginInstall', id, store: installed ?? null, node: pushed ?? null }) + '\n');
  } else {
    if (installed) {
      process.stdout.write(`${installed.id} ${installed.version}: installed, ${installed.files} files, door installed, at ${installed.dir}\n`);
    }
    if (pushed) process.stdout.write(renderPush(pushed));
  }
  return pushed?.exit ?? ExitCode.OK;
}


interface Pushed {
  node: string;
  container: string;
  id: string;
  door: Door | undefined;
  files: number;

  sync?: SyncAnswer;

  landed_only?: string;
  exit: number;
}


interface SyncAnswer {
  plugin: string;
  found: boolean;
  linked: number;
  refused: number;
  unknown: number;
  skills?: { name: string; source: string; state: string; at?: string; why?: string }[];
  trust?: string;
  trust_reason?: string;
  hook?: string;
  instructions?: string;
  withdrawn?: string[];
  summary?: string;
  error?: string;
}















function pushToNode(dataDir: string, id: string, node: string): Pushed {
  const ledger = readLedger(dataDir);
  const pl = resolvePlugin({ id }, dataDir, `plugin install ${id}`, ledger);
  if (pl.runtime !== undefined) {




    throw new BatonError({
      code: 'CONFLICT',
      message: `${id} supplies a runtime, and a running node's runtime is not changed by install`,
      remediation: 'A node is one runtime, chosen at create from its template. To change it, create a node from a template that names the other one.',
      exitCode: ExitCode.CONFLICT,
    });
  }













  const missing = (pl.service?.secrets ?? [])
    .map((sec) => ({ name: sec.name ?? '(unnamed)', target: sec.mountPath ?? `/run/secrets/${sec.name ?? '(unnamed)'}` }))
    .filter((sec) => !boundMounts(dataDir, node).has(sec.target));
  if (missing.length > 0) {
    throw preconditionError(
      `${id} needs ${missing.map((m) => m.name).join(', ')} mounted at ${missing.map((m) => m.target).join(', ')}, and ${node} was created without them`,
      `A credential is a read-only bind decided at create, and \`rebuild\` re-creates the container from the ` +
        `manifest that already exists — it swaps the image, not the contract. Refitting this office means making it again:\n` +
        `  baton snapshot ${node}\n` +
        `  baton destroy ${node} --yes\n` +
        `  baton agent create --name ${node} --template <template with \`- ${id}\`> --secret <NAME>=<file>\n` +
        `  baton agent join --name ${node} --network <network>\n` +
        `A facility whose service declares no secrets needs none of this: it installs into a running node.`,
    );
  }
  const target = resolveTarget(node, { role: 'agent', dataDir });
  const tmp = mkdtempSync(join(tmpdir(), 'baton-plugin-'));
  try {


    const probe = copyFileOutIfPresent(target.container, `${PLUGINS_ROOT}/${id}/${PLUGIN_MANIFEST}`, join(tmp, 'probe.yaml'));
    if (probe.present) {
      throw new BatonError({
        code: 'CONFLICT',
        message: `${id} is already on ${node}`,
        remediation: `An upgrade is \`baton plugin remove ${id} --node ${node}\` and then install again — removal is a complete verb, and an overwrite in place would skip it.`,
        exitCode: ExitCode.CONFLICT,
      });
    }
    const nodeLedger = readNodeLedger(target, tmp);


    const treeDir = join(tmp, 'tree');
    mkdirSync(join(treeDir, id), { recursive: true });
    cpSync(pl.dir as string, join(treeDir, id), { recursive: true });
    const put = copyIn(treeDir, target.container, PLUGINS_ROOT);
    if (!put.ok) {
      throw new BatonError({
        code: 'INTERNAL',
        message: `could not put ${id} into ${node} at ${PLUGINS_ROOT}`,
        remediation: (put.stderr ?? '').trim() || 'The engine gave no reason. Nothing on the node was changed: the ledger is written after the tree, and the tree did not land.',
        exitCode: ExitCode.INTERNAL,
      });
    }

    const kept = nodeLedger.filter((l) => !l.path.startsWith(`${id}/`)).map(ledgerLine);
    const mine = ledgerFor([id], dataDir).split('\n').filter(Boolean);
    writeNodeLedger(target, tmp, [...kept, ...mine]);

    const files = mine.length;
    const base: Pushed = { node, container: target.container, id, door: pl.door, files, exit: ExitCode.OK };
    if (runState(target.container) !== 'running') {




      return {
        ...base,
        landed_only: `${node} is not running: the plugin is on its disk, and the daemon reconciles it at the next start`,
        exit: ExitCode.PARTIAL,
      };
    }
    const sync = askSync(target, id);



















    const disagrees = !sync.error && !sync.found;
    return { ...base, sync, exit: sync.error || disagrees ? ExitCode.PARTIAL : base.exit };
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}

function readNodeLedger(target: Target, tmp: string): LedgerLine[] {
  const got = copyFileOutIfPresent(target.container, `${PLUGINS_ROOT}/${PROVENANCE_FILE}`, join(tmp, 'node.provenance'));
  if (!got.present) return [];
  if (!got.ok) {
    throw new BatonError({
      code: 'INTERNAL',
      message: `could not read ${target.node}'s plugin ledger`,
      remediation: `${(got.stderr ?? '').trim() || 'The engine gave no reason.'} — nothing was written: a ledger that could not be read is not one to overwrite.`,
      exitCode: ExitCode.INTERNAL,
    });
  }
  const out: LedgerLine[] = [];
  for (const [i, line] of readFileSync(join(tmp, 'node.provenance'), 'utf8').split('\n').entries()) {
    const t = line.trim();
    if (t === '' || t.startsWith('#')) continue;
    const m = /^([0-9a-f]{64})\s{2}(seeded|installed|adopted)\s{2}(.+)$/.exec(t);
    if (!m) throw preconditionError(`${target.node}:${PLUGINS_ROOT}/${PROVENANCE_FILE}:${i + 1} is not \`<sha256>  <door>  <path>\``, 'The node\'s ledger is not one this build can read, and it is not rewritten on a guess.');
    out.push({ digest: m[1]!, door: m[2] as Door, path: m[3]! });
  }
  return out;
}

function writeNodeLedger(target: Target, tmp: string, lines: string[]): void {
  const dir = join(tmp, 'ledger');
  mkdirSync(dir, { recursive: true });
  const sorted = [...lines].sort();
  writeFileSync(join(dir, PROVENANCE_FILE), sorted.length ? sorted.join('\n') + '\n' : '', { mode: 0o600 });
  const put = copyIn(dir, target.container, PLUGINS_ROOT);
  if (!put.ok) {
    throw new BatonError({
      code: 'PARTIAL',
      message: `the tree is on ${target.node} and its ledger is not`,
      remediation: `${(put.stderr ?? '').trim() || 'The engine gave no reason.'} — the daemon reads the tree as \`unknown\` (no ledger lines) until the ledger lands; run the same command again.`,
      exitCode: ExitCode.PARTIAL,
    });
  }
}


function askSync(target: Target, id: string): SyncAnswer {
  const res = execCapture(target.container, ['baton-resource', 'skills', 'sync', '--plugin', id, '--json']);
  let parsed: SyncAnswer | undefined;
  try { parsed = JSON.parse(res.stdout) as SyncAnswer; } catch {  }
  if (!parsed) {
    throw new BatonError({
      code: 'INTERNAL',
      message: `${target.node}'s daemon gave no readable answer to the sync request`,
      remediation: `${(res.stderr ?? '').trim() || res.stdout.trim() || 'No output.'} — the bytes are on the node; \`baton plugin list --node\` shows what its daemon made of them once it has reconciled.`,
      exitCode: ExitCode.INTERNAL,
    });
  }
  return parsed;
}

function renderPush(p: Pushed): string {
  const lines = [`${p.id} → ${p.node}: ${p.files} ledger lines (door ${p.door ?? 'none — the store has no lines for it; the node reads it as unknown'})`];
  if (p.landed_only) {
    lines.push(`  ${p.landed_only}`);
    return lines.join('\n') + '\n';
  }
  const s = p.sync!;
  if (s.error) {
    lines.push(`  the daemon could not reconcile: ${s.error}`);
  } else if (!s.found) {
    lines.push('  the daemon reconciled and did not find it — the tree landed and the readout disagrees; inspect the node');
  } else {
    lines.push(`  linked ${s.linked} / refused ${s.refused} / unknown ${s.unknown} of ${s.skills?.length ?? 0} skills · trust ${s.trust} · hook ${s.hook} · instructions ${s.instructions}`);
    if (s.trust !== 'system' && s.trust_reason) lines.push(`  ${s.trust_reason}`);
    for (const sk of s.skills ?? []) {
      lines.push(`    ${sk.name.padEnd(32)} ${sk.state.padEnd(14)} ${sk.source}${sk.why ? `  — ${sk.why}` : ''}`);
    }
  }
  for (const w of s.withdrawn ?? []) lines.push(`  withdrawn: ${w}`);
  return lines.join('\n') + '\n';
}




function list(args: ParsedArgs, dataDir: string): number {
  const items = listPlugins(dataDir);
  if (args.global.output === 'json') {
    process.stdout.write(json({ apiVersion: 'baton.mailloop.dev/v1alpha1', kind: 'PluginList', dir: pluginsDir(dataDir), items }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    table(
      items,
      [
        { header: 'id', get: (p) => p.id },
        { header: 'version', get: (p) => (p.error === null ? p.version : '-') },


        { header: 'door', get: (p) => (p.error === null ? (p.door ?? 'none') : '-') },
        { header: 'runtime', get: (p) => (p.error === null ? (p.supplies_runtime ? 'supplies' : '-') : '-') },

        { header: 'service', get: (p) => (p.error === null ? (p.service ? `${p.service.command[0] ?? ''} (${p.service.health})` : '-') : '-') },
        { header: 'skills', get: (p) => (p.error === null ? (p.skills.length ? p.skills.join(',') : '-') : '-') },
        { header: 'skill_sources', get: (p) => (p.error === null ? (p.skill_sources.length ? p.skill_sources.join(',') : '-') : '-') },


        { header: 'state', get: (p) => (p.error === null ? stateCounts(p.state) : '-') },
        { header: 'note', get: (p) => (p.error !== null ? `unreadable: ${p.error.split('\n')[0]}` : (p.door ? '' : 'no ledger lines: a node reads it as unknown')) },
      ],
      `Nothing in ${pluginsDir(dataDir)}. \`baton init\` seeds the ones this build ships; \`baton plugin install <dir>\` adds one.`,
    ) + '\n',
  );
  return ExitCode.OK;
}


function stateCounts(state: { belongs_to: string | undefined }[]): string {
  if (state.length === 0) return '-';
  const by = new Map<string, number>();
  for (const s of state) by.set(s.belongs_to ?? '?', (by.get(s.belongs_to ?? '?') ?? 0) + 1);
  return [...by.entries()].map(([k, n]) => `${n} ${k}`).join(', ');
}


interface NodePluginRow {
  id: string;
  version: string;
  trust: string;







  trust_reason: string;
  hook: string;
  instructions: string;
  linked: number;
  refused: number;
  unknown: number;
  skills: number;
}






function listOnNode(args: ParsedArgs, dataDir: string, node: string): number {
  const target = resolveTarget(node, { role: 'agent', dataDir });
  const tmp = mkdtempSync(join(tmpdir(), 'baton-plugin-'));
  let rows: NodePluginRow[] = [];




  let refusedRows: string[] = [];
  let unknownWhy: string | undefined;
  let summary: unknown;
  let withdrawn: string[] = [];
  try {
    const got = copyFileOutIfPresent(target.container, PLUGINS_REPORT, join(tmp, 'plugins.json'));
    if (!got.present) {
      unknownWhy = `${target.container} has no ${PLUGINS_REPORT} yet: its daemon has not reconciled (not started, or started before the tree landed)`;
    } else if (!got.ok) {
      unknownWhy = `${PLUGINS_REPORT} could not be read from ${target.container}: ${(got.stderr ?? '').trim()}`;
    } else {
      const rep = JSON.parse(readFileSync(join(tmp, 'plugins.json'), 'utf8')) as {
        plugins?: { id: string; version: string; trust: string; trust_reason?: string; hook: string; instructions?: string; skills?: { state: string }[] }[];
        summary?: unknown;
        withdrawn?: string[];



        refused?: string[];
      };
      summary = rep.summary;
      withdrawn = rep.withdrawn ?? [];














      refusedRows = rep.refused ?? [];
      rows = (rep.plugins ?? []).map((p) => {
        const sk = p.skills ?? [];
        const n = (state: string[]) => sk.filter((s) => state.includes(s.state)).length;
        return {
          id: p.id, version: p.version, trust: p.trust, trust_reason: p.trust_reason ?? '', hook: p.hook, instructions: p.instructions ?? 'none',
          linked: n(['symlink', 'external-dirs']), refused: n(['refused']), unknown: n(['unknown']), skills: sk.length,
        };
      });
    }
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
  const running = runState(target.container);
  if (args.global.output === 'json') {
    process.stdout.write(json({
      apiVersion: 'baton.mailloop.dev/v1alpha1', kind: 'NodePluginList', node, container: target.container,


      run_state: running, readout: PLUGINS_REPORT, items: rows, refused: refusedRows,
      summary: summary ?? null, withdrawn, unknown: unknownWhy ?? null,
    }) + '\n');
    return ExitCode.OK;
  }
  if (unknownWhy) {


    process.stdout.write(`${node}: plugins unknown — ${unknownWhy}\n`);
    return ExitCode.OK;
  }
  process.stdout.write(
    table(
      rows,
      [
        { header: 'id', get: (r) => r.id },
        { header: 'version', get: (r) => r.version },
        { header: 'trust', get: (r) => r.trust },


        { header: 'why', get: (r) => (r.trust === 'system' ? '-' : r.trust_reason || '-') },
        { header: 'hook', get: (r) => r.hook },
        { header: 'instructions', get: (r) => r.instructions.split(' ')[0] ?? r.instructions },
        { header: 'skills', get: (r) => `${r.linked} linked / ${r.refused} refused / ${r.unknown} unknown of ${r.skills}` },
      ],
      `${node}'s daemon reconciled and found no plugins.`,
    ) +
      `\n  as of the daemon's last reconcile (${PLUGINS_REPORT}); the node is ${running}` +




      (refusedRows.length
        ? `\n  on the node and not readable as a facility:\n${refusedRows.map((r) => `    ${r}`).join('\n')}`
        : '') +
      (withdrawn.length ? `\n  withdrawn:\n${withdrawn.map((w) => `    ${w}`).join('\n')}` : '') +
      '\n',
  );
  return ExitCode.OK;
}




function remove(args: ParsedArgs, dataDir: string, node: string | undefined): number {
  const id = args.positionals[2];
  if (!id) throw usageError('remove needs a plugin id', 'Name one: `baton plugin remove <id>`. See ids with `baton plugin list`.');
  if (node) return removeFromNode(args, dataDir, id, node);
  const r = removePlugin(dataDir, id, { force: flagBool(args, 'force') });
  if (args.global.output === 'json') {
    process.stdout.write(json({ kind: 'PluginRemove', id: r.id, door: r.door ?? null, files: r.files, nodes_scanned: false }) + '\n');
  } else {
    process.stdout.write(
      `${r.id}: removed from the store (${r.files} ledger lines, door ${r.door ?? 'none'})\n` +


        `  nodes were not touched — each holds its own copy; \`baton plugin remove ${r.id} --node <name>\` takes it off one\n`,
    );
  }
  return ExitCode.OK;
}










function removeFromNode(args: ParsedArgs, dataDir: string, id: string, node: string): number {
  validatePluginID(id, 'remove');
  const target = resolveTarget(node, { role: 'agent', dataDir });
  if (runState(target.container) !== 'running') {
    throw preconditionError(
      `${node} is not running, and removing from a node needs its daemon`,
      `Start it (\`baton start ${node}\`) and run this again: the removal is an exec into the container, and the daemon is what retires the plugin's links and state.`,
    );
  }
  const tmp = mkdtempSync(join(tmpdir(), 'baton-plugin-'));
  try {
    const manifest = copyFileOutIfPresent(target.container, `${PLUGINS_ROOT}/${id}/${PLUGIN_MANIFEST}`, join(tmp, 'manifest.yaml'));
    if (!manifest.present) {
      throw preconditionError(`${id} is not on ${node}`, `See what is: \`baton plugin list --node ${node}\`.`);
    }
    if (manifest.ok && /^runtime:/m.test(readFileSync(join(tmp, 'manifest.yaml'), 'utf8'))) {


      throw new BatonError({
        code: 'CONFLICT',
        message: `${id} supplies ${node}'s runtime and is not removed from it`,
        remediation: 'A node is one runtime, chosen at create. To be rid of it, destroy the node.',
        exitCode: ExitCode.CONFLICT,
      });
    }
    const nodeLedger = readNodeLedger(target, tmp);
    const rm = execCapture(target.container, ['rm', '-rf', `${PLUGINS_ROOT}/${id}`], { user: '0' });
    if (!rm.ok) {
      throw new BatonError({
        code: 'INTERNAL',
        message: `could not delete ${PLUGINS_ROOT}/${id} on ${node}`,
        remediation: (rm.stderr ?? '').trim() || 'The engine gave no reason. Nothing else was changed.',
        exitCode: ExitCode.INTERNAL,
      });
    }
    writeNodeLedger(target, tmp, nodeLedger.filter((l) => !l.path.startsWith(`${id}/`)).map(ledgerLine));
    const sync = askSync(target, id);
    if (args.global.output === 'json') {
      process.stdout.write(json({ kind: 'PluginRemove', id, node, container: target.container, sync }) + '\n');
      return sync.found ? ExitCode.PARTIAL : ExitCode.OK;
    }
    const lines = [`${id} ← ${node}: directory and ledger lines removed`];
    if (sync.error) lines.push(`  the daemon could not reconcile: ${sync.error}`);
    else if (sync.found) lines.push('  the daemon reconciled and STILL finds it — the deletion did not take; inspect the node');
    else lines.push('  the daemon reconciled and no longer finds it');
    for (const w of sync.withdrawn ?? []) lines.push(`  withdrawn: ${w}`);
    if (!sync.error && !sync.found && !(sync.withdrawn ?? []).some((w) => w.includes(id))) {






      lines.push('  (no record of what its removal took back: the daemon had no readable record when the directory went — its leftovers, if any, are where they were)');
    }
    process.stdout.write(lines.join('\n') + '\n');
    return sync.found ? ExitCode.PARTIAL : ExitCode.OK;
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}
