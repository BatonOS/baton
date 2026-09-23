// SPDX-License-Identifier: Apache-2.0













import { readFileSync, writeFileSync, mkdirSync, existsSync, readdirSync, statSync } from 'node:fs';
import { join, basename, dirname } from 'node:path';
import { createHash } from 'node:crypto';
import { Readable } from 'node:stream';
import { request as httpsRequest } from 'node:https';

import { defaultDataDir, type Client } from '../api/client.js';
import { readAsMember } from '../runtime/member-read.js';
import { parseWorkspaceTemplate } from '../runtime/template.js';
import { resolveNetwork, fetchDescriptor, verifyDescriptor } from '../runtime/resolver.js';
import { cloudFetch, cloudURL } from './cloud.js';
import { BatonError, ExitCode, preconditionError, unsupportedError, usageError } from '../errors.js';
import { flagString, type ParsedArgs } from '../args.js';
import { json, table } from '../output.js';










const TYPES = new Set(['skill', 'knowledge', 'policy', 'workflow', 'template', 'other', 'plugin']);
const VISIBILITIES = new Set(['private', 'network', 'public']);




















const PUBLIC_TYPES = new Set(['template', 'skill', 'plugin']);

export async function resource(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const sub = args.positionals[1] ?? 'list';
  switch (sub) {
    case 'list':
      return list(args, newClient);
    case 'get':
      return get(args, newClient);
    case 'publish':
      return publish(args, newClient);
    case 'unpublish':
      return unpublish(args, newClient);
    case 'move':
      return move(args, newClient);
    case 'tag':
      return tag(args, newClient);
    case 'discover':
      return discover(args);
    case 'fetch':
      return fetchResource(args, newClient);
    case 'copy':
      return copyResource(args, newClient);
    case 'verify':
      return verifyResources(args, newClient);
    case 'admit':
      return decide(args, newClient, 'admit');
    case 'deny':
      return decide(args, newClient, 'deny');
    case 'install':






      throw unsupportedError(
        `resource ${sub} is not built yet`,
        'install arrives with dependency resolution, when a template expands into the resources it references; discover/fetch/verify already exist.',
      );
    default:
      throw usageError(`resource ${sub} is not a subcommand`, 'Available: baton resource list | get | publish | copy | unpublish | move | tag | admit | deny | discover | fetch | verify.');
  }
}















function originFromFile(path: string, type: string): Record<string, unknown> | null {
  const fields = type === 'template' ? sidecarFields(path) : frontMatterFields(path);
  if (!fields) return null;
  const network = fields.source_network ?? '';
  const resourceId = fields.source_resource_id ?? '';
  const fetchedAt = fields.fetched_at ?? '';
  if (!network || !resourceId || !fetchedAt) return null;






  const networkId = fields.source_network_id || '';
  return {
    via: networkId ? 'member' : 'public',
    network,
    network_id: networkId || null,
    resource_id: resourceId,
    hash: fields.hash || null,
    fetched_at: fetchedAt,
  };
}

















function missingOriginFields(fields: Record<string, string>): string[] {
  const need = ['source_network', 'source_resource_id', 'fetched_at'];
  const held = (k: string) => (fields[k] ?? '').trim() !== '';
  return need.some(held) ? need.filter((k) => !held(k)) : [];
}













function bindingFor(args: ParsedArgs): { mode: 'pinned' | 'follow'; credential: string } {
  const raw = flagString(args, 'binding') ?? 'pinned';
  if (raw !== 'pinned' && raw !== 'follow') {
    throw usageError(
      `--binding ${raw} is not a binding this build knows`,
      'Use --binding pinned (bytes frozen here) or --binding follow (re-read from upstream).',
    );
  }
  const credential = flagString(args, 'credential') ?? '';










  if (raw === 'follow' && !credential) {
    throw usageError(
      'a follow binding must name the credential it will fetch with',
      'Add --credential <name>. Following re-reads upstream later, so it needs something to read with; ' +
      'pinned needs no runtime credential at all, which is its second benefit after reproducibility.',
    );
  }




  if (raw === 'pinned' && credential) {
    throw usageError(
      '--credential does not apply to a pinned binding',
      'A pinned copy never fetches again, so it holds no credential. Did you mean --binding follow?',
    );
  }
  return { mode: raw, credential };
}



























type VerifyRow = {
  name: string; type: string; binding: string;
  result: 'same' | 'drifted' | 'unreachable' | 'unverifiable';
  detail: string;











  consequence: string;
};

async function verifyResources(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const only = args.positionals[2] ?? '';
  const rows: VerifyRow[] = [];

  for (const [type, dir] of [['template', join(dataDir, 'templates')], ['knowledge', join(dataDir, 'kb')]] as const) {
    let entries: string[];
    try { entries = readdirSync(dir); } catch { continue; }
    for (const file of entries) {
      if (file.endsWith('.source.json')) continue;
      const path = join(dir, file);
      const fields = type === 'template' ? sidecarFields(path) : frontMatterFields(path);



      if (!fields) continue;
      const origin = originFromFile(path, type);
      const name = file.replace(/\.(yaml|md|html)$/, '');
      if (only && name !== only) continue;
      if (!origin) {



        const missing = missingOriginFields(fields);
        if (missing.length === 0) continue;
        rows.push({
          name, type, binding: fields.binding || 'unstated', result: 'unverifiable',
          detail: `adopted, but ${missing.join(' and ')} ${missing.length > 1 ? 'are' : 'is'} missing from its provenance`,
          consequence: 'adopt it again with `resource fetch <id> --save` to restore what it came from',
        });
        continue;
      }




      const binding = fields.binding || 'unstated';
      const recorded = String(origin.hash ?? '');
      if (!recorded) {
        rows.push({ name, type, binding, result: 'unverifiable', detail: 'no hash was recorded when this was adopted', consequence: 'adopt it again to give it an anchor' });
        continue;
      }

      try {
        const id = String(origin.resource_id);
        const q = `?type=${encodeURIComponent(type)}`;
        const label = String(origin.network ?? '');
        const doc = label === 'this network'
          ? await newClient().get<Record<string, unknown>>(`/networks/self/resources/${encodeURIComponent(id)}${q}`)
          : await anonymousGet<Record<string, unknown>>(await publicEndpoint(args, label, newClient()), `/public/resources/${encodeURIComponent(id)}${q}`);
        const upstream = typeof doc.hash === 'string' ? doc.hash : '';
        if (!upstream) {



          rows.push({ name, type, binding, result: 'unverifiable', detail: 'upstream returned no hash to compare against', consequence: 'two absences are not agreement' });
        } else if (binding === 'follow') {



















          rows.push({
            name, type, binding, result: 'same',
            detail: upstream.slice(0, 19),
            consequence: upstream === recorded
              ? 'reading upstream; the local cache matches'
              : `reading upstream — the local cache still holds ${recorded.slice(0, 19)}`,
          });
        } else if (upstream === recorded) {
          rows.push({ name, type, binding, result: 'same', detail: recorded.slice(0, 19), consequence: '' });
        } else {
          rows.push({
            name, type, binding, result: 'drifted',
            detail: `held ${recorded.slice(0, 19)} · upstream ${upstream.slice(0, 19)}`,
            consequence: binding === 'pinned'
              ? 'holding the adopted bytes on purpose; this is a notice, not an error'
              : 'this copy never declared a binding, so what should follow is not recorded',
          });
        }
      } catch (err) {



        rows.push({ name, type, binding, result: 'unreachable', detail: err instanceof Error ? err.message.split('\n')[0]! : 'upstream could not be reached', consequence: 'not same, not drifted — nothing was compared' });
      }
    }
  }

  if (args.global.output === 'json') {
    process.stdout.write(json({ items: rows }) + '\n');
  } else {
    process.stdout.write(table(rows, [
      { header: 'resource', get: (r) => r.name },
      { header: 'type', get: (r) => r.type },
      { header: 'binding', get: (r) => r.binding },
      { header: 'result', get: (r) => r.result },
      { header: 'detail', get: (r) => r.detail },
      { header: 'consequence', get: (r) => r.consequence },
    ], 'no adopted resources here yet — `baton resource fetch <id> --save` adopts one.') + '\n');
  }


























  if (rows.some((r) => r.result === 'unreachable')) return ExitCode.UNREACHABLE;
  if (rows.some((r) => r.result === 'unverifiable')) return ExitCode.PRECONDITION;
  return ExitCode.OK;
}


function sidecarFields(path: string): Record<string, string> | null {
  try {
    const raw = JSON.parse(readFileSync(path + '.source.json', 'utf8')) as Record<string, unknown>;
    const out: Record<string, string> = {};
    for (const [k, v] of Object.entries(raw)) out[k] = typeof v === 'string' ? v : '';
    return out;
  } catch {
    return null;
  }
}


function frontMatterFields(path: string): Record<string, string> | null {
  let text: string;
  try {
    text = readFileSync(path, 'utf8');
  } catch {
    return null;
  }
  if (!text.startsWith('---\n')) return null;
  const end = text.indexOf('\n---', 4);
  if (end < 0) return null;
  const out: Record<string, string> = {};
  for (const line of text.slice(4, end).split('\n')) {
    const cut = line.indexOf(':');
    if (cut > 0) out[line.slice(0, cut).trim()] = line.slice(cut + 1).trim();
  }
  return out;
}


async function list(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const type = flagString(args, 'type');
  if (type && !TYPES.has(type)) throw usageError(`unknown --type ${type}`, `One of: ${[...TYPES].join(', ')}.`);
  const ref = flagString(args, 'network');
  const q = new URLSearchParams();
  if (type) q.set('type', type);
  let res: { items: Array<Record<string, unknown>> };
  if (ref) {

    res = await readAsMember(args, ref, `/agent/network-resources${q.toString() ? '?' + q : ''}`, newClient());
  } else {

    const scope = flagString(args, 'scope');
    if (scope && !VISIBILITIES.has(scope)) throw usageError(`unknown --scope ${scope}`, 'One of: private, network, public.');
    if (scope) q.set('visibility', scope);
    res = await newClient().get(`/networks/self/resources${q.toString() ? '?' + q : ''}`);
  }
  return render(args, res.items ?? []);
}


async function get(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const id = args.positionals[2];
  if (!id) throw usageError('resource get needs a resource id or name', 'For example: baton resource get onboarding --type knowledge');
  const type = flagString(args, 'type');
  const ref = flagString(args, 'network');
  const q = type ? `?type=${encodeURIComponent(type)}` : '';
  const path = `${encodeURIComponent(id)}${q}`;
  const doc = ref
    ? await readAsMember<Record<string, unknown>>(args, ref, `/agent/network-resources/${path}`, newClient())
    : await newClient().get<Record<string, unknown>>(`/networks/self/resources/${path}`);
  process.stdout.write(json(doc) + '\n');
  return ExitCode.OK;
}







async function publish(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const type = flagString(args, 'type');
  if (!type || !TYPES.has(type)) throw usageError('resource publish needs --type', `One of: ${[...TYPES].join(', ')}.`);
  const path = args.positionals[2];



























  const givenHash = flagString(args, 'hash') ?? '';
  if (givenHash) assertDigestShape(givenHash, '--hash');

  if (type === 'plugin') return publishFacility(args, newClient);

  const wantsBody = type !== 'skill' || process.stdin.isTTY !== true;
  const body = path ? readFileSync(path, 'utf8') : wantsBody ? readFileSync(0, 'utf8') : '';




  const dataDir = args.global.dataDir ?? defaultDataDir();
  if (!body.trim() && type !== 'skill') throw usageError('resource publish reads the body from stdin (or a path)', 'Pipe it in: baton resource publish --type knowledge --name onboarding < doc.md');






  const nameFlag = flagString(args, 'name');
  let name: string;
  if (type === 'template') {
    const tmpl = parseWorkspaceTemplate(body, path ?? '<stdin>', dataDir);
    name = tmpl.name;
    if (nameFlag && nameFlag !== name) {
      throw usageError(
        `a template declares its own name: this one is "${name}", not "${nameFlag}"`,
        'Drop --name (the template names itself), or make them agree — one name, one source.',
      );
    }
  } else {
    if (!nameFlag) throw usageError('resource publish needs --name', 'For example: baton resource publish --type knowledge --name onboarding < doc.md');
    name = nameFlag;
  }
  const scope = flagString(args, 'scope');
  if (scope && !VISIBILITIES.has(scope)) throw usageError(`unknown --scope ${scope}`, 'One of: private, network, public.');
  if (scope === 'public' && !PUBLIC_TYPES.has(type)) {
    throw usageError(`--scope public is templates and skills only, not ${type}`, 'Public sharing is limited to template and skill (operator 2026-08-20). Use --scope network or --scope private for other types.');
  }
  const detail: Record<string, unknown> = {};
  const contentType = flagString(args, 'content-type');
  if (type === 'knowledge') {



    detail.content_type = contentType ?? 'text/markdown';
    detail.size_bytes = Buffer.byteLength(body);
  }



  const summary = flagString(args, 'summary');
  if (summary) detail.summary = summary;




  let hash = givenHash;

















  if (!hash && body.trim()) hash = 'sha256:' + createHash('sha256').update(body).digest('hex');



  const visibility = scope ?? 'network';
  const payload = {
    type, name,
    version: flagString(args, 'version') ?? '',
    folder: flagString(args, 'folder') ?? '',
    tags: args.repeated.get('tag') ?? [],
    visibility,
    source: flagString(args, 'source') ?? '',







    origin: path ? originFromFile(path, type) : null,
    hash,
    license: flagString(args, 'license') ?? '',
    body,
    detail,
  };
  const res = await newClient().post<Record<string, unknown>>('/networks/self/resources', payload);








  const directory: DirectoryOutcome =
    visibility === 'public' ? await registerToDirectory(newClient(), res) : DIRECTORY_NOT_APPLICABLE;
  if (args.global.output === 'json') {
    process.stdout.write(json({ ...res, directory }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(`published ${type}:${name} → ${String(res.visibility)} (${String(res.resource_id)}).${directory.note}\n`);
  return ExitCode.OK;
}








async function registerToDirectory(client: Client, res: Record<string, unknown>): Promise<DirectoryOutcome> {
  try {
    const att = await client.post<{ network_id: string; public_key: string; signature: string; payload: string }>(
      '/networks/self/attestations',
      {
        purpose: 'resource-register',
        resource_id: String(res.resource_id ?? ''),
        resource_type: String(res.type ?? ''),
        resource_name: String(res.name ?? ''),
        resource_version: String(res.version ?? ''),
        resource_hash: String(res.hash ?? ''),
      },
    );
    const detail = (res.detail ?? {}) as Record<string, unknown>;
    const meta = {
      folder: res.folder ?? null,
      source: res.source ?? null,
      compatibility: res.compatibility ?? null,









      origin: res.origin ?? null,
      license: res.license ?? null,
      summary: detail.summary ?? null,
    };
    const reg = await cloudFetch('/api/resources/register', {
      method: 'POST',
      body: JSON.stringify({ network_id: att.network_id, public_key: att.public_key, signed_record: att.signature, payload: att.payload, meta }),
    });
    if (reg.status === 201 || reg.status === 200) return { attempted: true, state: 'listed', status: reg.status, note: ' listed in the public directory.' };
    if (reg.status >= 400 && reg.status < 500) {






      return { attempted: true, state: 'refused', status: reg.status, note: ` stored public and fetchable, but the directory REFUSED to list it (${reg.status}) — this is not a connection problem: the network is likely not registered with the directory. Re-publishing alone will not list it.` };
    }
    return { attempted: true, state: 'unavailable', status: reg.status, note: ` stored public and fetchable, but the directory is unavailable (${reg.status}) — not listed; this is transient.` };
  } catch (err) {
    return { attempted: true, state: 'unreachable', status: null, note: ` stored public and fetchable, but the directory could not be reached (${err instanceof Error ? err.message : String(err)}) — not listed.` };
  }
}



















type DirectoryOutcome = {
  attempted: boolean;

  state: 'listed' | 'refused' | 'unavailable' | 'unreachable' | 'not-applicable';

  status: number | null;

  note: string;
};

const DIRECTORY_NOT_APPLICABLE: DirectoryOutcome = {
  attempted: false, state: 'not-applicable', status: null, note: '',
};


async function unpublish(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const id = args.positionals[2];
  if (!id) throw usageError('resource unpublish needs a resource id', 'For example: baton resource unpublish <id>');




  let wasPublic = false;
  try {
    const res = await newClient().get<Record<string, unknown>>(`/networks/self/resources/${encodeURIComponent(id)}`);
    wasPublic = res.visibility === 'public';
  } catch {  }



  const directory: DirectoryOutcome =
    wasPublic ? await unregisterFromDirectory(newClient(), id) : DIRECTORY_NOT_APPLICABLE;
  await newClient().delete(`/networks/self/resources/${encodeURIComponent(id)}`);
  if (args.global.output === 'json') {
    process.stdout.write(json({ resource_id: id, removed: true, directory }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(`unpublished ${id}.${directory.note}\n`);
  return ExitCode.OK;
}


async function unregisterFromDirectory(client: Client, id: string): Promise<DirectoryOutcome> {
  try {
    const att = await client.post<{ network_id: string; public_key: string; signature: string; payload: string }>(
      '/networks/self/attestations',
      { purpose: 'resource-unregister', resource_id: id },
    );
    const reg = await cloudFetch(`/api/resources/${encodeURIComponent(att.network_id)}/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      body: JSON.stringify({ network_id: att.network_id, public_key: att.public_key, signed_record: att.signature, payload: att.payload }),
    });
    if (reg.status === 200 || reg.status === 204) return { attempted: true, state: 'listed', status: reg.status, note: ' withdrawn from the public directory.' };
    return { attempted: true, state: 'unavailable', status: reg.status, note: ` removed locally, but the directory answered ${reg.status} — it may still be listed (retry).` };
  } catch (err) {
    return { attempted: true, state: 'unreachable', status: null, note: ` removed locally, but the directory could not be reached (${err instanceof Error ? err.message : String(err)}) — it may still be listed.` };
  }
}


async function move(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const id = args.positionals[2];
  const folder = flagString(args, 'folder');
  if (!id || folder === undefined) throw usageError('resource move needs a resource id and --folder', 'For example: baton resource move <id> --folder Skills/db');
  const res = await newClient().post<Record<string, unknown>>(`/networks/self/resources/${encodeURIComponent(id)}/move`, { folder });
  process.stdout.write(args.global.output === 'json' ? json(res) + '\n' : `moved ${id} → ${folder}.\n`);
  return ExitCode.OK;
}









async function tag(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const id = args.positionals[2];
  if (!id) throw usageError('resource tag needs a resource id', 'For example: baton resource tag <id> --tag runbook --tag security');
  const tags = args.repeated.get('tag') ?? [];
  const res = await newClient().post<Record<string, unknown>>(`/networks/self/resources/${encodeURIComponent(id)}/tags`, { tags });
  if (args.global.output === 'json') { process.stdout.write(json(res) + '\n'); return ExitCode.OK; }
  process.stdout.write(`tagged ${String(res.name ?? id)} → ${tags.length ? tags.join(', ') : '(cleared)'}\n`);
  return ExitCode.OK;
}
















async function decide(args: ParsedArgs, newClient: () => Client, verb: 'admit' | 'deny'): Promise<number> {
  const id = args.positionals[2];
  if (!id) {
    throw usageError(`resource ${verb} needs a resource id`,
      `Ids come from \`baton resource list\`. For example: baton resource ${verb} <id>` +
        (verb === 'deny' ? ' --reason "secrets in the body"' : ''));
  }
  const reason = flagString(args, 'reason');
  if (verb === 'deny' && !reason?.trim()) {
    throw usageError('resource deny needs --reason',
      'The publisher reads it to decide what to change. A refusal nobody can act on gets filtered as noise.');
  }
  const res = await newClient().post<Record<string, unknown>>(
    `/networks/self/resources/${encodeURIComponent(id)}/${verb}`,
    reason === undefined ? {} : { reason },
  );
  if (args.global.output === 'json') { process.stdout.write(json(res) + '\n'); return ExitCode.OK; }



  const name = String(res.name ?? id);
  process.stdout.write(verb === 'admit'
    ? `admitted ${name} — members can read it now.\n`
    : `denied ${name} — it stays hidden, and the publisher can read why.\n`);
  return ExitCode.OK;
}









async function discover(args: ParsedArgs): Promise<number> {
  const query = args.positionals[2];
  if (!query) throw usageError('resource discover needs a query', 'For example: baton resource discover "rust postgres migration" --type skill');
  const type = flagString(args, 'type');
  if (type && !TYPES.has(type)) throw usageError(`unknown --type ${type}`, `One of: ${[...TYPES].join(', ')}.`);
  const q = new URLSearchParams();
  q.set('q', query);
  if (type) q.set('type', type);
  const limit = flagString(args, 'limit');
  if (limit) q.set('limit', limit);



  if (args.flags.get('with-avatars') === true) q.set('with_avatars', '1');


  const res = await cloudFetch(`/api/discovery/resources?${q}`);
  if (res.status !== 200) {
    throw new BatonError({
      code: String(res.body?.code ?? `HTTP_${res.status}`),
      message: 'could not search the directory',
      remediation: `The directory answered ${res.status}. Retry, or check ${cloudURL()}.`,
      exitCode: ExitCode.UNREACHABLE,
    });
  }
  const items: Array<Record<string, unknown>> = res.body?.items ?? [];





  const total = typeof res.body?.total === 'number' ? res.body.total : items.length;
  const nextCursor = typeof res.body?.next_cursor === 'string' ? res.body.next_cursor : '';
  if (args.global.output === 'json') {



    process.stdout.write(json({ kind: 'ResourceDiscoveryList', total, next_cursor: nextCursor, items }) + '\n');
    return ExitCode.OK;
  }
  const pub = (r: Record<string, unknown>): string => {
    const pn = r.publisher_network as { address?: string; name?: string } | undefined;
    return pn?.address ?? pn?.name ?? String(r.publisher ?? '—');
  };
  process.stdout.write(
    table(items, [
      { header: 'TYPE', get: (r) => String(r.type ?? '') },
      { header: 'NAME', get: (r) => String(r.name ?? '') },
      { header: 'VERSION', get: (r) => String(r.version ?? '—') },
      { header: 'PUBLISHER', get: pub },
      { header: 'COMPATIBILITY', get: (r) => (r.compatibility == null ? '—' : JSON.stringify(r.compatibility)) },
    ], `The directory has no resource matching "${query}".`) +
      (items.length < total ? `\n  showing ${items.length} of ${total} — narrow with --type or raise --limit\n` : '\n'),
  );
  return ExitCode.OK;
}



async function publicEndpoint(args: ParsedArgs, ref: string, ownClient: Client): Promise<string> {
  if (/^https?:\/\//.test(ref)) return ref.replace(/\/+$/, '');
  try {
    const self = await ownClient.get<{ display_name?: string; network_id?: string }>('/networks/self');
    if (ref === self.display_name || ref === self.network_id || ref === args.global.master) return args.global.master.replace(/\/+$/, '');
  } catch {  }
  const resolved = await resolveNetwork(ref);
  const refusals: string[] = [];
  for (const ep of resolved.candidates) {
    try {
      const d = await fetchDescriptor(ep);
      if (verifyDescriptor(d, resolved.pin).ok) return ep.replace(/\/+$/, '');
      refusals.push(`${ep}: descriptor did not verify`);
    } catch (err) { refusals.push(`${ep}: ${err instanceof Error ? err.message : String(err)}`); }
  }
  throw new BatonError({ code: 'NETWORK_UNREACHABLE', message: `could not reach ${ref}`, remediation: refusals.join('\n  ') || 'No candidate endpoint answered.', exitCode: ExitCode.UNREACHABLE });
}











export function anonymousGet<T>(endpoint: string, path: string): Promise<T> {
  const url = new URL(endpoint.replace(/\/+$/, '') + '/api/v1alpha1' + path);
  return new Promise<T>((resolve, reject) => {
    const req = httpsRequest({ method: 'GET', hostname: url.hostname, port: url.port, path: url.pathname + url.search, rejectUnauthorized: false }, (res) => {
      let body = '';
      res.on('data', (c) => (body += c));
      res.on('end', () => {
        if ((res.statusCode ?? 0) >= 400) {
          let doc: { message?: string; code?: string } = {};
          try { doc = JSON.parse(body); } catch {  }
          reject(new BatonError({ code: doc.code ?? `HTTP_${res.statusCode}`, message: doc.message ?? `the publisher answered ${res.statusCode}`, remediation: 'The resource may not be public, or the id is wrong.', exitCode: ExitCode.PRECONDITION }));
          return;
        }
        try { resolve(JSON.parse(body) as T); } catch { reject(preconditionError('the publisher did not answer with JSON', 'It may not be a Baton master.')); }
      });
    });
    req.on('error', (err) => reject(new BatonError({ code: 'NETWORK_UNREACHABLE', message: `could not reach ${endpoint}: ${err.message}`, remediation: 'Check it is up and reachable.', exitCode: ExitCode.UNREACHABLE })));
    req.end();
  });
}
















async function fetchResource(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const id = args.positionals[2];
  if (!id) throw usageError('resource fetch needs a resource id', 'For example: baton resource fetch <id> (this network) or --from <publisher> (a public one).');
  const ref = flagString(args, 'from');
  const type = flagString(args, 'type');
  const binding = bindingFor(args);
  const q = type ? `?type=${encodeURIComponent(type)}` : '';




  let doc: Record<string, unknown>;
  let sourceLabel: string;
  if (ref) {
    const endpoint = await publicEndpoint(args, ref, newClient());
    doc = await anonymousGet<Record<string, unknown>>(endpoint, `/public/resources/${encodeURIComponent(id)}${q}`);
    sourceLabel = ref;
  } else {


    doc = await newClient().get<Record<string, unknown>>(`/networks/self/resources/${encodeURIComponent(id)}${q}`);
    sourceLabel = 'this network';
  }

  const detail = (doc.detail ?? {}) as Record<string, unknown>;


  const body = typeof detail.body === 'string' ? detail.body : typeof detail.yaml === 'string' ? detail.yaml : '';
  const hash = typeof doc.hash === 'string' ? doc.hash : '';
  let verified: string;
  if (hash && body) {
    const got = 'sha256:' + createHash('sha256').update(body).digest('hex');
    if (got !== hash) throw preconditionError(`hash mismatch: published ${hash}, got ${got}`, 'The source served bytes that do not match the recorded hash. Do not trust them.');
    verified = 'hash verified';
  } else {











    verified = hash ? 'published under a hash; nothing here has checked it' : 'no hash to verify';
  }






  let savedTo: string | null = null;
  if (args.flags.get('save') === true) {
    const dataDir = args.global.dataDir ?? defaultDataDir();
    const name = String(doc.name ?? id);
    const now = new Date().toISOString();
    const force = args.flags.get('force') === true;



    const guard = (path: string, what: string) => {
      if (existsSync(path) && !force) {
        throw preconditionError(`${what} ${name} already exists locally (${path})`, `Adopting the shared copy would overwrite your local one. Re-run with --force to replace it, or rename one of them.`);
      }
    };





    if (doc.type === 'knowledge') {
      const ct = String(detail.content_type ?? 'text/markdown');
      const ext = ct === 'text/html' ? 'html' : 'md';
      const tags = Array.isArray(doc.tags) ? (doc.tags as string[]) : [];
      const header = [
        `title: ${name}`,
        `tags: ${tags.join(',')}`,
        `scope: local`,
        `updated_at: ${now}`,
        `author: ${String(doc.publisher ?? 'unknown')}`,
        `source_network: ${sourceLabel}`,





        `source_network_id: ${String(doc.network_id ?? '')}`,
        `source_resource_id: ${id}`,
        `fetched_at: ${now}`,
        `hash: ${hash}`,

        `binding: ${binding.mode}`,
        `follow_credential: ${binding.credential}`,
      ].join('\n');
      const dir = join(dataDir, 'kb'); mkdirSync(dir, { recursive: true });
      const dest = join(dir, `${name}.${ext}`);
      guard(dest, 'knowledge');
      writeFileSync(dest, `---\n${header}\n---\n${body}`, { mode: 0o644 });
      savedTo = dest;
    } else if (doc.type === 'template') {




      const dir = join(dataDir, 'templates'); mkdirSync(dir, { recursive: true });
      const yamlPath = join(dir, `${name}.yaml`);
      guard(yamlPath, 'template');
      const head = `# baton: fetched from ${sourceLabel} (resource ${id}) at ${now}${hash ? `, hash ${hash}` : ''}\n`;
      writeFileSync(yamlPath, head + body);
      writeFileSync(yamlPath + '.source.json', json({
        source_network: sourceLabel,
        source_network_id: doc.network_id ?? null,
        source_resource_id: id, fetched_at: now, hash,



        binding: binding.mode,





        follow_credential: binding.credential,
      }));
      savedTo = yamlPath;
    } else {
      throw usageError(`--save does not apply to a ${String(doc.type)}`, 'Only knowledge and template are files. A skill is installed on a node (Stage 3).');
    }
  }

  if (args.global.output === 'json') {



    process.stdout.write(json({ ...doc, saved_to: savedTo }) + '\n');
    return ExitCode.OK;
  }

  if (savedTo) {
    process.stdout.write(`saved ${basename(savedTo)} to ${dirname(savedTo)} — fetched from ${sourceLabel} (${verified}).\n`);
    return ExitCode.OK;
  }

  process.stdout.write(`${String(doc.type)}:${String(doc.name)} from ${sourceLabel} — ${verified}\n`);
  if (body) process.stdout.write((body.endsWith('\n') ? body : body + '\n'));
  return ExitCode.OK;
}

















async function copyResource(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const id = args.positionals[2];
  if (!id) throw usageError('resource copy needs a resource id', 'For example: baton resource copy <id> --network acme, or --from <publisher> for a public one.');
  const memberRef = flagString(args, 'network');
  const publicRef = flagString(args, 'from');
  if (memberRef && publicRef) {
    throw usageError('give --network or --from, not both', '--network reads as a member of that network; --from reads a public resource anonymously. They are different reads and record different things.');
  }
  if (!memberRef && !publicRef) {
    throw usageError('resource copy needs --network <ref> or --from <ref>', 'Which network is it being copied FROM? Copying within this network is not a copy — it already exists here.');
  }
  const type = flagString(args, 'type');
  const q = type ? `?type=${encodeURIComponent(type)}` : '';

  let doc: Record<string, unknown>;
  if (memberRef) {
    doc = await readAsMember<Record<string, unknown>>(args, memberRef, `/agent/network-resources/${encodeURIComponent(id)}${q}`, newClient());
  } else {
    const endpoint = await publicEndpoint(args, publicRef!, newClient());
    doc = await anonymousGet<Record<string, unknown>>(endpoint, `/public/resources/${encodeURIComponent(id)}${q}`);
  }


  const fetchedAt = new Date().toISOString();

  const detail = (doc.detail ?? {}) as Record<string, unknown>;
  const body = typeof detail.body === 'string' ? detail.body : typeof detail.yaml === 'string' ? detail.yaml : '';
  const hash = typeof doc.hash === 'string' ? doc.hash : '';




  if (hash && body) {
    const got = 'sha256:' + createHash('sha256').update(body).digest('hex');
    if (got !== hash) throw preconditionError(`hash mismatch: published ${hash}, got ${got}`, 'The source served bytes that do not match the recorded hash. Nothing was copied.');
  }

  const scope = flagString(args, 'scope') ?? 'network';
  if (!VISIBILITIES.has(scope)) throw usageError(`unknown --scope ${scope}`, 'One of: private, network, public.');
  const payload = {
    type: String(doc.type ?? ''),
    name: String(doc.name ?? ''),
    version: String(doc.version ?? ''),
    folder: flagString(args, 'folder') ?? '',
    tags: Array.isArray(doc.tags) ? doc.tags : [],
    visibility: scope,
    source: typeof doc.source === 'string' ? doc.source : '',
    hash,
    license: typeof doc.license === 'string' ? doc.license : '',
    body,
    detail,
    origin: {






      via: memberRef ? 'member' : 'public',
      network: memberRef ?? publicRef,
      network_id: memberRef ? (doc.network_id ?? null) : null,
      resource_id: id,
      hash: hash || null,
      fetched_at: fetchedAt,
    },
  };
  const res = await newClient().post<Record<string, unknown>>('/networks/self/resources', payload);
  if (args.global.output === 'json') { process.stdout.write(json(res) + '\n'); return ExitCode.OK; }
  process.stdout.write(`copied ${String(res.type)}:${String(res.name)} from ${memberRef ?? publicRef} → ${String(res.visibility)} (${String(res.resource_id)}).\n`);
  return ExitCode.OK;
}

function render(args: ParsedArgs, items: Array<Record<string, unknown>>): number {
  if (args.global.output === 'json') {
    process.stdout.write(json({ kind: 'ResourceList', items }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    table(items, [
      { header: 'TYPE', get: (r) => String(r.type ?? '') },
      { header: 'NAME', get: (r) => String(r.name ?? '') },
      { header: 'VERSION', get: (r) => String(r.version ?? '') },
      { header: 'FOLDER', get: (r) => String(r.folder ?? '') },
      { header: 'VISIBILITY', get: (r) => String(r.visibility ?? '') },
      { header: 'ID', get: (r) => String(r.resource_id ?? '') },
    ], 'No shared resources.'),
  );
  return ExitCode.OK;
}

















async function publishFacility(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const path = args.positionals[2];
  if (!path) {
    throw usageError('resource publish --type plugin needs an archive',
      'Pack the facility first: `deploy/plugins/pack.sh <plugin-dir>` prints the archive and the command to publish it.');
  }
  let st;
  try {
    st = statSync(path);
  } catch {
    throw usageError(`${path} does not exist`, 'Give the archive `deploy/plugins/pack.sh` produced.');
  }
  if (st.isDirectory()) {



    throw usageError(`${path} is a directory, and a facility is published as an archive`,
      `Pack it first: deploy/plugins/pack.sh ${path}`);
  }
  const name = flagString(args, 'name');
  if (!name) throw usageError('resource publish --type plugin needs --name', 'Use the facility id, for example: --name example.notepad');
  const scope = flagString(args, 'scope');
  if (scope && !VISIBILITIES.has(scope)) throw usageError(`unknown --scope ${scope}`, 'One of: private, network, public.');



  if (flagString(args, 'hash')) {
    throw usageError('a facility\'s hash is its archive\'s, and is computed here',
      'Drop --hash. Publishing a record whose hash disagreed with the bytes would point at an archive nobody packed.');
  }

  const bytes = readFileSync(path);
  const hash = 'sha256:' + createHash('sha256').update(bytes).digest('hex');
  const client = newClient();



  await client.postStream<{ size_bytes?: number }>(
    `/networks/self/resource-archives/${hash.slice('sha256:'.length)}`,
    Readable.from(bytes),
    bytes.length,
  );

  const detail: Record<string, unknown> = { plugin_id: name, size_bytes: bytes.length };
  const summary = flagString(args, 'summary');
  if (summary) detail.summary = summary;

  const res = await client.post<{ resource_id?: string; review?: string }>('/networks/self/resources', {
    type: 'plugin',
    name,
    version: flagString(args, 'version') ?? '',
    folder: flagString(args, 'folder') ?? '',
    visibility: scope ?? 'network',
    hash,


    detail,
  });

  return render(args, [{
    kind: 'ResourcePublished',
    resource_id: res.resource_id ?? null,
    type: 'plugin',
    name,
    hash,
    size_bytes: bytes.length,
    review: res.review ?? null,
    note: 'the archive is stored and the record names it; a node adopts it with `baton-plugin adopt`',
  }]);
}














function assertDigestShape(value: string, where: string): void {
  if (!/^sha256:[0-9a-f]{64}$/.test(value)) {
    throw usageError(
      `${where} is not a content digest: ${value.slice(0, 80)}`,
      'It is `sha256:` followed by 64 lowercase hex characters — the prefix is part of the value, because a digest has to say which algorithm made it. ' +
        'Leave the flag out and it is computed from the content.',
    );
  }
}
