// SPDX-License-Identifier: Apache-2.0




























import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { basename, dirname, isAbsolute, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse, stringify } from 'yaml';

import { preconditionError, usageError } from '../errors.js';
import { cpuCores, memoryBytes } from './spec.js';
import { type Extension } from './extensions.js';
import { type DeclaredSecret, parsePluginList, type ResolvedPlugin, resolvePlugins, secretsOf } from './plugins.js';

const API_VERSION = 'baton.mailloop.dev/v1alpha1';




const KIND = 'WorkspaceTemplate';
const NAME = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/;

export interface WorkspaceTemplate {

  name: string;

  path: string;

  node: NodeSection;


















  runtime: Record<string, unknown> | undefined;

  official: boolean;













  version: string | null;

  plugins: ResolvedPlugin[];










  runtimeFrom: string | undefined;





















  skills: TemplateSkill[];
}









export interface TemplateSkill {

  name: string;

  ref: string;
}

















export function readTemplateHead(path: string): { name: string; official: boolean } | null {
  let doc: Record<string, unknown>;
  try {
    doc = parse(readFileSync(path, 'utf8')) as Record<string, unknown>;
  } catch {
    return null;
  }
  if (!doc || typeof doc !== 'object' || doc.kind !== KIND) return null;
  const meta = (doc.metadata ?? {}) as Record<string, unknown>;
  if (typeof meta.name !== 'string' || !NAME.test(meta.name)) return null;
  return { name: meta.name, official: meta.official === true };
}


export function templatesDir(dataDir: string): string {
  return join(dataDir, 'templates');
}





























export function looksLikePath(ref: string): boolean {
  return ref.includes('/') || ref.includes('\\') || /\.ya?ml$/i.test(ref) || isAbsolute(ref);
}







export function resolveTemplateRef(ref: string, dataDir: string): string {
  if (looksLikePath(ref)) return resolve(ref);


  if (!NAME.test(ref)) {
    throw usageError(
      `${ref} is neither a template name nor a path`,
      'A name is lowercase letters, digits and interior hyphens; a path contains a slash or ends in .yaml.',
    );
  }





  const dir = templatesDir(dataDir);
  const all = listTemplates(dataDir);
  const hits = all.filter((t) => t.error === null && t.name === ref);
  if (hits.length === 1) return hits[0]!.path;
  if (hits.length > 1) {
    throw preconditionError(
      `${hits.length} templates in ${dir} are named ${ref}`,
      `One name, one template. Files: ${hits.map((h) => basename(h.path)).join(', ')}.`,
    );
  }
  const known = all.filter((t) => t.error === null).map((t) => t.name);
  const broken = all.filter((t) => t.error !== null);





  if (known.length === 0 && broken.length > 0) {
    throw preconditionError(
      `this machine has ${broken.length} template(s) in ${dir} and none of them can be read`,
      `The first: ${basename(broken[0]!.path)} — ${broken[0]!.error}. Fix that (it is usually the plugin store, not the template), ` +
        'or give a path to a Node Template file.',
    );
  }
  throw preconditionError(
    `this machine has no template called ${ref}`,
    known.length > 0
      ? `In ${dir}: ${known.join(', ')}${broken.length ? ` (${broken.length} more unreadable — \`baton template list\` says why)` : ''}. Or give a path to a Node Template file.`
      : `${dir} has no templates yet. Put a Node Template there (any file name; its metadata.name is what you give here), or give a path to one.`,
  );
}

function listTemplateFiles(dir: string): string[] {
  if (!existsSync(dir)) return [];








  return readdirSync(dir)
    .filter((f) => f.endsWith('.yaml'))
    .sort()
    .map((f) => join(dir, f));
}


export function loadWorkspaceTemplate(path: string, dataDir: string): WorkspaceTemplate {
  if (!existsSync(path)) {
    throw preconditionError(`${path} does not exist`, 'Point --template at a Workspace Template file, or give a template name.');
  }
  return parseWorkspaceTemplate(readFileSync(path, 'utf8'), path, dataDir);
}









export function parseWorkspaceTemplate(text: string, label: string, dataDir: string): WorkspaceTemplate {
  let doc: Record<string, unknown>;
  try {
    doc = parse(text) as Record<string, unknown>;
  } catch (err) {
    throw usageError(
      `${label} is not valid YAML: ${err instanceof Error ? err.message : String(err)}`,
      'Check the indentation; a template is a single YAML document.',
    );
  }
  if (!doc || typeof doc !== 'object') {
    throw usageError(`${label} is empty`, 'A Workspace Template needs apiVersion, kind and metadata; its runtime comes from a plugin listed under plugins:.');
  }
  if (doc.apiVersion !== API_VERSION) {
    throw usageError(
      `${label} has apiVersion ${String(doc.apiVersion ?? '(none)')}; this build understands ${API_VERSION}`,
      `Set apiVersion: ${API_VERSION}`,
    );
  }
  if (doc.kind !== KIND) {



    const hint = doc.kind === 'AgentRuntime'
      ? 'This is a runtime spec by itself. A user file is a Workspace Template, and a bare AgentRuntime at the top level is refused rather than wrapped: put this document ' +
        'inside a plugin — `plugins: [{id: local.<name>, runtime: {…}}]` — with `kind: WorkspaceTemplate`, ' +
        '`metadata.name` and `node: {}` above it. Or install it as a facility and name it.'
      : `Set kind: ${KIND}`;
    throw usageError(`${label} has kind ${String(doc.kind ?? '(none)')}`, hint);
  }
  const meta = (doc.metadata ?? {}) as Record<string, unknown>;
  const name = meta.name;
  if (typeof name !== 'string' || !NAME.test(name)) {
    throw usageError(
      `${label} has no valid metadata.name`,
      'A template is named like a node: lowercase letters, digits and interior hyphens, 1-40 characters.',
    );
  }





  if (meta.version !== undefined && typeof meta.version !== 'string') {
    throw usageError(
      `${label}: metadata.version must be a string`,
      'Quote it — `version: "1.0"`. A bare 1.0 is the number 1 by the time it gets here, and that is not what was written.',
    );
  }
  const version = typeof meta.version === 'string' && meta.version.trim() !== '' ? meta.version : null;








  if (doc.node === undefined) {
    throw usageError(
      `${label}: no node section`,
      'A Workspace Template states its hardware: `node:` with vcpu, memory, volumeSize, transfer, ' +
        'bandwidth (volumeSize sizes the node\'s data volume only, not the container\'s root filesystem). The software half — `plugins:` — is optional and may be empty.',
    );
  }












  if (typeof doc.node !== 'object' || doc.node === null || Array.isArray(doc.node)) {
    throw usageError(`${label}: node must be a mapping`, 'node: holds the hardware configuration — vcpu, memory, volumeSize, transfer, bandwidth; volumeSize sizes the node\'s data volume only, not the container\'s root filesystem.');
  }




  const node = { ...NODE_DEFAULTS, ...(doc.node as Record<string, unknown>) };
  validateNodeSection(label, node as Record<string, unknown>);
















  for (const [gone, hint] of [
    ['runtime', 'Move that document into a plugin and list it: `plugins: [<id>]`. For a one-off, define it ' +
      'inline: `plugins: [{id: local.<name>, runtime: {…}}]`.'],
    ['extensions', 'A parcel is now a plugin entry with a `config`: `plugins: [{id: <provider>, config: {…}}]`. ' +
      'What BATON does with it is unchanged — written verbatim, never read.'],
  ] as const) {
    if (doc[gone] !== undefined) {
      throw usageError(`${label} still has a top-level \`${gone}:\` (removed 2026-09-01)`, hint);
    }
  }
  const resolution = resolvePlugins(parsePluginList(doc.plugins, label), dataDir, label);








  const known = new Set(['apiVersion', 'kind', 'metadata', 'node', 'plugins', 'skills']);
  const unknown = Object.keys(doc).filter((k) => !known.has(k));
  if (unknown.length > 0) {
    throw usageError(
      `${label} has fields this build does not understand: ${unknown.join(', ')}`,
      'A Workspace Template has apiVersion, kind, metadata, node, plugins, skills — nothing else (v1alpha1 is strict on purpose).',
    );
  }
  const skills = parseSkillList(doc.skills, label);
  return {
    name, path: label, node: node as NodeSection, runtime: resolution.runtime, skills,
    official: meta.official === true,
    version,
    plugins: resolution.plugins,
    runtimeFrom: resolution.runtimeFrom,
  };
}









export const NODE_FIELDS = ['vcpu', 'memory', 'volumeSize', 'transfer', 'bandwidth'] as const;
export type NodeSection = Partial<Record<(typeof NODE_FIELDS)[number], string>>;




























export const NODE_DEFAULTS: NodeSection = { vcpu: '2', memory: '2G', volumeSize: '20G' };













export function parseSkillList(raw: unknown, label: string): TemplateSkill[] {
  if (raw === undefined) return [];
  if (!Array.isArray(raw)) {
    throw usageError(`${label}: skills must be a list`, 'Each entry is a name or a path, or a mapping with `from:` and an optional `as:`.');
  }
  const seen = new Set<string>();
  const out: TemplateSkill[] = [];
  for (const [i, entry] of raw.entries()) {
    const where = `${label}: skills[${i}]`;
    let ref: string;
    let as: string | undefined;
    if (typeof entry === 'string') {
      ref = entry;
    } else if (entry && typeof entry === 'object' && !Array.isArray(entry)) {
      const e = entry as Record<string, unknown>;
      const k = new Set(['from', 'as']);
      const extra = Object.keys(e).filter((x) => !k.has(x));
      if (extra.length > 0) {
        throw usageError(
          `${where} has fields this build does not understand: ${extra.join(', ')}`,
          'A skill entry is `from:` and an optional `as:`. A facility is what carries a manifest, a door and code — this is not one.',
        );
      }
      if (typeof e.from !== 'string' || e.from === '') {
        throw usageError(`${where} needs a from`, 'Give the name or the path of the directory holding its SKILL.md.');
      }
      ref = e.from;
      if (e.as !== undefined) {
        if (typeof e.as !== 'string' || e.as === '') throw usageError(`${where}: as must be a name`, 'Leave it out to use the directory\'s own name.');
        as = e.as;
      }
    } else {
      throw usageError(`${where} is not a name or a mapping`, 'Each entry is a name or a path, or a mapping with `from:` and an optional `as:`.');
    }
    if (ref.trim() === '') throw usageError(`${where} is empty`, 'Give the name or the path of the directory holding its SKILL.md.');



    const name = as ?? basename(ref.replace(/[/\\]+$/, ''));
    if (!SKILL_NAME.test(name)) {
      throw usageError(
        `${where}: ${name} is not a skill name`,
        'A skill is named like a node: lowercase letters, digits and interior hyphens, 1-40 characters. Use `as:` when the directory is not named that way.',
      );
    }



    if (seen.has(name)) throw usageError(`${where}: ${name} is listed twice`, 'Use `as:` to expose one of them under another name.');
    seen.add(name);
    out.push({ name, ref });
  }
  return out;
}















export const STOCKED_FILE = '.stocked';


const SKILL_NAME = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/;













export function skillsDir(dataDir: string): string {
  return join(dataDir, 'skills');
}


export interface ResolvedTemplateSkill {

  name: string;

  dir: string;

  ref: string;
}















export function resolveTemplateSkills(
  skills: TemplateSkill[],
  dataDir: string,
  label: string,
): ResolvedTemplateSkill[] {
  return skills.map((s) => {




    const isPath = s.ref.includes('/') || s.ref.includes('\\') || isAbsolute(s.ref);
    const dir = isPath ? resolve(s.ref) : join(skillsDir(dataDir), s.ref);
    if (!existsSync(dir)) {
      throw usageError(
        `${label}: skill ${s.ref} is not here`,
        isPath
          ? `Looked for ${dir}.`
          : `A bare name is looked up in ${skillsDir(dataDir)}. Put the directory there, or write a path.`,
      );
    }
    if (!statSync(dir).isDirectory()) {
      throw usageError(`${label}: skill ${s.ref} is not a directory`, `${dir} is a file. A skill is the directory that holds its SKILL.md.`);
    }
    if (!existsSync(join(dir, 'SKILL.md'))) {
      throw usageError(
        `${label}: ${dir} has no SKILL.md`,
        'That file is what makes a directory a skill. Without it the node would come up with the name in its readout and nothing behind it.',
      );
    }
    return { name: s.name, dir, ref: s.ref };
  });
}

function validateNodeSection(path: string, node: Record<string, unknown>): void {







  if (Object.prototype.hasOwnProperty.call(node, 'disk')) {
    throw usageError(
      `${path}: node.disk was renamed node.volumeSize`,
      'It sizes the node\'s data VOLUME, not the whole disk — the rootfs is shared and never was covered. Write volumeSize: "20G".',
    );
  }
  const known = new Set<string>(NODE_FIELDS);
  const unknown = Object.keys(node).filter((k) => !known.has(k));
  if (unknown.length > 0) {
    throw usageError(
      `${path}: node has fields this build does not understand: ${unknown.join(', ')}`,
      `node: takes ${NODE_FIELDS.join(', ')} — nothing else. isolation, region, count, retention and model each have their home elsewhere.`,
    );
  }
  for (const k of NODE_FIELDS) {
    const v = node[k];
    if (v === undefined || v === null) continue;
    if (typeof v !== 'string') {
      throw usageError(
        `${path}: node.${k} must be a string quantity, got ${typeof v}`,
        k === 'vcpu' ? 'Write "2" or "500m" — a Kubernetes quantity, quoted.' :
        k === 'memory' || k === 'volumeSize' ? 'Write "8G" or "20G" — a Kubernetes quantity, quoted.' :
        'Write it as the provider spells it, quoted: "2 TB", "1 Gbps".',
      );
    }
    if (k === 'vcpu' && !/^[0-9]+(\.[0-9]+)?m?$/.test(v)) throw usageError(`${path}: node.vcpu is ${v}`, 'A Kubernetes CPU quantity: "2", "0.5", "500m".');
    if ((k === 'memory' || k === 'volumeSize') && !/^[0-9]+(\.[0-9]+)?([EPTGMK]i?)?$/.test(v)) throw usageError(`${path}: node.${k} is ${v}`, 'A Kubernetes quantity: "512M", "8G", "20G".');
  }
}










export function materialise(t: WorkspaceTemplate, dataDir: string, nodeName: string): { runtimeFile: string; templateCopy: string } {
  const dir = join(dataDir, 'compose');
  mkdirSync(dir, { recursive: true });
  const runtimeFile = join(dir, `agent-${nodeName}.runtime.yaml`);
  const templateCopy = join(dir, `agent-${nodeName}.template.yaml`);


















  if (t.runtime === undefined) {
    writeFileSync(templateCopy, readFileSync(t.path), { mode: 0o644 });
    return { runtimeFile: '', templateCopy };
  }
  const rt = JSON.parse(JSON.stringify(t.runtime)) as {
    metadata?: { name?: string; labels?: Record<string, string> };
    execution?: { resources?: { cpu?: string; memory?: string } };
  };



















  const labels: Record<string, string> = { ...(rt.metadata?.labels ?? {}), template: t.name };
  if (t.runtimeFrom) labels.supplier = t.runtimeFrom;
  else delete labels.supplier;
  rt.metadata = { ...(rt.metadata ?? {}), labels };



















  const res = rt.execution?.resources;
  if (res) {
    if (res.cpu !== undefined) res.cpu = cpuCores(res.cpu, t.path) ?? res.cpu;
    if (res.memory !== undefined) res.memory = memoryBytes(res.memory, t.path) ?? res.memory;
  }

  writeFileSync(runtimeFile, stringify(rt, { lineWidth: 0 }), { mode: 0o644 });
  writeFileSync(templateCopy, readFileSync(t.path), { mode: 0o644 });
  return { runtimeFile, templateCopy };
}











export function boundMounts(dataDir: string, nodeName: string): Map<string, string> {
  const file = join(dataDir, 'compose', `agent-${nodeName}.yml`);
  const out = new Map<string, string>();
  if (!existsSync(file)) return out;
  let doc: { services?: Record<string, { volumes?: unknown[] }> } | null;
  try { doc = parse(readFileSync(file, 'utf8')) as typeof doc; } catch { return out; }
  for (const svc of Object.values(doc?.services ?? {})) {
    for (const v of svc.volumes ?? []) {
      if (typeof v === 'string') {
        const parts = v.split(':');
        if (parts.length >= 2 && parts[0]!.startsWith('/')) out.set(parts[1]!, parts[0]!);
      } else if (v && typeof v === 'object') {
        const m = v as { source?: unknown; target?: unknown; type?: unknown };
        if (typeof m.target === 'string' && typeof m.source === 'string' && m.type === 'bind') out.set(m.target, m.source);
      }
    }
  }
  return out;
}
















export function shippedTemplatesDir(): string {
  const here = dirname(fileURLToPath(import.meta.url));
  const compiled = join(here, '..', 'templates');
  if (existsSync(compiled)) return compiled;

  return join(here, '..', '..', 'dist', 'templates');
}






















export function shippedOfficialNames(): Set<string> {
  const dir = shippedTemplatesDir();
  if (!existsSync(dir)) return new Set();
  const out = new Set<string>();
  for (const f of readdirSync(dir)) {
    if (!f.endsWith('.yaml')) continue;
    try {
      if (readTemplateHead(join(dir, f))?.official === true) out.add(f);
    } catch {


    }
  }
  return out;
}













export function officialRegistry(): { provider: string; templates: Record<string, { id: string; spec_sha256: string }> } | null {
  try {
    const raw = readFileSync(join(shippedTemplatesDir(), 'official-registry.json'), 'utf8');
    const d = JSON.parse(raw) as Record<string, unknown>;
    const provider = typeof d.provider === 'string' ? d.provider : '';
    const templates = (d.templates ?? {}) as Record<string, { id: string; spec_sha256: string }>;
    return provider ? { provider, templates } : null;
  } catch {


    return null;
  }
}










function standing(
  path: string,
  officialNames: Set<string>,
  registry: { provider: string; templates: Record<string, { id: string }> } | null,
  prov: { source_network: string | null; source_resource_id: string | null },
): { source: 'npm' | 'local' | 'batoncloud' | 'other'; official: boolean; template_id: string | null } {
  const shipped = officialNames.has(basename(path));


  const reg = shipped ? registry?.templates[basename(path, '.yaml')] : undefined;
  if (reg) {
    const source = registry!.provider === 'batoncloud' ? 'batoncloud' : 'other';
    return { source, official: true, template_id: reg.id };
  }
  const fetched = prov.source_resource_id !== null && prov.source_resource_id !== '';
  if (fetched) {
    return {
      source: prov.source_network === 'batoncloud' ? 'batoncloud' : 'other',
      official: shipped,
      template_id: prov.source_resource_id,
    };
  }
  return { source: shipped ? 'npm' : 'local', official: shipped, template_id: null };
}









export function differsFromShipped(path: string): boolean | null {
  const shipped = join(shippedTemplatesDir(), basename(path));
  if (!existsSync(shipped)) return null;
  try {
    return readFileSync(shipped, 'utf8') !== readFileSync(path, 'utf8');
  } catch {
    return null;
  }
}







function provenanceOf(path: string): { source_network: string | null; source_resource_id: string | null; fetched_at: string | null; hash: string | null } {
  const none = { source_network: null, source_resource_id: null, fetched_at: null, hash: null };
  try {
    const raw = readFileSync(path + '.source.json', 'utf8');
    const s = JSON.parse(raw) as Record<string, unknown>;
    return {
      source_network: typeof s.source_network === 'string' ? s.source_network : null,
      source_resource_id: typeof s.source_resource_id === 'string' ? s.source_resource_id : null,
      fetched_at: typeof s.fetched_at === 'string' ? s.fetched_at : null,
      hash: typeof s.hash === 'string' && s.hash ? s.hash : null,
    };
  } catch {
    return none;
  }
}








export function declaredSecrets(t: WorkspaceTemplate): DeclaredSecret[] {
  return t.plugins.flatMap((p) => secretsOf(p));
}

export function listTemplates(dataDir: string): {
  name: string; path: string;















  source: 'npm' | 'local' | 'batoncloud' | 'other';











  official: boolean;






  template_id: string | null;









  version: string | null;
  image: string | null; cpu: string | null; memory: string | null;

  node: { vcpu: string | null; memory: string | null; volumeSize: string | null; transfer: string | null; bandwidth: string | null } | null;








  secrets: { name: string | null; label: string | null; provider: string | null; help_url: string | null; required: boolean | null; plugin: string }[];

  differs_from_shipped: boolean | null;

  source_network: string | null;
  source_resource_id: string | null;
  fetched_at: string | null;
  hash: string | null;
  error: string | null;
}[] {


  const officialNames = shippedOfficialNames();
  const registry = officialRegistry();
  return listTemplateFiles(templatesDir(dataDir)).map((path) => {
    try {
      const t = loadWorkspaceTemplate(path, dataDir);





      const rt = t.runtime as {
        package?: { image?: string };
        execution?: { resources?: { cpu?: string; memory?: string } };
      } | undefined;
      const prov = provenanceOf(path);
      const nd = t.node ?? {};
      const nodeStr = (k: string) => (nd as Record<string, unknown>)[k] != null ? String((nd as Record<string, unknown>)[k]) : null;
      const node = Object.keys(nd).length ? { vcpu: nodeStr('vcpu'), memory: nodeStr('memory'), volumeSize: nodeStr('volumeSize'), transfer: nodeStr('transfer'), bandwidth: nodeStr('bandwidth') } : null;
      return {
        name: t.name, path,
        ...standing(path, officialNames, registry, prov),
        version: t.version,
        image: rt?.package?.image ?? null,
        node,
        secrets: declaredSecrets(t),
        differs_from_shipped: differsFromShipped(path),



        cpu: rt?.execution?.resources?.cpu != null ? String(rt.execution.resources.cpu) : null,
        memory: rt?.execution?.resources?.memory != null ? String(rt.execution.resources.memory) : null,
        ...prov,
        error: null,
      };
    } catch (err) {


      return {
        name: basename(path, '.yaml'), path,


        ...standing(path, officialNames, registry, provenanceOf(path)),


        version: null,
        image: null, cpu: null, memory: null, node: null, secrets: [], differs_from_shipped: differsFromShipped(path),
        ...provenanceOf(path),
        error: err instanceof Error ? err.message : String(err),
      };
    }
  });
}
