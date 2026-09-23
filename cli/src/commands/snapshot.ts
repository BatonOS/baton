// SPDX-License-Identifier: Apache-2.0



























import { spawnSync } from 'node:child_process';
import { copyIn, copyOut, execSucceeds, hostSecurityOptions, runState } from '../runtime/engine.js';
import { BATON_DIR, POSITIONS, TRAVELS, ZONES, nonTravelling, pruneNonTravelling } from '../runtime/workspace.js';
import { existsSync, mkdirSync, mkdtempSync, rmSync, statSync, chmodSync, chownSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { parse as parseYAML } from 'yaml';
import { join, resolve, basename, dirname } from 'node:path';

import { BatonError, ExitCode, preconditionError, unsupportedError, usageError } from '../errors.js';
import { json } from '../output.js';
import { flagBool, flagString, type ParsedArgs } from '../args.js';
import type { Client } from '../api/client.js';
import { defaultDataDir } from '../api/client.js';
import { nodeCreate } from './create.js';
import { joinNetwork } from './agent.js';
import { resolveTarget } from '../runtime/target.js';
import { composeDir } from '../runtime/manifests.js';
import { volumePaths } from '../runtime/volume.js';
import { METADATA_MEMBER, consistencyFrom, consistencyOf, describeConsistency, metadataDocument, type Consistency } from '../runtime/consistency.js';


const RUNTIME_HOME = '/var/lib/baton/home';


















const MEMBER_WORKSPACE = 'workspace';














function notCarried(hasVolume = false): string {
  const rows: [string, string][] = [
    ['pki', 'outside the workspace — this command never reaches it'],
    ...POSITIONS.filter((p) => p !== TRAVELS).map(
      (p): [string, string] => [`${BATON_DIR}/${p}/`, `never carried — ${ZONES[p].join(', ')}`],
    ),










    ...(hasVolume
      ? ([['data volume', 'not carried — the node\'s own storage, restored empty and refilled by its runtime']] as [string, string][])
      : []),
  ];
  const width = Math.max(...rows.map(([label]) => label.length));
  return rows.map(([label, why]) => `  ${label.padEnd(width)}  ${why}\n`).join('');
}
const CLASS_MEMBERS: Record<PersistClass, string> = { state: 'state', cache: 'cache', secret: 'secrets' };
const KNOWN_MEMBERS = new Set([MEMBER_WORKSPACE, METADATA_MEMBER, ...Object.values(CLASS_MEMBERS)]);

type PersistClass = 'cache' | 'state' | 'secret';
export interface Persisted { path: string; class: PersistClass }



















export function persistedOf(dataDir: string | undefined, node: string): Persisted[] | undefined {
  const file = join(composeDir(dataDir), `agent-${node}.runtime.yaml`);

  if (!existsSync(file)) return [];
  let doc: { adapter?: { persistence?: unknown } };
  try {
    doc = parseYAML(readFileSync(file, 'utf8')) as typeof doc;
  } catch {
    return undefined;
  }
  if (doc !== null && doc !== undefined && (typeof doc !== 'object' || Array.isArray(doc))) return undefined;
  const raw = doc?.adapter?.persistence;
  if (raw === undefined || raw === null) return [];
  if (!Array.isArray(raw)) return undefined;
  const out: Persisted[] = [];
  for (const item of raw) {
    if (!item || typeof item !== 'object') continue;
    const { path, class: cls } = item as { path?: unknown; class?: unknown };
    if (typeof path !== 'string' || !path) continue;
    const c: PersistClass = cls === 'cache' || cls === 'secret' ? cls : 'state';
    out.push({ path: path.startsWith('/') ? path : join(RUNTIME_HOME, path), class: c });
  }
  return out;
}


function memberPathFor(p: Persisted): string {
  return join(CLASS_MEMBERS[p.class], p.path.replace(/^\/+/, ''));
}






















export function foreignPaths(staging: string, declared: Persisted[]): string[] {
  const declaredMembers = declared.map(memberPathFor);
  const out: string[] = [];
  for (const member of Object.values(CLASS_MEMBERS)) {
    const root = join(staging, member);
    if (!existsSync(root)) continue;
    out.push(...undeclaredIn(root, member, declaredMembers));
  }
  return out;
}

function undeclaredIn(root: string, member: string, declared: string[]): string[] {
  const out: string[] = [];
  const walk = (dir: string, rel: string): void => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const r = rel ? `${rel}/${e.name}` : e.name;
      const full = `${member}/${r}`;
      if (declared.some((d) => full === d || full.startsWith(`${d}/`))) continue;
      if (e.isDirectory() && declared.some((d) => d.startsWith(`${full}/`))) {
        walk(join(dir, e.name), r);
        continue;
      }
      out.push(full);
    }
  };
  walk(root, '');
  return out;
}



const SPEC_MOUNT = '/etc/baton/runtime.yaml';

function run(argv: string[], opts: { cwd?: string } = {}) {
  return spawnSync(argv[0]!, argv.slice(1), { encoding: 'utf8', cwd: opts.cwd });
}









export interface NodeShape {
  specPath?: string;
  network?: string;
  workspace?: string;

  secrets: { source: string; target: string }[];
}

export function readNodeShape(dataDir: string | undefined, container: string): NodeShape {
  const shape: NodeShape = { secrets: [] };
  const file = join(composeDir(dataDir), `${container.replace(/^baton-/, '')}.yml`);
  let body: string;
  try {
    body = readFileSync(file, 'utf8');
  } catch {
    return shape;
  }















  shape.workspace = workspacePath(dataDir, container)?.path;
  for (const m of body.matchAll(/^\s+- ([^\s:]+):(\/[^\s:]+)(:ro)?$/gm)) {
    const [, source, target] = m;
    if (target === SPEC_MOUNT) shape.specPath = source;
    else if (target!.startsWith('/run/secrets')) shape.secrets.push({ source: source!, target: target! });
  }
  const net = /^\s+name: (\S+)$/m.exec(body.slice(body.indexOf('networks:')));
  if (net) shape.network = net[1];
  return shape;
}


export type WorkspaceSource = 'manifest' | 'spec';
























































export function workspacePath(
  dataDir: string | undefined,
  container: string,
): { path: string; source: WorkspaceSource } | undefined {
  const file = join(composeDir(dataDir), `${container.replace(/^baton-/, '')}.yml`);
  let body: string;
  try {
    body = readFileSync(file, 'utf8');
  } catch {
    return undefined;
  }

  const declared = /^x-baton-workspace:\s*(\S+)\s*$/m.exec(body);
  if (declared) return { path: declared[1]!, source: 'manifest' };















  const spec = /- ([^\s:]+):\/etc\/baton\/runtime\.yaml(?::ro)?\s*$/m.exec(body)?.[1];
  if (!spec) return undefined;
  let declaredInSpec: unknown;
  try {
    const doc = parseYAML(readFileSync(spec, 'utf8')) as { adapter?: { workspace?: { mountPath?: unknown } } };
    declaredInSpec = doc?.adapter?.workspace?.mountPath;
  } catch {
    return undefined;
  }
  if (typeof declaredInSpec !== 'string' || declaredInSpec === '') return undefined;


  const mounted = new RegExp(
    `(?:- [^\\s:]+:${declaredInSpec.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?::ro)?\\s*$)|(?:^\\s+target: ${declaredInSpec.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*$)`,
    'm',
  ).test(body);
  return mounted ? { path: declaredInSpec, source: 'spec' } : undefined;
}





function remappedRootUID(): number | undefined {
  const info = hostSecurityOptions();
  if (!info.ok) return undefined;
  const options = info.stdout;
  if (options.includes('name=rootless')) {
    const host = process.env.DOCKER_HOST ?? '';
    const sock = host.startsWith('unix://') ? host.slice('unix://'.length) : '/var/run/docker.sock';
    try {
      return statSync(sock).uid;
    } catch {
      return undefined;
    }
  }
  if (!options.includes('name=userns')) return undefined;
  try {
    for (const line of readFileSync('/etc/subuid', 'utf8').split('\n')) {
      const [user, base] = line.split(':');
      if (user === 'dockremap' && base) return Number(base);
    }
  } catch {

  }
  return undefined;
}















export function secretNamesByMount(specPath: string): Map<string, string> {


















  const names = new Map<string, string>();
  let spec: { execution?: { secrets?: unknown } };
  try {
    spec = parseYAML(readFileSync(specPath, 'utf8')) as typeof spec;
  } catch {
    throw preconditionError(
      `${specPath} is not readable`,
      'It is the runtime spec the source node was created from; the clone needs the same one.',
    );
  }
  const declared = spec?.execution?.secrets;
  if (!Array.isArray(declared)) return names;
  for (const item of declared) {
    if (!item || typeof item !== 'object') continue;
    const { name, mountPath } = item as { name?: unknown; mountPath?: unknown };
    if (typeof name !== 'string' || !name) continue;


    names.set(typeof mountPath === 'string' && mountPath ? mountPath : `/run/secrets/${name}`, name);
  }
  return names;
}

export async function snapshot(args: ParsedArgs): Promise<number> {
  const node = args.positionals[1];
  if (!node) {
    throw usageError('snapshot needs a node', 'For example: baton snapshot agent01');
  }
  const target = resolveTarget(node);








  const found = workspacePath(args.global.dataDir, target.container);
  if (!found) {
    throw preconditionError(
      `${target.container}: this node does not say where its workspace is`,
      'Neither `x-baton-workspace:` in its compose manifest nor `adapter.workspace.mountPath` in its runtime spec ' +
        'could be read. A node created without a runtime (a skeleton: its template listed no plugins) has no workspace to capture; ' +
        'otherwise `baton rebuild` regenerates the manifest. Refusing here rather than assuming /workspace, ' +
        'because assuming it is how the plugin tree got archived under the workspace\'s name.',
    );
  }
  const ws = found.path;
  const take = (guestPath: string, dest: string) => copyOut(target.container, guestPath, dest);

  const dir = join(composeDir(args.global.dataDir), '..', 'snapshots');
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').replace('Z', 'Z');
  const out = resolve(flagString(args, 'out') ?? join(dir, `${node}-${stamp}.tar.gz`));











  const consistency: Consistency = consistencyOf(runState(target.container));

  const persisted = persistedOf(args.global.dataDir, node);
  if (persisted === undefined) {




    throw preconditionError(
      `${node} declares what it keeps in a file this build cannot read`,
      `Read ${join(composeDir(args.global.dataDir), `agent-${node}.runtime.yaml`)} — until it parses, ` +
        'a snapshot would carry the workspace only, and would not say so.',
    );
  }




  const hasSizedVolume = existsSync(volumePaths(args.global.dataDir ?? defaultDataDir(), node).img);

  const captured = [ws, ...persisted.map((p) => `${p.path} (${p.class})`)];

  if (args.global.dryRun) {
    process.stdout.write(
      `would capture from ${node}:\n` + captured.map((c) => `  ${c}\n`).join('') +
        `would write ${out}\n` +
        `would record consistency: ${describeConsistency(consistency)}\n` +
        'would capture no identity:\n' +
        notCarried(hasSizedVolume),
    );
    return ExitCode.OK;
  }

  mkdirSync(join(out, '..'), { recursive: true });
  const staging = mkdtempSync(join(tmpdir(), 'baton-snapshot-'));
  let pruned: string[] = [];

  try {




    mkdirSync(join(staging, MEMBER_WORKSPACE), { recursive: true });
    const wsCopy = take(ws, join(staging, MEMBER_WORKSPACE));




    pruned = pruneNonTravelling(join(staging, MEMBER_WORKSPACE));
    let any = wsCopy.ok;
    const members = new Set<string>([MEMBER_WORKSPACE]);
    for (const p of persisted) {
      const dest = join(staging, memberPathFor(p));
      mkdirSync(dest, { recursive: true });
      const c = take(p.path, dest);
      if (c.ok) any = true;
      members.add(CLASS_MEMBERS[p.class]);
    }
    if (!any) {
      throw preconditionError(
        `${node} has no portable state to capture`,
        `Neither ${ws} nor any declared persisted path exists on it. A node that supervises nothing and ` +
          'has never been worked in carries nothing a snapshot is for.',
      );
    }





    writeFileSync(join(staging, METADATA_MEMBER), JSON.stringify(metadataDocument(consistency, new Date().toISOString()), null, 2) + '\n');
    members.add(METADATA_MEMBER);

    const tar = run(['tar', '-czf', out, '-C', staging, ...[...members].sort()]);
    if (tar.status !== 0) {
      throw new BatonError({
        code: 'INTERNAL',
        message: `could not write ${out}`,
        remediation: (tar.stderr ?? '').trim() || 'Check the path is writable.',
        exitCode: ExitCode.INTERNAL,
      });
    }


    chmodSync(out, 0o600);
    const uid = remappedRootUID();
    if (uid !== undefined) {
      try {
        chownSync(out, uid, -1);
      } catch {

      }
    }
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }

  const bytes = statSync(out).size;
  process.stdout.write(
    args.global.output === 'json'
      ? json({ node, file: out, bytes, captured, members: [...new Set([MEMBER_WORKSPACE, ...persisted.map((p) => CLASS_MEMBERS[p.class])])] }) + '\n'
      : `snapshot ${out}\n  ${bytes} bytes — the workspace${persisted.length ? ` and ${persisted.length} persisted path(s) by class` : ''}\n` +
          (pruned.length ? `  left behind: ${pruned.join(', ')} — the position that does not travel.\n` : '') +
          `  consistency: ${describeConsistency(consistency)}` +
          `${consistency === 'unknown' ? ' (it was not running, and how it stopped is not recorded)' : ''}\n` +
          '  no identity: a node restored from this must enrol as a new node.\n' +




          (hasSizedVolume
            ? '  no data volume: the node\'s own storage is not in here; a restored node starts with an empty one.\n'
            : '') +
          (persisted.some((p) => p.class === 'secret')
            ? '  treat it as a credential: it holds what the runtime declared secret.\n'
            : ''),
  );
  return ExitCode.OK;
}







export async function restore(args: ParsedArgs): Promise<number> {
  const node = args.positionals[1];
  const from = flagString(args, 'from');
  if (!node || !from) {
    throw usageError(
      'restore needs a node and an archive',
      'For example: baton restore agent01 --from ./agent01-2026-08-10.tar.gz',
    );
  }
  const file = resolve(from);
  if (!existsSync(file)) {
    throw preconditionError(`${file} does not exist`, 'Point --from at a snapshot.');
  }

  const target = resolveTarget(node);








  const found = workspacePath(args.global.dataDir, target.container);
  if (!found) {
    throw preconditionError(
      `${target.container}: this node does not say where its workspace is`,
      'Neither `x-baton-workspace:` in its compose manifest nor `adapter.workspace.mountPath` in its runtime spec ' +
        'could be read. A node created without a runtime (a skeleton: its template listed no plugins) has no workspace to capture; ' +
        'otherwise `baton rebuild` regenerates the manifest. Refusing here rather than assuming /workspace, ' +
        'because assuming it is how the plugin tree got archived under the workspace\'s name.',
    );
  }
  const ws = found.path;
  const put = (src: string, guestPath: string) => copyIn(src, target.container, guestPath);

  const staging = mkdtempSync(join(tmpdir(), 'baton-restore-'));
  const restored: string[] = [];
  let carried: Consistency = 'unknown';
  try {
    const untar = run(['tar', '-xf', file, '-C', staging]);
    if (untar.status !== 0) {
      throw preconditionError(
        `${basename(file)} is not a readable snapshot`,
        (untar.stderr ?? '').trim() || 'It should be a tar archive written by `baton snapshot`.',
      );
    }





    for (const member of readdirSync(staging)) {
      if (!KNOWN_MEMBERS.has(member)) {
        throw preconditionError(
          `${basename(file)} contains ${member}, which no snapshot may carry`,
          member === 'home'
            ? 'This archive was written before persisted paths were classified as cache, state or secret and archived under those names. Take a new snapshot of the source; ' +
              'a lump named home cannot be restored by class.'
            : 'A snapshot holds workspace/, state/, cache/, secrets/. It never carries identity or mail: a restored node enrolls as a new identity.',
        );
      }
    }






    if (existsSync(join(staging, METADATA_MEMBER))) {
      try {
        carried = consistencyFrom(JSON.parse(readFileSync(join(staging, METADATA_MEMBER), 'utf8')));
      } catch {
        carried = 'unknown';
      }
    }
























    const declared = persistedOf(args.global.dataDir, node);
    if (declared === undefined) {



      throw preconditionError(
        `cannot read what ${node} keeps, so this archive cannot be checked against it`,
        `${join(composeDir(args.global.dataDir), `agent-${node}.runtime.yaml`)} did not parse. ` +
          'Fix or restore that file first — restoring blind would copy paths this node never declared.',
      );
    }
    const declaredMembers = declared.map(memberPathFor);
    const foreign = foreignPaths(staging, declared);
    if (foreign.length > 0) {
      throw preconditionError(
        `${basename(file)} carries paths ${node} does not keep: ${foreign.join(', ')}`,
        (declaredMembers.length > 0
          ? `${node} declares ${declaredMembers.join(', ')}. `
          : `${node} declares no persisted paths at all. `) +
          'This archive was taken from a node with a different runtime. Restore it onto a node created ' +
          'from the same template; to move an agent to another runtime, create it there and hand the ' +
          'work over through the company store — an archive of the previous runtime\'s home is not that.',
      );
    }





    if (args.global.dryRun) {
      process.stdout.write(
        `would replace everything in ${ws} that ${basename(file)} covers, and every persisted path it carries, on ${node}\n` +
          `would leave ${BATON_DIR}/${nonTravelling().join('/, ')}/ as it is — it is not in the archive, so there is no second copy of it\n` +
          `would leave ${node}'s identity untouched\n`,
      );
      return ExitCode.OK;
    }

    if (!flagBool(args, 'yes')) {
      throw preconditionError(
        `restoring replaces the workspace and persisted state of ${node}`,
        `Everything ${basename(file)} covers is overwritten and anything else in the workspace is deleted — ` +
          `except ${BATON_DIR}/${nonTravelling().join('/, ')}/, which no archive carries. Add --yes if that is what you want.`,
      );
    }













    if (existsSync(join(staging, MEMBER_WORKSPACE))) {
      const keep = nonTravelling();
      const notName = (names: string[]) => names.map((n) => `! -name ${n}`).join(' ');
      const clear =
        `find ${ws} -mindepth 1 -maxdepth 1 ${notName([BATON_DIR])} -exec rm -rf {} + ; ` +
        `find ${ws}/${BATON_DIR} -mindepth 1 -maxdepth 1 ${notName(keep)} -exec rm -rf {} + ; ` +
        `exit 0`;
      if (!execSucceeds(target.container, ['sh', '-c', clear])) {
        throw preconditionError(
          `${node} is not running, so restore cannot replace what is in its workspace`,
          'A restore that only copies over the top would leave files the archive does not contain — ' +
            `and the confirmation says they are gone. Start it first: baton start ${node}`,
        );
      }
    }

    if (existsSync(join(staging, MEMBER_WORKSPACE))) {
      const cp = put(join(staging, MEMBER_WORKSPACE), ws);
      if (!cp.ok) {
        throw new BatonError({ code: 'INTERNAL', message: `could not restore workspace into ${node}`, remediation: cp.stderr.trim(), exitCode: ExitCode.INTERNAL });
      }
      restored.push(ws);
    }





    for (const member of Object.values(CLASS_MEMBERS)) {
      const root = join(staging, member);
      if (!existsSync(root)) continue;
      const cp = put(root, '/');
      if (!cp.ok) {
        throw new BatonError({ code: 'INTERNAL', message: `could not restore ${member} into ${node}`, remediation: cp.stderr.trim(), exitCode: ExitCode.INTERNAL });
      }
      restored.push(...topPaths(root).map((r) => `/${r} (${member})`));
    }
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }

  process.stdout.write(
    args.global.output === 'json'
      ? json({ node, from: file, restored, consistency: carried }) + '\n'
      : `restored ${node} from ${basename(file)}\n` +
          `  replaced: ${restored.join(', ')}; identity untouched.\n` +
          `  what it carried: ${describeConsistency(carried)}` +
          `${carried === 'unknown' ? ' (this archive records none — taken before it was recorded, or by something else)' : ''}\n` +
          '  restart it so the runtime reads what it was given: baton restart ' + node + '\n',
  );
  return ExitCode.OK;
}


function topPaths(root: string): string[] {
  const out: string[] = [];
  const walk = (dir: string, rel: string): void => {
    const entries = readdirSync(dir, { withFileTypes: true });
    if (entries.length === 1 && entries[0]!.isDirectory()) {
      walk(join(dir, entries[0]!.name), rel ? `${rel}/${entries[0]!.name}` : entries[0]!.name);
      return;
    }
    if (rel) out.push(rel);
  };
  walk(root, '');
  return out;
}











export function cloneCreatePlan(
  args: ParsedArgs,
  src: string,
  dst: string,
): { flags: Map<string, string | boolean>; repeated: Map<string, string[]>; network: string | undefined } {
  const from = resolveTarget(src);
  const shape = readNodeShape(args.global.dataDir, from.container);
  if (!shape.specPath) {
    throw preconditionError(
      `${src} was not created here, so there is nothing to copy its shape from`,
      'Clone reads the runtime section, network and secrets off the source node’s manifest. ' +
        'Create the new agent with `baton agent create --template <file>` and use `baton snapshot` / `baton restore` instead.',
    );
  }
  const names = secretNamesByMount(shape.specPath);
  const secretArgs: string[] = [];
  for (const s of shape.secrets) {
    const name = names.get(s.target);
    if (name) secretArgs.push(`${name}=${s.source}`);
  }















  const dataDir = args.global.dataDir ?? defaultDataDir();
  const srcTemplate = join(dataDir, 'compose', `agent-${src}.template.yaml`);
  if (!existsSync(srcTemplate)) {
    throw preconditionError(
      `${src} has no kept Node Template at ${srcTemplate}`,
      'It was created before templates were kept, or from another data directory. ' +
        'Create the new agent with `baton agent create --template <file>` and use snapshot / restore instead.',
    );
  }
  const createFlags = new Map<string, string | boolean>([
    ['template', srcTemplate],
    ['owner', dst],
  ]);
  return { flags: createFlags, repeated: new Map([['secret', secretArgs]]), network: shape.network };
}


















export async function clone(args: ParsedArgs, client: Client): Promise<number> {
  const src = args.positionals[1];
  const dst = args.positionals[2];
  if (!src || !dst) {
    throw usageError('clone needs a source and a new name', 'For example: baton clone agent01 agent02');
  }


































  if (args.flags.has('template')) {
    throw unsupportedError(
      'clone does not take --template: a clone runs the source’s runtime, not a different one',
      'To move an agent onto a different runtime, `baton destroy <node>` and ' +
        '`baton agent create --name <node> --template <file>`. That is the one path, and ' +
        'clone deliberately is not a second one.',
    );
  }



  const plan = cloneCreatePlan(args, src, dst);

  if (args.global.dryRun) {
    process.stdout.write(
      `would snapshot ${src}\n` +
        `would create ${dst} from its own copy of the source's Node Template on ${plan.network ?? '(default network)'}\n` +
        `would join ${dst} to this network as a new identity, with its own certificate\n` +
        `would restore the snapshot into ${dst}\n`,
    );
    return ExitCode.OK;
  }

  const staging = mkdtempSync(join(tmpdir(), 'baton-clone-'));
  chmodSync(staging, 0o700);
  const archive = join(staging, 'state.tar.gz');
  const tokenFile = join(staging, 'enrollment.token');

  try {


    const snapArgs: ParsedArgs = {
      ...args,
      positionals: ['snapshot', src],
      flags: new Map([...args.flags, ['out', archive]]),
    };
    const captured = await snapshot({ ...snapArgs, global: { ...args.global, output: 'json' } });
    if (captured !== ExitCode.OK) return captured;







    if (plan.network) process.env.BATON_DRIVER_NETWORK ??= plan.network;



    const created = await nodeCreate({
      ...args,
      positionals: ['node', 'create', dst],
      flags: plan.flags,
      repeated: plan.repeated,
    });
    if (created !== ExitCode.OK) return created;











    const joined = await joinNetwork(args, dst, args.global.master);
    if (joined !== ExitCode.OK) return joined;




    await client.post(`/join-requests/${encodeURIComponent(dst)}/admit`, {});



    return await restore({
      ...args,
      positionals: ['restore', dst],
      flags: new Map<string, string | boolean>([['from', archive], ['yes', true]]),
    });
  } finally {



    rmSync(staging, { recursive: true, force: true });
  }
}
