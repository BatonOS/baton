// SPDX-License-Identifier: Apache-2.0




















import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

import { defaultDataDir } from '../api/client.js';
import { ExitCode, usageError, preconditionError } from '../errors.js';
import { flagString, type ParsedArgs } from '../args.js';
import { json, table } from '../output.js';

const SLUG = /^[a-z0-9][a-z0-9-]{0,63}$/;
const CONTENT_TYPES = new Set(['text/markdown', 'text/html']);
const EXT: Record<string, string> = { 'text/markdown': 'md', 'text/html': 'html' };
const TYPE_OF_EXT: Record<string, string> = { md: 'text/markdown', html: 'text/html' };

interface Doc {
  slug: string;
  title: string;
  tags: string[];
  scope: string;
  content_type: string;
  updated_at: string;
  author: string;
  size_bytes: number;
  body: string;


  source_network: string;
  source_resource_id: string;
  fetched_at: string;
  hash: string;
}

function kbDir(dataDir: string): string {
  return join(dataDir, 'kb');
}







function readDoc(dataDir: string, slug: string): Doc | null {
  const dir = kbDir(dataDir);
  for (const ext of ['md', 'html']) {
    const path = join(dir, `${slug}.${ext}`);
    if (!existsSync(path)) continue;
    const raw = readFileSync(path, 'utf8');
    const size = statSync(path).size;
    let title = slug, scope = 'local', updated = '', author = '';
    let sourceNetwork = '', sourceResourceId = '', fetchedAt = '', hash = '';
    let tags: string[] = [];
    let body = raw;
    if (raw.startsWith('---\n')) {
      const end = raw.indexOf('\n---\n', 4);
      if (end !== -1) {
        const header = raw.slice(4, end);
        body = raw.slice(end + 5);
        for (const line of header.split('\n')) {
          const i = line.indexOf(':');
          if (i <= 0) continue;
          const k = line.slice(0, i).trim();
          const v = line.slice(i + 1).trim();
          if (k === 'title') title = v;
          else if (k === 'scope') scope = v;
          else if (k === 'updated_at') updated = v;
          else if (k === 'author') author = v;
          else if (k === 'tags') tags = v ? v.split(',').map((t) => t.trim()).filter(Boolean) : [];
          else if (k === 'source_network') sourceNetwork = v;
          else if (k === 'source_resource_id') sourceResourceId = v;
          else if (k === 'fetched_at') fetchedAt = v;
          else if (k === 'hash') hash = v;
        }
      }
    }
    return { slug, title, tags, scope, content_type: TYPE_OF_EXT[ext]!, updated_at: updated, author, size_bytes: size, body,
      source_network: sourceNetwork, source_resource_id: sourceResourceId, fetched_at: fetchedAt, hash };
  }
  return null;
}

function allDocs(dataDir: string): Doc[] {
  const dir = kbDir(dataDir);
  if (!existsSync(dir)) return [];
  const slugs = new Set<string>();
  for (const f of readdirSync(dir)) {
    const m = f.match(/^(.+)\.(md|html)$/);
    if (m) slugs.add(m[1]!);
  }
  return [...slugs].map((s) => readDoc(dataDir, s)).filter((d): d is Doc => d !== null);
}




export async function kb(args: ParsedArgs): Promise<number> {
  const sub = args.positionals[1] ?? 'list';
  const dataDir = args.global.dataDir ?? defaultDataDir();
  if (sub === 'list') return list(args, dataDir);
  if (sub === 'show') return show(args, dataDir);
  if (sub === 'set') return set(args, dataDir);
  if (sub === 'rm') return remove(args, dataDir);
  throw usageError(`kb ${sub} is not a subcommand`, 'Available: baton kb list | show <slug> | set <slug> | rm <slug>. To share to a network: baton resource publish --type knowledge.');
}


async function list(args: ParsedArgs, dataDir: string): Promise<number> {
  const scope = flagString(args, 'scope');
  const tag = flagString(args, 'tag');
  const q = flagString(args, 'q')?.toLowerCase();
  let items = allDocs(dataDir);
  if (scope) items = items.filter((d) => d.scope === scope);
  if (tag) items = items.filter((d) => d.tags.includes(tag));
  if (q) {



    items = items
      .map((d) => ({ d, score: score(d, q) }))
      .filter((x) => x.score > 0)
      .sort((a, b) => b.score - a.score)
      .map((x) => x.d);
  } else {
    items.sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''));
  }
  const rows = items.map((d) => ({ slug: d.slug, title: d.title, tags: d.tags, scope: d.scope, content_type: d.content_type, updated_at: d.updated_at, author: d.author, size_bytes: d.size_bytes,


    source_network: d.source_network || null, source_resource_id: d.source_resource_id || null, fetched_at: d.fetched_at || null, hash: d.hash || null }));
  if (args.global.output === 'json') {
    process.stdout.write(json({ items: rows }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    table(rows, [
      { header: 'slug', get: (r) => r.slug },
      { header: 'title', get: (r) => r.title },
      { header: 'tags', get: (r) => r.tags.join(',') || '-' },
      { header: 'scope', get: (r) => r.scope },
      { header: 'updated', get: (r) => r.updated_at || '-' },
    ], `No documents in ${kbDir(dataDir)}. Add one: baton kb set <slug> --title "…" < doc.md`) + '\n',
  );
  return ExitCode.OK;
}

function score(d: Doc, q: string): number {
  let s = 0;
  if (d.title.toLowerCase().includes(q)) s += 10;
  if (d.tags.some((t) => t.toLowerCase().includes(q))) s += 5;
  if (d.body.toLowerCase().includes(q)) s += 1;
  return s;
}


async function show(args: ParsedArgs, dataDir: string): Promise<number> {
  const slug = args.positionals[2];
  if (!slug) throw usageError('kb show needs a slug', 'For example: baton kb show onboarding');
  const d = readDoc(dataDir, slug);
  if (!d) throw preconditionError(`no document called ${slug}`, 'See them with `baton kb list`.');
  if (args.global.output === 'json') {
    process.stdout.write(json({ ...d, source_network: d.source_network || null, source_resource_id: d.source_resource_id || null, fetched_at: d.fetched_at || null, hash: d.hash || null }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(d.body.endsWith('\n') ? d.body : d.body + '\n');
  return ExitCode.OK;
}







async function set(args: ParsedArgs, dataDir: string): Promise<number> {
  const slug = args.positionals[2];
  if (!slug) throw usageError('kb set needs a slug', 'For example: baton kb set onboarding --title "Onboarding" < doc.md');
  if (!SLUG.test(slug)) throw usageError(`${slug} is not a valid slug`, 'Lowercase letters, digits and dashes, starting with a letter or digit, at most 64 characters.');
  const contentType = flagString(args, 'content-type') ?? 'text/markdown';
  if (!CONTENT_TYPES.has(contentType)) throw usageError(`content type ${contentType} is not supported`, 'A document is text/markdown or text/html, and only those.');
  const scope = flagString(args, 'scope') ?? 'local';
  if (scope !== 'local' && !/^network:[a-z0-9][a-z0-9-]{0,62}$/.test(scope)) {
    throw usageError(`scope ${scope} is not valid`, 'A document is scope: local, or scope: network:<name>. One scope; it is not copied.');
  }
  const body = readFileSync(0, 'utf8');
  if (!body.trim()) throw usageError('kb set reads the body from stdin', 'Pipe the document in: baton kb set onboarding --title "…" < doc.md');
  const existing = readDoc(dataDir, slug);
  const tags = collectTags(args, existing);
  const title = flagString(args, 'title') ?? existing?.title ?? slug;
  const author = flagString(args, 'author') ?? existing?.author ?? 'operator';
  const updated = new Date().toISOString();
  const dir = kbDir(dataDir);
  mkdirSync(dir, { recursive: true });


  for (const ext of ['md', 'html']) {
    const p = join(dir, `${slug}.${ext}`);
    if (ext !== EXT[contentType] && existsSync(p)) rmSync(p, { force: true });
  }
  const header = [`title: ${title}`, `tags: ${tags.join(',')}`, `scope: ${scope}`, `updated_at: ${updated}`, `author: ${author}`].join('\n');
  const path = join(dir, `${slug}.${EXT[contentType]}`);
  writeFileSync(path, `---\n${header}\n---\n${body}`, { mode: 0o644 });
  if (args.global.output === 'json') {
    process.stdout.write(json({ slug, path, saved: true, content_type: contentType, scope }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(`saved ${slug} (${path}).\n`);
  return ExitCode.OK;
}

function collectTags(args: ParsedArgs, existing: Doc | null): string[] {
  const vals = args.repeated.get('tag');
  if (vals === undefined) return existing?.tags ?? [];
  return [...new Set(vals.filter((t) => t.length > 0))];
}


async function remove(args: ParsedArgs, dataDir: string): Promise<number> {
  const slug = args.positionals[2];
  if (!slug) throw usageError('kb rm needs a slug', 'For example: baton kb rm onboarding');
  const dir = kbDir(dataDir);
  let removed = false;
  for (const ext of ['md', 'html']) {
    const p = join(dir, `${slug}.${ext}`);
    if (existsSync(p)) { rmSync(p, { force: true }); removed = true; }
  }
  if (!removed) throw preconditionError(`no document called ${slug}`, 'See them with `baton kb list`.');
  process.stdout.write(args.global.output === 'json' ? json({ slug, removed: true }) + '\n' : `removed ${slug}.\n`);
  return ExitCode.OK;
}
