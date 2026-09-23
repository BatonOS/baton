// SPDX-License-Identifier: Apache-2.0














































import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';

import type { Client } from '../api/client.js';
import { defaultDataDir } from '../api/client.js';
import { volumePaths } from '../runtime/volume.js';
import { flagBool, flagString, type ParsedArgs } from '../args.js';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { json } from '../output.js';
import { joinNetwork, officeOfAgent } from './agent.js';
import { nodeCreate } from './create.js';
import { destroy, lifecycle } from './lifecycle.js';
import { restore, snapshot } from './snapshot.js';
import { allNodes } from './cluster.js';
import { markLeftNetwork, reconcileApplied } from '../runtime/register.js';
import { LOCAL_HOSTS } from '../runtime/resolver.js';


interface RegistryNode {
  node_id: string;
  display_name: string;
  status?: string;
  labels?: Record<string, string>;
}



























function volumeDoesNotTravel(args: ParsedArgs, office: string): string {
  const paths = volumePaths(args.global.dataDir ?? defaultDataDir(), office);
  if (!existsSync(paths.img)) return '';
  return (
    "  the data volume does not travel: the destination creates an empty one at the template's " +
    'declared size, and the node fills it again as it runs\n'
  );
}









export async function sourceNodeIdOrThrow(client: Pick<Client, 'get'>, name: string): Promise<string> {
  const n = await client.get<{ node_id?: string; status?: string }>(`/nodes/${encodeURIComponent(name)}`);
  if (!n.node_id) {
    throw preconditionError(
      `the control plane returned no node id for ${name}`,
      'Nothing has been changed. Check `baton node show ' + name + '` and run the migration again.',
    );
  }
  if (n.status === 'revoked') {
    throw preconditionError(
      `${name} is already revoked (node ${n.node_id})`,
      'Nothing has been changed. A revoked node has no identity left to move.',
    );
  }
  return n.node_id;
}


async function providerFor(client: Client, host: string): Promise<{ node: RegistryNode; driver: string }> {


  const items = (await allNodes(client)) as unknown as RegistryNode[];
  const candidates = items.filter((n) => (n.labels ?? {}).host === host && n.status !== 'revoked');
  if (candidates.length === 0) {
    throw preconditionError(
      `no provider node is registered for ${host}`,
      `A destination declares itself with a node that joined this network carrying labels: on ${host}, ` +
        `\`baton agent create --name <n> --no-network --labels driver=<container|microvm>,host=${host}\` then ` +
        '`baton agent join --name <n> --network <this network>`, and admit it here. That node is how this network knows the destination host.',
    );
  }
  const withDriver = candidates.filter((n) => (n.labels ?? {}).driver);
  if (withDriver.length === 0) {
    throw preconditionError(
      `${host} has a registered node (${candidates.map((n) => n.display_name).join(', ')}) but none declares its driver`,
      'Add the label driver=container or driver=microvm to the provider node; a driver is never assumed.',
    );
  }
  const n = withDriver[0]!;
  return { node: n, driver: (n.labels ?? {}).driver! };
}































export type SourceOutcome = 'removed' | 'kept' | 'left-behind';












export function sourceOutcome(gone: number, keepSource: boolean): SourceOutcome {
  if (keepSource) return 'kept';
  return gone === ExitCode.OK || gone === ExitCode.PARTIAL ? 'removed' : 'left-behind';
}

export async function migrate(args: ParsedArgs, client: Client): Promise<number> {
  const name = flagString(args, 'name');
  const to = flagString(args, 'to');
  const from = flagString(args, 'from');
  if (to && from) {
    throw usageError(
      'migrate takes --to OR --from, not both',
      '--to <host> pushes an agent from here to there; --from <host> pulls one from there to here.',
    );
  }



  if (from) return migrateFrom(args, client, name, from);
  if (!name || !to) {
    throw usageError(
      'migrate needs --name <agent> and --to <host> (or --from <host>)',
      'For example: baton migrate --name bob --to root@10.0.0.7 --yes',
    );
  }
  const withCache = flagBool(args, 'with-cache');
  const keepSource = flagBool(args, 'keep-source');



  const identity = flagString(args, 'identity');
  const dataDir = args.global.dataDir ?? defaultDataDir();


  const first = officeOfAgent(args, dataDir, name);
  reconcileApplied(dataDir, first.name);
  const office = officeOfAgent(args, dataDir, name);
  if (!office.driver_network) {
    throw preconditionError(
      `${name} is not in a network; there is nothing to migrate it within`,
      'Migration moves an agent between machines inside one network. Join first: `baton agent join --name ' + name + ' --network <network>`.',
    );
  }

  const hostOnly = to.includes('@') ? to.split('@')[1]! : to;
  const local = LOCAL_HOSTS.has(hostOnly);



  const driver = local ? 'container' : (await providerFor(client, hostOnly)).driver;
  if (local && keepSource) {
    throw usageError(
      '--keep-source cannot hold on the same host',
      'The new office takes the same name, and volumes under that name would give it the revoked identity. ' +
        'On this host the archive is the safety net; it is kept until the migration succeeds and its path is printed.',
    );
  }




  let entry = args.global.master;
  if (!local) {
    const net = await client.get<{ endpoints?: { address: string; port: number }[] }>('/networks/self');



    const ep = (net.endpoints ?? []).find((e) => !LOCAL_HOSTS.has(e.address));
    if (!ep) {
      throw preconditionError(
        'this network has published no endpoint that another host could reach (loopback does not count)',
        'On this master: baton network publish --endpoint https://<reachable host>:8443 — then migrate again.',
      );
    }
    entry = `https://${ep.address}:${ep.port}`;
  }
  const remoteDir = `.baton-migrate/${name}`;

  if (args.global.dryRun) {
    process.stdout.write(
      `would migrate ${name} (office ${office.name}) to ${to} [${driver}]:\n` +
        `  snapshot${withCache ? ' (with cache)' : ' (without cache)'}` +
        (local ? '' : ` → copy the archive and the kept template to ${to}:~/${remoteDir}/ over SSH`) +
        ` → revoke ${office.name}` + (local ? '' : ` (stop it; volumes kept${keepSource ? ' for good' : ' until the move succeeds'})`) +
        ` → ${local ? 'open a new office' : `on ${to}: baton agent create`} from the kept template → ` +
        `apply as ${name} to ${entry} → admit → restore${local ? '' : ' there'}\n` +
        '  the address does not change; the node id does\n' +
        volumeDoesNotTravel(args, office.name),
    );
    return ExitCode.OK;
  }
  if (!flagBool(args, 'yes')) {
    throw preconditionError(
      `migrating ${name} revokes its current node's identity and moves it to a new node on ${to}`,
      'Add --yes if that is what you want. The archive is kept until the move succeeds.',
    );
  }

  const staging = mkdtempSync(join(tmpdir(), 'baton-migrate-'));
  const archive = join(staging, `${name}.tar.gz`);
  const templateCopy = join(dataDir, 'compose', `agent-${office.name}.template.yaml`);
  if (!existsSync(templateCopy)) {
    throw preconditionError(
      `${office.name} has no kept Node Template at ${templateCopy}`,
      'It was created before templates were kept. Snapshot it, create the destination by hand from the same template, and restore.',
    );
  }
  const keptTemplate = join(staging, 'template.yaml');
  spawnSync('cp', [templateCopy, keptTemplate]);


  const snap = await snapshot({ ...args, positionals: ['snapshot', office.name], flags: new Map([['out', archive]]), global: { ...args.global, output: 'json', dryRun: false } });
  if (snap !== ExitCode.OK) return snap;
  if (!withCache) dropMember(archive, 'cache');
  process.stdout.write(`  1  captured ${name}'s state → ${archive}\n`);
  process.stdout.write(volumeDoesNotTravel(args, office.name));







  if (!local) {
    sshOrThrow(to, `mkdir -p ~/${remoteDir}`, 'prepare the staging directory', archive, identity);
    const scp = spawnSync('scp', ['-q', '-o', 'BatchMode=yes', ...idOpts(identity), archive, keptTemplate, `${to}:~/${remoteDir}/`], { encoding: 'utf8' });
    if (scp.status !== 0) {
      throw new BatonError({ code: 'MIGRATE_TRANSFER', message: `could not copy the archive to ${to}`, remediation: (scp.stderr || '').trim() || 'Is SSH to the destination set up (keys, not passwords)?', exitCode: ExitCode.UNREACHABLE });
    }
    process.stdout.write(`  1b copied the archive and template to ${to}:~/${remoteDir}/\n`);
  }













  const sourceNodeID = await sourceNodeIdOrThrow(client, office.name);
  await client.post(`/nodes/${encodeURIComponent(sourceNodeID)}/revoke`, { reason: `migrated to ${to} by operator` });

  markLeftNetwork(dataDir, office.name);





  let source: SourceOutcome = 'removed';
  if (local) {
    process.stdout.write(`  2  revoked ${office.name} (name released; the archive above is now the only copy)\n`);

    const gone = await destroy({ ...args, positionals: ['destroy', office.name], flags: new Map([['yes', true]]), global: { ...args.global, output: 'json' } }, () => client, { identityRevoked: sourceNodeID });
    if (gone !== ExitCode.OK && gone !== ExitCode.PARTIAL) {
      throw new BatonError({ code: 'MIGRATE_SOURCE', message: `could not remove the source office ${office.name}`, remediation: `The archive is at ${archive}. Remove the office by hand and restore into a new one.`, exitCode: ExitCode.PARTIAL });
    }
  } else {



    await lifecycle({ ...args, positionals: ['stop', office.name], global: { ...args.global, output: 'json' } }, 'stop');
    process.stdout.write(`  2  revoked ${office.name} (name released; its volumes are kept, stopped, until the move succeeds)\n`);
  }


  if (local) {
    const created = await nodeCreate({ ...args, positionals: ['node', 'create', name], flags: new Map<string, string | boolean>([['template', keptTemplate], ['owner', name]]), global: { ...args.global, output: 'json' } });
    if (created !== ExitCode.OK) {
      throw new BatonError({ code: 'MIGRATE_CREATE', message: `could not open the new office for ${name}`, remediation: `The archive is at ${archive}. Open one by hand: baton agent create --name ${name} --template ${keptTemplate}; then restore.`, exitCode: ExitCode.PARTIAL });
    }
    const applied = await joinNetwork({ ...args, global: { ...args.global, output: 'table' } }, name, entry);
    if (applied !== ExitCode.OK) return applied;
  } else {


    const env = process.env.BATON_DRIVER_NETWORK ? `BATON_DRIVER_NETWORK=${shq(process.env.BATON_DRIVER_NETWORK)} ` : '';
    sshOrThrow(to, `${env}baton agent create --name ${shq(name)} --no-network --template ~/${remoteDir}/template.yaml`, `open the office on ${to}`, archive, identity);
    sshOrThrow(to, `${env}baton agent join --name ${shq(name)} --network ${shq(entry)}`, `apply from ${to}`, archive, identity);
    process.stdout.write(`  3  ${to} opened an office for ${name} and applied to ${entry}\n`);
  }


  await client.post(`/join-requests/${encodeURIComponent(name)}/admit`, { reason: 'migration' });
  process.stdout.write(`  4  admitted ${name}; the new node enrols by itself\n`);


  let active: { status?: string; node_id?: string } | null = null;
  for (let i = 0; i < 40; i++) {
    try {
      const n = await client.get<{ status?: string; node_id?: string }>(`/nodes/${encodeURIComponent(name)}`);
      if (n.status === 'active') { active = n; break; }
    } catch {  }
    await new Promise((r) => setTimeout(r, 2000));
  }
  if (!active) {
    throw new BatonError({ code: 'MIGRATE_ENROL', message: `${name}'s new node did not become active in time`, remediation: `Watch it: baton status. The archive is at ${archive}; restore with baton restore ${name} --from ${archive} --yes once it is up.`, exitCode: ExitCode.PARTIAL });
  }
  process.stdout.write(`  5  ${name} is in as node ${active.node_id ?? '?'}\n`);
  if (local) reconcileApplied(dataDir, name);


  if (local) {
    const rc = await restore({ ...args, positionals: ['restore', name], flags: new Map<string, string | boolean>([['from', archive], ['yes', true]]), global: { ...args.global, output: 'json' } });
    if (rc !== ExitCode.OK) {
      throw new BatonError({ code: 'MIGRATE_RESTORE', message: `the new node is up but restoring into it failed`, remediation: `The archive is at ${archive}: baton restore ${name} --from ${archive} --yes`, exitCode: ExitCode.PARTIAL });
    }
  } else {
    sshOrThrow(to, `baton restore ${shq(name)} --from ~/${remoteDir}/${shq(`${name}.tar.gz`)} --yes`, `restore on ${to}`, archive, identity);
    sshOrThrow(to, `rm -rf ~/${remoteDir}`, 'remove the remote staging', archive, identity);
    process.stdout.write(`  6  restored on ${to}; remote staging removed\n`);


    if (!keepSource) {













      const gone = await destroy({ ...args, positionals: ['destroy', office.name], flags: new Map([['yes', true]]), global: { ...args.global, output: 'json' } }, () => client, { identityRevoked: sourceNodeID }).catch(() => ExitCode.PARTIAL);
      source = sourceOutcome(gone, keepSource);
      process.stdout.write(source === 'removed'
        ? `  7  removed the source office ${office.name} here (its state now lives on ${to})\n`
        : `  7  could not remove the source office ${office.name} here — it is revoked and stopped, and its volumes are still on this host. Remove it by hand: baton destroy ${office.name} --yes\n`);
    } else {
      source = sourceOutcome(ExitCode.OK, keepSource);
      process.stdout.write(`  7  kept the source office ${office.name} here, stopped and revoked (--keep-source)\n`);
    }
  }
  rmSync(staging, { recursive: true, force: true });

  const view = await client.get<{ node_id?: string }>(`/nodes/${encodeURIComponent(name)}`).catch(() => ({} as { node_id?: string }));
  if (args.global.output === 'json') {
    process.stdout.write(json({ agent: name, to, driver, node_id: view.node_id ?? null, address_unchanged: true, source }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    `\n  ${name} migrated to ${to} [${driver}].\n` +
      `    node id   ${view.node_id ?? '(see baton node show)'}   (new — the identity is the same, its node changed)\n` +
      `    address   unchanged\n` +
      `  restart it so the runtime reads what it was given: ${local ? '' : `on ${to}: `}baton restart ${name}\n`,
  );
  return ExitCode.OK;
}
















async function migrateFrom(args: ParsedArgs, client: Client, name: string | undefined, fromHost: string): Promise<number> {
  if (!name) {
    throw usageError('migrate --from needs --name <agent>', 'For example: baton migrate --name bob --from root@10.0.0.7 --yes');
  }
  const withCache = flagBool(args, 'with-cache');
  const keepSource = flagBool(args, 'keep-source');
  const identity = flagString(args, 'identity');
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const remoteDir = `.baton-migrate/${name}`;




  const nodes = (await allNodes(client)) as Array<{ display_name?: string; status?: string; host_address?: string; node_id?: string }>;
  const current = nodes.find((n) => n.display_name === name && n.status !== 'revoked');
  if (!current) {
    throw preconditionError(
      `no active agent called ${name} is in this network`,
      'migrate --from moves an agent that is already a member here. See `baton status`.',
    );
  }
  const fromOnly = fromHost.includes('@') ? fromHost.split('@')[1]! : fromHost;
  if (LOCAL_HOSTS.has(fromOnly)) {
    throw usageError(
      `--from ${fromHost} is this machine; there is nothing to pull across`,
      'To move between two nodes on this host, use --to localhost.',
    );
  }

  if (args.global.dryRun) {
    process.stdout.write(
      `would migrate ${name} from ${fromHost} to here:\n` +
        `  on ${fromHost}: baton snapshot${withCache ? ' (with cache)' : ' (without cache)'} → pull the archive and its kept config here → ` +
        `revoke ${name} → open a new office here from the pulled config → apply → admit → restore here → ` +
        (keepSource ? `keep the source on ${fromHost} (stopped, revoked)` : `destroy the source on ${fromHost}`) + '\n' +
        '  the address does not change; the node id does\n',
    );
    return ExitCode.OK;
  }
  if (!flagBool(args, 'yes')) {
    throw preconditionError(
      `migrating ${name} from ${fromHost} revokes its current node and rebuilds it here`,
      'Add --yes if that is what you want. The archive is kept here until the move succeeds.',
    );
  }

  const staging = mkdtempSync(join(tmpdir(), 'baton-migrate-'));
  const archive = join(staging, `${name}.tar.gz`);
  const keptTemplate = join(staging, 'template.yaml');



  sshOrThrow(fromHost, `mkdir -p ~/${remoteDir}`, 'prepare the staging directory on the source', archive, identity);
  sshOrThrow(fromHost, `baton snapshot ${shq(name)} --out ~/${remoteDir}/${shq(`${name}.tar.gz`)}`, `capture ${name} on ${fromHost}`, archive, identity);
  const tpl = sshOrThrow(fromHost, `baton agent config show ${shq(name)}`, `read ${name}'s config on ${fromHost}`, archive, identity);
  writeFileSync(keptTemplate, tpl, { mode: 0o644 });
  const scp = spawnSync('scp', ['-q', '-o', 'BatchMode=yes', ...idOpts(identity), `${fromHost}:~/${remoteDir}/${name}.tar.gz`, archive], { encoding: 'utf8' });
  if (scp.status !== 0) {
    throw new BatonError({ code: 'MIGRATE_TRANSFER', message: `could not pull the archive from ${fromHost}`, remediation: (scp.stderr || '').trim() || 'Is SSH to the source set up (keys, not passwords)?', exitCode: ExitCode.UNREACHABLE });
  }
  if (!withCache) dropMember(archive, 'cache');
  process.stdout.write(`  1  captured ${name} on ${fromHost} and pulled it here → ${archive}\n`);






  if (!current.node_id) {
    throw preconditionError(`the control plane listed ${name} without a node id`, 'Nothing has been revoked. Check `baton node show ' + name + '` and run the migration again.');
  }
  await client.post(`/nodes/${encodeURIComponent(current.node_id)}/revoke`, { reason: `migrated from ${fromHost} by operator` });
  process.stdout.write(`  2  revoked ${name} on ${fromHost} (name released; the pulled archive is the safety net)\n`);




  const created = await nodeCreate({ ...args, positionals: ['node', 'create', name], flags: new Map<string, string | boolean>([['template', keptTemplate], ['owner', name]]), global: { ...args.global, output: 'json' } });
  if (created !== ExitCode.OK) {
    throw new BatonError({ code: 'MIGRATE_CREATE', message: `could not open the new office for ${name} here`, remediation: `The archive is at ${archive}. Open one by hand: baton agent create --name ${name} --template ${keptTemplate}; then restore.`, exitCode: ExitCode.PARTIAL });
  }
  const applied = await joinNetwork({ ...args, global: { ...args.global, output: 'table' } }, name, args.global.master);
  if (applied !== ExitCode.OK) return applied;
  process.stdout.write(`  3  opened a new office for ${name} here and applied\n`);


  await client.post(`/join-requests/${encodeURIComponent(name)}/admit`, { reason: 'migration' });
  process.stdout.write(`  4  admitted ${name}; the new node enrols by itself\n`);


  let active: { status?: string; node_id?: string } | null = null;
  for (let i = 0; i < 40; i++) {
    try {
      const n = await client.get<{ status?: string; node_id?: string }>(`/nodes/${encodeURIComponent(name)}`);
      if (n.status === 'active') { active = n; break; }
    } catch {  }
    await new Promise((r) => setTimeout(r, 2000));
  }
  if (!active) {
    throw new BatonError({ code: 'MIGRATE_ENROL', message: `${name}'s new node did not become active in time`, remediation: `Watch it: baton status. The archive is at ${archive}; restore with baton restore ${name} --from ${archive} --yes once it is up.`, exitCode: ExitCode.PARTIAL });
  }
  process.stdout.write(`  5  ${name} is in as node ${active.node_id ?? '?'}\n`);
  reconcileApplied(dataDir, name);


  const rc = await restore({ ...args, positionals: ['restore', name], flags: new Map<string, string | boolean>([['from', archive], ['yes', true]]), global: { ...args.global, output: 'json' } });
  if (rc !== ExitCode.OK) {
    throw new BatonError({ code: 'MIGRATE_RESTORE', message: `the new node is up but restoring into it failed`, remediation: `The archive is at ${archive}: baton restore ${name} --from ${archive} --yes`, exitCode: ExitCode.PARTIAL });
  }
  process.stdout.write(`  6  restored ${name} here from the pulled archive\n`);















  if (!keepSource) {
    const gone = sshTolerant(fromHost, `baton destroy ${shq(name)} --yes`, identity);
    if (gone.ok || /identity/.test(gone.out)) {
      process.stdout.write(`  7  removed the source office ${name} on ${fromHost} (its state now lives here)\n`);
    } else {
      process.stdout.write(`  7  could not remove the source office on ${fromHost}; remove it by hand there: baton destroy ${name} --yes\n`);
    }
  } else {
    process.stdout.write(`  7  kept the source office ${name} on ${fromHost}, revoked (--keep-source)\n`);
  }
  sshTolerant(fromHost, `rm -rf ~/${remoteDir}`, identity);
  rmSync(staging, { recursive: true, force: true });

  const view = await client.get<{ node_id?: string }>(`/nodes/${encodeURIComponent(name)}`).catch(() => ({} as { node_id?: string }));
  if (args.global.output === 'json') {
    process.stdout.write(json({ agent: name, from: fromHost, to: 'localhost', node_id: view.node_id ?? null, address_unchanged: true }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    `\n  ${name} migrated from ${fromHost} to here.\n` +
      `    node id   ${view.node_id ?? '(see baton node show)'}   (new — the identity is the same, its node changed)\n` +
      `    address   unchanged\n` +
      `  restart it so the runtime reads what it was given: baton restart ${name}\n`,
  );
  return ExitCode.OK;
}


function idOpts(identity: string | undefined): string[] {
  return identity ? ['-i', identity, '-o', 'IdentitiesOnly=yes'] : [];
}


function sshOrThrow(to: string, command: string, what: string, archive: string, identity?: string): string {
  const r = spawnSync('ssh', ['-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15', ...idOpts(identity), to, `bash -lc ${shq(command)}`], { encoding: 'utf8' });
  if (r.status !== 0) {
    throw new BatonError({
      code: 'MIGRATE_REMOTE',
      message: `could not ${what}: ${(r.stderr || r.stdout || '').trim().split('\n').slice(-3).join(' | ') || `ssh exited ${r.status}`}`,
      remediation: `The archive is at ${archive}. Fix the destination and run migrate again, or restore by hand there.`,
      exitCode: ExitCode.PARTIAL,
    });
  }
  return r.stdout;
}


function sshTolerant(host: string, command: string, identity?: string): { ok: boolean; out: string } {
  const r = spawnSync('ssh', ['-o', 'BatchMode=yes', '-o', 'ConnectTimeout=15', ...idOpts(identity), host, `bash -lc ${shq(command)}`], { encoding: 'utf8' });
  return { ok: r.status === 0, out: (r.stdout || '') + (r.stderr || '') };
}


function shq(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}


function dropMember(archive: string, member: string): void {
  const dir = mkdtempSync(join(tmpdir(), 'baton-migrate-filter-'));
  try {
    spawnSync('tar', ['-xzf', archive, '-C', dir]);
    rmSync(join(dir, member), { recursive: true, force: true });
    const members = ['workspace', 'state', 'cache', 'secrets'].filter((m) => existsSync(join(dir, m)));
    mkdirSync(join(dir, 'workspace'), { recursive: true });
    spawnSync('tar', ['-czf', archive, '-C', dir, ...new Set(['workspace', ...members])]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}
