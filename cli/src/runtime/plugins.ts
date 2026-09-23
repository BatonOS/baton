// SPDX-License-Identifier: Apache-2.0

























import { createHash } from 'node:crypto';
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { basename, isAbsolute, join, posix, resolve, sep } from 'node:path';
import { parse } from 'yaml';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import type { Extension } from './extensions.js';
import { BATON_DIR, BATON_PAGE, ROOT_NORMS } from './workspace.js';
import { assertKnownFields, assertSecretSaysRequired, SECRET_ENTRY_KNOWN, type FacilitySecrets, type KnownTree, type RawSecret } from './spec.js';


export const PLUGIN_API_VERSION = 'baton.mailloop.dev/v1alpha1';


export const PLUGIN_KIND = 'WorkspacePlugin';









export const INLINE_VERSION = '0.0.0';


export const PLUGIN_MANIFEST = 'manifest.yaml';


export const PLUGIN_PAGE = 'PLUGIN.md';






















export const PROVENANCE_FILE = '.provenance';


export type Door = 'seeded' | 'installed' | 'adopted';

















export const TRUSTED_DOORS: readonly Door[] = ['seeded'];


const SEGMENT = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/;


export interface PluginRef {
  id: string;

  inline?: Record<string, unknown>;

  config?: unknown;
}


export interface ResolvedPlugin {
  id: string;
  version: string;
  dir: string | undefined;
  manifest: Record<string, unknown>;
  runtime: Record<string, unknown> | undefined;

  service: ServiceDecl | undefined;
  config: unknown;
  door: Door | undefined;
}














export const SERVICE_KNOWN: KnownTree = {
  command: true,
  workingDir: true,
  user: true,
  env: [{ name: true, value: true }],
  secrets: [SECRET_ENTRY_KNOWN],
  health: { probe: true, intervalSec: true, tcp: true, exec: true, failureThreshold: true },
  restart: true,
  stopGraceSec: true,
};







const SERVICE_IS_NOT_A_RUNTIME: Record<string, string> = {
  apiVersion: 'a service is a section of the plugin manifest, not a document of its own',
  kind: 'a service is a section of the plugin manifest, not a document of its own',
  package: 'the image is the runtime\'s; a service starts inside the container the runtime already defined. Put its `command` and `workingDir` directly under service:',
  adapter: 'terminal, workspace, skills and status are how BATON controls the OCCUPANT; a service has `health`, `restart` and `stopGraceSec` directly under it',
  execution: 'a service has no resource ceiling of its own — the container\'s is the template\'s node: section. Put `user`, `env` and `secrets` directly under service:',
  status: 'a service reports through the daemon\'s own readout (plugins[].service.status), not through a status bridge of its own',
};

export interface ServiceDecl {
  command: string[];
  workingDir?: string;
  user?: string;
  env?: { name: string; value: string }[];
  secrets: RawSecret[];
  health?: { probe?: string; intervalSec?: number; tcp?: string; exec?: string[]; failureThreshold?: number };
  restart?: string;
  stopGraceSec?: number;
}








export function validateService(raw: unknown, label: string): ServiceDecl {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw usageError(`${label}: service is not a mapping`, 'Write `service: {command: [...], ...}` — a facility may declare one service, a long-running process the node daemon supervises beside the runtime.');
  }
  const doc = raw as Record<string, unknown>;
  for (const [k, why] of Object.entries(SERVICE_IS_NOT_A_RUNTIME)) {
    if (doc[k] !== undefined) {
      throw usageError(`${label}: service.${k} is a runtime's key, and a service is not a runtime`, `${why}.`);
    }
  }
  assertKnownFields(doc, SERVICE_KNOWN, 'service', label);
  const command = doc.command;
  if (!Array.isArray(command) || command.length === 0 || command.some((c) => typeof c !== 'string' || c === '')) {
    throw usageError(`${label}: service has no command — there is nothing to start`, 'Write `command: [/path/to/program, arg, ...]` — a list of strings, absolute program first.');
  }
  const user = doc.user === undefined ? undefined : String(doc.user).trim();
  if (user !== undefined && !/^[0-9]+(:[0-9]+)?$/.test(user)) {
    throw usageError(
      `${label}: service.user is ${user}, which is not a numeric uid or uid:gid`,
      'Give a number, like `user: "65533"`. A name would be resolved inside the image against a passwd file BATON never reads.',
    );
  }


  const env = doc.env;
  if (env !== undefined) {
    if (!Array.isArray(env)) {
      throw usageError(`${label}: service.env is not a list`, 'Write it as the runtime does: `env: [{name: KEY, value: v}]`.');
    }
    for (const [i, entry] of (env as unknown[]).entries()) {
      const e = (entry ?? {}) as { name?: unknown; value?: unknown };
      if (!entry || typeof entry !== 'object' || typeof e.name !== 'string' || e.name === '') {
        throw usageError(`${label}: service.env[${i}] has no name`, 'Each entry is `{name: KEY, value: v}`.');
      }
      if (e.name.startsWith('BATON_')) {
        throw usageError(
          `${label}: service.env sets ${e.name}`,
          'BATON_* is the daemon\'s to set (BATON_OUTBOX_DIR, BATON_SERVICE, BATON_DATA_DIR); a facility that sets it is lying to its own process.',
        );
      }
      if (typeof e.value !== 'string') throw usageError(`${label}: service.env[${i}] (${e.name}) has no string value`, 'Environment values are strings; quote numbers.');
    }
  }
  const secrets = doc.secrets;
  if (secrets !== undefined && !Array.isArray(secrets)) {
    throw usageError(`${label}: service.secrets is not a list`, 'Write it as the runtime does: a list of {name, from, mountPath, required, ...}.');
  }
  for (const entry of (secrets as unknown[] | undefined) ?? []) {
    if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
      throw usageError(`${label}: a service.secrets entry is not a mapping`, 'Each entry is {name, from, mountPath, required, ...}.');
    }
    const sec = entry as RawSecret;
    if (typeof sec.name !== 'string' || sec.name === '') {
      throw usageError(`${label}: a service.secrets entry has no name`, 'The name is what `--secret NAME=<file>` and `<NAME>_FILE` use.');
    }
    assertSecretSaysRequired(sec, label);
  }
  const health = doc.health as ServiceDecl['health'] | undefined;
  if (health !== undefined) {
    const probe = health?.probe;
    if (probe !== undefined && !['none', 'process', 'tcp', 'exec'].includes(String(probe))) {
      throw usageError(`${label}: service.health.probe is ${String(probe)}`, 'It must be one of none, process, tcp, exec.');
    }
  }
  const restart = doc.restart;
  if (restart !== undefined && !['always', 'on-failure', 'never'].includes(String(restart))) {
    throw usageError(`${label}: service.restart is ${String(restart)}`, 'It must be one of always, on-failure, never.');
  }
  const grace = doc.stopGraceSec;
  if (grace !== undefined && (typeof grace !== 'number' || !Number.isInteger(grace) || grace < 0)) {
    throw usageError(`${label}: service.stopGraceSec is ${String(grace)}`, 'A non-negative whole number of seconds.');
  }
  return {
    command: command as string[],
    workingDir: doc.workingDir as string | undefined,
    user,
    env: env as { name: string; value: string }[] | undefined,
    secrets: (secrets as RawSecret[] | undefined) ?? [],
    health,
    restart: restart as string | undefined,
    stopGraceSec: grace as number | undefined,
  };
}


export interface DeclaredSecret {
  name: string | null;
  label: string | null;
  provider: string | null;
  help_url: string | null;

  required: boolean | null;


  plugin: string;
}








export function secretsOf(pl: ResolvedPlugin): DeclaredSecret[] {
  type Decl = { name?: string; label?: string; provider?: string; help_url?: string; required?: boolean };
  const rt = pl.runtime as { execution?: { secrets?: Decl[] } } | undefined;
  const decls: Decl[] = [...(rt?.execution?.secrets ?? []), ...((pl.service?.secrets ?? []) as Decl[])];
  return decls.map((x) => ({
    name: x.name ?? null,
    label: x.label ?? null,
    provider: x.provider ?? null,
    help_url: x.help_url ?? null,
    required: typeof x.required === 'boolean' ? x.required : null,
    plugin: pl.id,
  }));
}






export function facilitySecretsOf(plugins: ResolvedPlugin[]): FacilitySecrets[] {
  return plugins
    .filter((p) => p.service !== undefined && p.service.secrets.length > 0)
    .map((p) => ({ facility: p.id, label: `${p.id}/${PLUGIN_MANIFEST}`, secrets: p.service?.secrets ?? [] }));
}


export function pluginsDir(dataDir: string): string {
  return join(dataDir, 'plugins');
}






export const PLUGINS_ROOT = '/opt/baton/plugins';







export const PLUGINS_TREE_SUBPATH = 'plugins/tree';


export const PLUGINS_TREE_UID = 65532;


export const PLUGINS_REPORT = '/var/lib/baton/plugins/plugins.json';








export function looksLikePluginPath(ref: string): boolean {
  return ref.includes('/') || ref.includes('\\') || isAbsolute(ref);
}


export function validatePluginID(id: unknown, label: string): string {
  if (typeof id !== 'string' || id === '') {
    throw usageError(`${label}: a plugin entry needs an id`, 'Give it as a string (`- baton.claude-code`) or as `- id: baton.claude-code`.');
  }
  const parts = id.split('.');
  if (parts.length < 2) {
    throw usageError(
      `${label}: ${id} is not a plugin id`,
      'It needs a namespace, like `baton.claude-code` or `acme.legal` — a bare name collides with every ' +
        "other vendor's idea of the same facility, and the collision surfaces as a shadowed directory rather than an error.",
    );
  }
  for (const p of parts) {
    if (!SEGMENT.test(p)) {
      throw usageError(
        `${label}: ${id} is not a plugin id`,
        `The segment ${p} must be lowercase letters, digits and interior hyphens.`,
      );
    }
  }
  return id;
}









export function parsePluginList(raw: unknown, label: string): PluginRef[] {
  if (raw === undefined) {
    throw usageError(
      `${label} has no plugins`,
      'A template is hardware plus a list of facilities: `plugins: [baton.claude-code]`. ' +
        'For a one-off, define one inline with `- id: local.<name>` and a `runtime:` under it.',
    );
  }
  if (!Array.isArray(raw)) {
    throw usageError(`${label}: plugins must be a list`, 'Each entry is a name or a mapping with an `id`.');
  }









  const seen = new Set<string>();
  const out: PluginRef[] = [];
  for (const [i, entry] of raw.entries()) {
    const where = `${label}: plugins[${i}]`;
    let ref: PluginRef;
    if (typeof entry === 'string') {
      ref = { id: validatePluginID(entry, where) };
    } else if (entry && typeof entry === 'object' && !Array.isArray(entry)) {
      const e = entry as Record<string, unknown>;
      const id = validatePluginID(e.id, where);
      if (e.config !== undefined && (typeof e.config !== 'object' || e.config === null || Array.isArray(e.config))) {



        throw usageError(`${where} (${id}) has a config that is not a mapping`,
          'BATON never reads a config, but it does write it out as one — give it keys.');
      }
      const known = new Set(['id', 'config', 'runtime', 'service', 'instructions', 'state', 'skills', 'hooks']);
      const unknown = Object.keys(e).filter((k) => !known.has(k));
      if (unknown.length > 0) {
        throw usageError(
          `${where} has fields this build does not understand: ${unknown.join(', ')}`,
          'A plugin entry is `id`, an optional opaque `config`, and — for an inline definition — ' +
            '`runtime`, `instructions`, `state`, `skills`.',
        );
      }
      const inlineKeys = Object.keys(e).filter((k) => k !== 'id' && k !== 'config');
      if (inlineKeys.length > 0) {
        if (e.hooks !== undefined) {




          throw usageError(
            `${where} (${id}) is defined inline and declares hooks`,
            'An inline plugin is declarative only: it has no files on disk, so there is nothing to ' +
              'attest and no door it came through. Install it as a real plugin if it needs to run code.',
          );
        }
        if (e.service !== undefined) {




          throw usageError(
            `${where} (${id}) is defined inline and declares a service`,
            'A service is a process the node runs, and the node runs one only for a facility that came through ' +
              'a trusted door. An inline definition has no door. Ship it as a plugin directory instead.',
          );
        }
        ref = { id, inline: e, config: e.config };
      } else {
        ref = { id, config: e.config };
      }
    } else {
      throw usageError(`${where} is neither a name nor a mapping`, 'Write `- baton.claude-code` or `- id: baton.claude-code`.');
    }
    if (seen.has(ref.id)) {
      throw usageError(`${label}: ${ref.id} is listed twice`, 'One entry per facility; two would make "which config won" unanswerable.');
    }
    seen.add(ref.id);
    out.push(ref);
  }
  return out;
}


export function parsePluginManifest(text: string, label: string): Record<string, unknown> {
  let doc: Record<string, unknown>;
  try {
    doc = parse(text) as Record<string, unknown>;
  } catch (err) {
    throw usageError(`${label} is not valid YAML: ${err instanceof Error ? err.message : String(err)}`, 'A manifest is a single YAML document.');
  }
  if (!doc || typeof doc !== 'object') throw usageError(`${label} is empty`, `A plugin manifest needs apiVersion, kind and metadata.id.`);
  if (doc.apiVersion !== PLUGIN_API_VERSION) {
    throw usageError(
      `${label} has apiVersion ${String(doc.apiVersion ?? '(none)')}; this build understands ${PLUGIN_API_VERSION}`,
      `Set apiVersion: ${PLUGIN_API_VERSION}`,
    );
  }
  if (doc.kind !== PLUGIN_KIND) {
    throw usageError(`${label} has kind ${String(doc.kind ?? '(none)')}`, `Set kind: ${PLUGIN_KIND}`);
  }






  if (doc.tools !== undefined) {
    throw usageError(`${label} declares tools`, 'Tools registration is not part of this build (deferred: it has no second consumer yet). Remove the key; the node would refuse the manifest.');
  }
  const known = new Set(['apiVersion', 'kind', 'metadata', 'runtime', 'service', 'instructions', 'state', 'skills', 'hooks']);
  const unknown = Object.keys(doc).filter((k) => !known.has(k));
  if (unknown.length > 0) {




    throw usageError(`${label} has fields this build does not understand: ${unknown.join(', ')}`, 'Check the spelling against the plugin contract.');
  }




  if (doc.runtime !== undefined && doc.service !== undefined) {
    throw usageError(
      `${label} declares both runtime: and service:`,
      'A facility is one of the two. A runtime is the occupant (the image, the terminal, the workspace); ' +
        'a service is a process the daemon runs beside the occupant. Split it into two plugins if it is really both.',
    );
  }
  if (doc.service !== undefined) validateService(doc.service, label);















  const rtSecrets = ((doc.runtime as { execution?: { secrets?: unknown } } | undefined)?.execution)?.secrets;
  for (const entry of Array.isArray(rtSecrets) ? rtSecrets : []) {
    if (entry && typeof entry === 'object') assertSecretSaysRequired(entry as RawSecret, label);
  }
  const meta = (doc.metadata ?? {}) as Record<string, unknown>;
  validatePluginID(meta.id, label);
  const version = meta.version;







  if (typeof version !== 'string') {
    throw usageError(
      `${label}: metadata.version is ${typeof version === 'number' ? `the number ${version}` : String(version ?? '(none)')}`,
      'Write it quoted — `version: "1.0.0"`. An unquoted `1.0` is a number in YAML, and a number cannot carry three parts.',
    );
  }
  if (/[-+]/.test(version)) {
    throw usageError(
      `${label}: metadata.version is ${version}`,
      'Pre-release and build metadata are not accepted yet: they exist so a resolver can rank ' +
        'candidates, and this build has no resolver.',
    );
  }
  if (!/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(version)) {
    throw usageError(
      `${label}: metadata.version is ${version}`,
      'It is MAJOR.MINOR.PATCH — three non-negative integers with no leading zeroes (`1.0.0`, `2.13.4`).',
    );
  }




  if (doc.skills !== undefined) {
    if (!Array.isArray(doc.skills)) throw usageError(`${label}: skills is not a list`, 'Write `skills:` as a list of `{path: skills/<name>}` or `{state: portable/<dir>}` entries.');
    doc.skills.forEach((entry, i) => {
      const e = (entry ?? {}) as { path?: unknown; state?: unknown };
      const extra = Object.keys(e).filter((k) => k !== 'path' && k !== 'state');
      if (extra.length > 0) throw usageError(`${label}: skills[${i}] has fields this build does not understand: ${extra.join(', ')}`, 'An entry is `path` or `state`, nothing else.');
      const hasPath = typeof e.path === 'string' && e.path !== '';
      const hasState = typeof e.state === 'string' && e.state !== '';
      if (hasPath && hasState) throw usageError(`${label}: skills[${i}] gives both path and state`, 'One entry is one place: a shipped directory (`path`) or a run-time one (`state`).');
      if (!hasPath && !hasState) throw usageError(`${label}: skills[${i}] gives neither path nor state`, 'Name a directory: `path: skills/<name>` or `state: portable/<dir>`.');
      if (hasState) {
        const st = e.state as string;
        const [pos, ...rest] = st.split('/');
        if ((pos !== 'portable' && pos !== 'local') || rest.length === 0 || rest.join('/') === '' || rest.includes('..')) {
          throw usageError(`${label}: skills[${i}].state is \`${st}\``, 'It is `portable/<dir>` or `local/<dir>` — the prefix says whether the skills grown there travel with the workspace.');
        }
      }
    });
  }





  if (doc.instructions !== undefined) {
    const ins = doc.instructions;
    if (!ins || typeof ins !== 'object' || Array.isArray(ins)) {
      throw usageError(`${label}: instructions is not a mapping`, 'Write `instructions: {path: <file the runtime reads>, file: <file in this plugin>}`.');
    }
    const extra = Object.keys(ins).filter((k) => k !== 'path' && k !== 'file');
    if (extra.length > 0) {
      throw usageError(
        `${label}: instructions has fields this build does not understand: ${extra.join(', ')}`,
        'It is `path` and `file` — a file in the plugin, appended once. There is no one-line form.',
      );
    }
    const { path: ipath, file } = ins as { path?: unknown; file?: unknown };
    if (typeof ipath !== 'string' || ipath === '') {
      throw usageError(`${label}: instructions.path is empty`, 'Name the file the runtime reads at start — absolute, or under $HOME.');
    }
    const surface = contractSurface(ipath);
    if (surface) {
      throw usageError(
        `${label}: instructions.path \`${ipath}\` points into ${surface}`,
        'A facility may not write into a surface whose bytes belong to someone else: ' +
          `${BATON_DIR}/ is BATON's own contract face, ${BATON_PAGE} is BATON speaking to the occupant, ` +
          `and ${ROOT_NORMS.map((n) => `${n}/`).join(', ')} belongs to the workspace's owner.`,
      );
    }
    if (typeof file !== 'string' || file === '' || isAbsolute(file) || file.split('/').includes('..')) {
      throw usageError(
        `${label}: instructions.file is ${typeof file === 'string' ? `\`${file}\`` : 'missing'}`,
        'Name a file inside this plugin, relative to it, like `instructions.md`.',
      );
    }
  }
  return doc;
}


export interface LedgerLine {
  digest: string;
  door: Door;
  path: string;
}


export function readLedger(dataDir: string): LedgerLine[] {
  const p = join(pluginsDir(dataDir), PROVENANCE_FILE);
  if (!existsSync(p)) return [];
  const out: LedgerLine[] = [];
  for (const [i, line] of readFileSync(p, 'utf8').split('\n').entries()) {
    const t = line.trim();
    if (t === '' || t.startsWith('#')) continue;
    const m = /^([0-9a-f]{64})\s{2}(seeded|installed|adopted)\s{2}(.+)$/.exec(t);
    if (!m) {
      throw preconditionError(
        `${p}:${i + 1} is not \`<sha256>  <door>  <path>\``,
        'The ledger is what says which door a plugin came through. A line this build cannot read is a ' +
          'plugin whose provenance is unknown, and guessing it is the one thing this file exists to prevent.',
      );
    }
    out.push({ digest: m[1]!, door: m[2] as Door, path: m[3]! });
  }
  return out;
}


export function doorOf(id: string, ledger: LedgerLine[]): Door | undefined {
  const prefix = `${id}/`;
  let door: Door | undefined;
  for (const l of ledger) {
    if (!l.path.startsWith(prefix)) continue;




    if (door !== undefined && door !== l.door) return undefined;
    door = l.door;
  }
  return door;
}









export function resolvePlugin(ref: PluginRef, dataDir: string, label: string, ledger: LedgerLine[]): ResolvedPlugin {
  if (ref.inline) {
















    const doc = { apiVersion: PLUGIN_API_VERSION, kind: PLUGIN_KIND, metadata: { id: ref.id, version: INLINE_VERSION }, ...ref.inline };
    delete (doc as Record<string, unknown>).id;
    delete (doc as Record<string, unknown>).config;
    return {
      id: ref.id,




      version: INLINE_VERSION,
      dir: undefined,
      manifest: doc,
      runtime: (ref.inline.runtime as Record<string, unknown> | undefined),
      service: undefined,
      config: ref.config,
      door: undefined,
    };
  }

  const dir = looksLikePluginPath(ref.id) ? resolve(ref.id) : join(pluginsDir(dataDir), ref.id);
  const manifestPath = join(dir, PLUGIN_MANIFEST);
  if (!existsSync(manifestPath)) {
    const have = existsSync(pluginsDir(dataDir))
      ? readdirSync(pluginsDir(dataDir)).filter((f) => {
          try { return statSync(join(pluginsDir(dataDir), f)).isDirectory(); } catch { return false; }
        })
      : [];
    throw preconditionError(
      `${label}: no plugin ${ref.id} at ${dir}`,
      have.length > 0
        ? `Installed here: ${have.join(', ')}. \`baton init\` seeds the ones this build ships.`
        : `Nothing is installed in ${pluginsDir(dataDir)}. \`baton init\` seeds the ones this build ships.`,
    );
  }
  const doc = parsePluginManifest(readFileSync(manifestPath, 'utf8'), `${ref.id}/${PLUGIN_MANIFEST}`);
  const meta = doc.metadata as Record<string, unknown>;
  if (meta.id !== ref.id) {
    throw usageError(
      `${label}: ${dir} holds a plugin that calls itself ${String(meta.id)}`,
      'A plugin answers to where it was installed: the directory name and metadata.id must match, because ' +
        'the id is what keys its state and its skill names.',
    );
  }
  return {
    id: ref.id,
    version: meta.version as string,
    dir,
    manifest: doc,
    runtime: doc.runtime as Record<string, unknown> | undefined,
    service: doc.service === undefined ? undefined : validateService(doc.service, `${ref.id}/${PLUGIN_MANIFEST}`),
    config: ref.config,
    door: doorOf(ref.id, ledger),
  };
}


export interface Resolution {
  plugins: ResolvedPlugin[];





  runtime: Record<string, unknown> | undefined;


  runtimeFrom: string | undefined;
}















export function resolvePlugins(refs: PluginRef[], dataDir: string, label: string): Resolution {
  const ledger = readLedger(dataDir);
  const plugins = refs.map((r) => resolvePlugin(r, dataDir, label, ledger));




  const suppliers = plugins.filter((p) => p.runtime !== undefined);

















  const supplier = suppliers[0];
  if (supplier === undefined) {





    return { plugins, runtime: undefined, runtimeFrom: undefined };
  }
  const runtime = supplier.runtime as Record<string, unknown> | undefined;
  if (!runtime || typeof runtime !== 'object' || Array.isArray(runtime)) {
    throw usageError(`${label}: ${supplier.id} has a runtime that is not a mapping`, 'runtime: is the AgentRuntime document — package, adapter, execution.');
  }





  const rtSecrets = (runtime.execution as { secrets?: unknown } | undefined)?.secrets;
  const where = supplier.dir === undefined ? `${label} (inline ${supplier.id})` : `${supplier.id}/${PLUGIN_MANIFEST}`;
  for (const entry of Array.isArray(rtSecrets) ? rtSecrets : []) {
    if (entry && typeof entry === 'object') assertSecretSaysRequired(entry as RawSecret, where);
  }
  return { plugins, runtime, runtimeFrom: supplier.id };
}












export function parcelsOf(plugins: ResolvedPlugin[]): Extension[] {
  return plugins
    .filter((p) => p.config !== undefined)
    .map((p) => ({ provider: p.id, config: p.config as Record<string, unknown> }));
}








export function ledgerFor(ids: string[], dataDir: string): string {
  const want = new Set(ids);
  const keep = readLedger(dataDir).filter((l) => want.has(l.path.split('/')[0] ?? ''));
  keep.sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  return keep.length ? keep.map((l) => `${l.digest}  ${l.door}  ${l.path}`).join('\n') + '\n' : '';
}


export function resolutionSummary(r: Resolution): string {
  const inline = r.plugins.filter((p) => p.dir === undefined).length;
  const named = r.plugins.length - inline;
  return `${r.plugins.length} plugins (${named} installed, ${inline} inline), runtime from ${r.runtimeFrom}`;
}






export function ledgerLine(l: LedgerLine): string {
  return `${l.digest}  ${l.door}  ${l.path}`;
}











export function writeLedger(dataDir: string, lines: LedgerLine[]): void {
  const dest = pluginsDir(dataDir);
  mkdirSync(dest, { recursive: true, mode: 0o700 });
  const text = [...lines].map(ledgerLine).sort();
  writeFileSync(join(dest, PROVENANCE_FILE), text.length ? text.join('\n') + '\n' : '', { mode: 0o600 });
}










export function walkFiles(root: string, prefix = ''): string[] {
  const out: string[] = [];
  for (const e of readdirSync(join(root, prefix), { withFileTypes: true })) {
    const rel = prefix ? join(prefix, e.name) : e.name;
    if (e.isSymbolicLink()) {
      throw usageError(
        `${join(root, rel)} is a symbolic link`,
        'A plugin is the bytes in its directory — a link names bytes outside it, which the ledger cannot attest ' +
          'and the node would refuse. Replace the link with the file.',
      );
    }
    if (e.isDirectory()) out.push(...walkFiles(root, rel));
    else if (e.isFile()) out.push(rel);
  }
  return out.sort();
}


export function digestLines(dir: string, id: string, door: Door): LedgerLine[] {
  return walkFiles(dir).map((rel) => ({
    digest: createHash('sha256').update(readFileSync(join(dir, rel))).digest('hex'),
    door,
    path: `${id}/${rel.split(sep).join('/')}`,
  }));
}


export interface Installed {
  id: string;
  version: string;
  dir: string;
  files: number;

  supplies_runtime: boolean;
}














export function installPlugin(dataDir: string, from: string): Installed {
  let st;
  try { st = statSync(from); } catch {
    throw usageError(`${from} does not exist`, 'Give the directory a plugin was unpacked into — the one holding manifest.yaml.');
  }
  if (!st.isDirectory()) {
    throw usageError(`${from} is not a directory`, 'Archives are not accepted: unpack it first and give the directory.');
  }
  const manifestPath = join(from, PLUGIN_MANIFEST);
  if (!existsSync(manifestPath)) {
    throw usageError(`${from} has no ${PLUGIN_MANIFEST}`, `A plugin is a directory with ${PLUGIN_MANIFEST} at its top. Is this the directory, or its parent?`);
  }
  const doc = parsePluginManifest(readFileSync(manifestPath, 'utf8'), `${basename(from)}/${PLUGIN_MANIFEST}`);
  const meta = doc.metadata as { id: string; version: string };






  if (meta.id.startsWith('baton.')) {
    throw usageError(
      `${meta.id} is in the baton. namespace, which is reserved for the facilities this build ships`,
      'Use your own namespace, e.g. `acme.<name>`: metadata.id is the directory it lands in and the prefix every readout shows.',
    );
  }
  const files = walkFiles(from);
  const dest = join(pluginsDir(dataDir), meta.id);
  if (existsSync(dest)) {
    throw new BatonError({
      code: 'CONFLICT',
      message: `${meta.id} is already installed at ${dest}`,
      remediation:
        `To upgrade: \`baton plugin remove ${meta.id}\` and then install again. There is no --replace — ` +
        'removal is a complete verb (links, instruction block, office state), and an in-place overwrite would skip it.',
      exitCode: ExitCode.CONFLICT,
    });
  }
  mkdirSync(pluginsDir(dataDir), { recursive: true, mode: 0o700 });
  cpSync(from, dest, { recursive: true, errorOnExist: true, dereference: false });
  const kept = readLedger(dataDir).filter((l) => !l.path.startsWith(`${meta.id}/`));
  writeLedger(dataDir, [...kept, ...digestLines(dest, meta.id, 'installed')]);
  return { id: meta.id, version: meta.version, dir: dest, files: files.length, supplies_runtime: doc.runtime !== undefined };
}


export function templatesReferencing(dataDir: string, id: string): string[] {
  const dir = join(dataDir, 'templates');
  if (!existsSync(dir)) return [];
  const out: string[] = [];
  for (const f of readdirSync(dir).filter((x) => x.endsWith('.yaml')).sort()) {
    try {
      const doc = parse(readFileSync(join(dir, f), 'utf8')) as { plugins?: unknown };
      const refs = Array.isArray(doc?.plugins) ? doc.plugins : [];
      const names = refs.map((r) => (typeof r === 'string' ? r : (r as { id?: unknown })?.id)).filter((x) => typeof x === 'string');
      if (names.includes(id)) out.push(basename(f, '.yaml'));
    } catch {


    }
  }
  return out;
}


export interface Removed {
  id: string;
  door: Door | undefined;
  files: number;
}













export function removePlugin(dataDir: string, id: string, opts: { force: boolean }): Removed {
  validatePluginID(id, 'remove');
  const dir = join(pluginsDir(dataDir), id);
  if (!existsSync(dir)) {
    const have = existsSync(pluginsDir(dataDir))
      ? readdirSync(pluginsDir(dataDir)).filter((f) => { try { return statSync(join(pluginsDir(dataDir), f)).isDirectory(); } catch { return false; } })
      : [];
    throw preconditionError(`${id} is not installed here`, have.length ? `Installed: ${have.join(', ')}.` : `Nothing is installed in ${pluginsDir(dataDir)}.`);
  }
  const ledger = readLedger(dataDir);
  const door = doorOf(id, ledger);
  if (door === 'seeded') {
    throw new BatonError({
      code: 'CONFLICT',
      message: `${id} came with this build (door: seeded) and is not removed`,
      remediation: 'A shipped facility is put back by `baton init`; removing it would only make the next init look like a change. ' +
        'To stop a node getting it, take it out of the template.',
      exitCode: ExitCode.CONFLICT,
    });
  }
  const named = templatesReferencing(dataDir, id);
  if (named.length > 0 && !opts.force) {
    throw new BatonError({
      code: 'CONFLICT',
      message: `${id} is named by ${named.length} template(s) here: ${named.join(', ')}`,
      remediation: 'A create from any of them would fail on a missing facility. Take it out of the template first, or pass --force to remove it anyway.',
      exitCode: ExitCode.CONFLICT,
      details: { templates: named },
    });
  }
  const files = ledger.filter((l) => l.path.startsWith(`${id}/`)).length;
  rmSync(dir, { recursive: true, force: true });
  writeLedger(dataDir, ledger.filter((l) => !l.path.startsWith(`${id}/`)));
  return { id, door, files };
}









export function pluginView(pl: ResolvedPlugin): PluginView {
  const skills = (pl.manifest.skills as { path?: string; state?: string }[] | undefined) ?? [];
  return {
    id: pl.id,
    version: pl.version,
    inline: pl.dir === undefined,
    door: pl.door ?? null,
    supplies_runtime: pl.runtime !== undefined,




    service: pl.service === undefined ? null : {
      command: pl.service.command,
      user: pl.service.user ?? null,
      health: pl.service.health?.probe ?? 'none',
    },




    secrets: secretsOf(pl),


    skills: skills.filter((sk) => sk.path).map((sk) => (sk.path ?? '').split('/').pop() ?? ''),
    skill_sources: skills.filter((sk) => sk.state).map((sk) => sk.state as string),
    state: ((pl.manifest.state as { path?: string; belongs_to?: string }[] | undefined) ?? []).map(
      (st) => ({ path: st.path, belongs_to: st.belongs_to }),
    ),
    config: pl.config,
  };
}

export interface PluginView {
  id: string;
  version: string;
  inline: boolean;
  door: Door | null;
  supplies_runtime: boolean;
  service: { command: string[]; user: string | null; health: string } | null;
  secrets: DeclaredSecret[];
  skills: string[];
  skill_sources: string[];
  state: { path: string | undefined; belongs_to: string | undefined }[];
  config: unknown;
}


export type StoreEntry = (PluginView & { error: null }) | { id: string; error: string };


export function listPlugins(dataDir: string): StoreEntry[] {
  const dir = pluginsDir(dataDir);
  if (!existsSync(dir)) return [];
  const ledger = readLedger(dataDir);
  const out: StoreEntry[] = [];
  for (const f of readdirSync(dir).sort()) {
    let isDir = false;
    try { isDir = statSync(join(dir, f)).isDirectory(); } catch {  }
    if (!isDir) continue;
    try {
      out.push({ ...pluginView(resolvePlugin({ id: f }, dataDir, 'plugin list', ledger)), error: null });
    } catch (err) {


      out.push({ id: f, error: err instanceof Error ? err.message : String(err) });
    }
  }
  return out;
}























function contractSurface(p: string): string {
  const cleaned = posix.normalize(p.replace(/\\/g, '/'));
  const segments = cleaned.split('/');
  if (segments[segments.length - 1] === BATON_PAGE) return BATON_PAGE;
  for (const seg of segments) {
    if (seg === BATON_DIR) return `${BATON_DIR}/`;
    for (const norm of ROOT_NORMS) if (seg === norm) return `${norm}/`;
  }
  return '';
}
