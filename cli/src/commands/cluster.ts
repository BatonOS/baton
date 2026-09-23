// SPDX-License-Identifier: Apache-2.0



import { randomUUID } from 'node:crypto';
import { readFileSync, writeFileSync, existsSync, readdirSync, mkdtempSync, mkdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { parse } from 'yaml';
import { Client , defaultDataDir} from '../api/client.js';
import { storageFaces } from '../runtime/volume.js';
import { containerInventory, copyOut } from '../runtime/engine.js';
import { containerName } from '../runtime/target.js';
import { BATON_DIR } from '../runtime/workspace.js';
import { EXT_DIR, decorationStateOf, describeDecoration, type DecorationState, type Extension } from '../runtime/extensions.js';
import { workspacePath } from './snapshot.js';
import { reconcileApplied } from '../runtime/register.js';
import { hostResources } from '../runtime/host.js';
import { BatonError, ExitCode, usageError } from '../errors.js';
import { writeCredentialFile } from '../credential-file.js';
import { age, json, statusText, table, titled, type Column } from '../output.js';
import { flagBool, flagString, parseDuration, type ParsedArgs } from '../args.js';





























export function registerVerdict(
  reg: { applied_to?: string | null; network_ref?: string | null; network_id?: string | null } | undefined,
): 'applied elsewhere' | 'joined elsewhere' | 'not enrolled' {
  if (reg?.applied_to) return 'applied elsewhere';
  if (reg?.network_ref || reg?.network_id) return 'joined elsewhere';
  return 'not enrolled';
}

export interface NodeView {
  node_id: string;
  display_name: string;
  roles?: string[];
  master_state?: string;
  deployment?: string;
  status: string;
  agent_version?: string;
  last_seen_age_sec?: number | null;
  heartbeat_seq?: number;
  connected?: boolean;
  mirror_lag_sec?: number | null;







  workspace?: { probe?: string; state?: string; runtime_status?: string; last_exit_code?: number };
  network?: string;
  network_id?: string;
  avatar_thumb?: { content_type: string; data_base64: string } | null;

  host_address?: string;











  suspended?: boolean;
  suspend_reason?: string;
}

interface CapabilityView {
  id: string;
  node_id: string;
  name: string;
  version: string;
  risk: string;
  health: string;

  revoked: { at: string; by: string } | null;
}

interface CallView {
  call_id: string;
  status: string;
  capability: string;
  output?: unknown;
  error_code?: string;
  error_message?: string;
  duration_ms: number;
  replayed?: boolean;
  trace_id?: string;
}

interface EventView {
  seq: number;
  ts: string;
  category: string;
  event: string;
  actor?: string;
  result?: string;
  node_id?: string;
}













export async function allNodes(client: Client): Promise<NodeView[]> {
  const out: NodeView[] = [];
  let cursor = '';
  for (;;) {
    const path = cursor ? `/nodes?cursor=${encodeURIComponent(cursor)}` : '/nodes';
    const res = await client.get<{ items: NodeView[]; next_cursor?: string }>(path);
    out.push(...(res.items ?? []));
    const next = res.next_cursor ?? '';


    if (!next || next === cursor) return out;
    cursor = next;
  }
}


export async function status(args: ParsedArgs, client: Client): Promise<number> {
  let records: NodeView[] = [];
  let reachable = true;
  let failure: BatonError | undefined;

  try {
    records = await allNodes(client);
  } catch (err) {
    reachable = false;


    failure =
      err instanceof BatonError
        ? err
        : new BatonError({
            code: 'UNREACHABLE',
            message: err instanceof Error ? err.message : String(err),
            remediation:
              'This is the error as it arrived, not a diagnosis — the CLI does not recognise it.',
            exitCode: ExitCode.UNREACHABLE,
          });
  }









  const agentOf = new Map<string, string>();
  if (reachable) {
    try {
      const res = await client.get<{ identities: { name?: string; node_id?: string; bound?: boolean }[] }>(
        '/identities',
      );
      for (const i of res.identities ?? []) {
        if (i.bound && i.node_id && i.name) agentOf.set(i.node_id, i.name);
      }
    } catch {



    }
  }









  let readOnly: boolean | null = null;






  let controlPlane: Record<string, unknown> | null = null;
  if (reachable) {
    try {
      const info = await client.get<Record<string, unknown>>('/system/info');
      readOnly = (info.read_only as boolean | undefined) ?? null;
      controlPlane = {
        role: info.role ?? null,
        read_only: info.read_only ?? null,
        leader_epoch: info.leader_epoch ?? null,
        cluster_id: info.cluster_id ?? null,
        observed_by: info.observed_by ?? null,
        observed_at: info.observed_at ?? null,
        self: info.self ?? null,
        peers: info.peers ?? null,
      };
    } catch {

    }
  }





  const register = new Map(
    readRegister(args.global.dataDir ?? defaultDataDir()).map((o) => [o.name, o]),
  );

  const local = localNodes(reachable ? records : undefined, new Set(register.keys()));




  const nodes = records
    .filter((n) => n.status !== 'revoked')
    .map((n) => ({ ...n, health: healthOf(n, true) }));















  const byName = new Map(nodes.map((n) => [n.display_name, n]));
  const view = local.map((c) => {
    const n = byName.get(c.node);
    const reg = register.get(c.node);




    const wsName = reg?.template ?? 'unknown';
    return {
      node_id: n?.node_id ?? reg?.node_id ?? null,
      node: c.node,
      component: c.component || null,






      agent: n?.node_id && agentOf.get(n.node_id)
        ? `@${agentOf.get(n.node_id)}`
        : reg?.owner
          ? `@${reg.owner}`
          : null,
      roles: n?.roles ?? [],
      master_state: n?.master_state ?? null,






      network: n?.network ?? null,
      network_id: n?.network_id ?? null,














      ledger_network: reg?.network_ref ?? reg?.applied_to ?? null,
      ledger_network_id: reg?.network_id ?? null,



      ledger_fingerprint: reg?.network_fingerprint ?? null,















      joined_network_id: n?.network_id ?? reg?.network_id ?? null,
      avatar_thumb: n?.avatar_thumb ?? null,


      host_address: n?.host_address ?? null,
      workspace: wsName,
      workspace_state: n?.workspace?.state ?? (reg?.template_file ? 'declared' : 'none'),











      workspace_probe: n?.workspace ? (n.workspace.probe ?? '') : null,






















      workspace_runtime_status: n?.workspace && n.status === 'active'
        ? (n.workspace.runtime_status ?? 'unknown')
        : null,
      workspace_runtime_status_last: n?.workspace && n.status !== 'active'
        ? (n.workspace.runtime_status ?? 'unknown')
        : null,
      health: c.health,
      status: reachable ? (n ? n.status : 'not enrolled') : 'unknown',


      origin: c.origin,
      last_seen_age_sec: n?.last_seen_age_sec ?? null,
      agent_version: n?.agent_version ?? null,
    };
  });

  if (args.global.output === 'json') {










    process.stdout.write(
      json({
        control_plane_reachable: reachable,
        read_only: readOnly,
        control_plane: controlPlane,
        host: hostResources(args.global.dataDir ?? defaultDataDir()),
        nodes: view,
        network_members: nodes,
      }) + '\n',
    );
  } else {


















    const groupOf = (c: LocalNode) => byName.get(c.node)?.network ?? '';
    const columns: Column<LocalNode>[] = [









          {








            header: 'node id',
            get: (c) => {
              const id = byName.get(c.node)?.node_id?.slice(0, 8);


              return id ?? register.get(c.node)?.node_id?.slice(0, 8) ?? '-';
            },
          },
          {



            header: 'agent',
            get: (c) => {
              const n = byName.get(c.node);
              const bound = n?.node_id ? agentOf.get(n.node_id) : undefined;
              if (bound) return `@${bound}`;




              const owner = register.get(c.node)?.owner;
              if (owner) return `@${owner}`;


              return 'empty';
            },
          },
          {









            header: 'component',
            get: (c) => c.component || '-',
          },
          {




            header: 'roles',
            get: (c) => {
              const n = byName.get(c.node);
              if (!n) return '-';




              const set = n.roles ?? [];
              const ordered = [...set].sort((a, b) => (a === 'agent' ? -1 : b === 'agent' ? 1 : 0));
              const roles = ordered.join('+') || '-';
              return n.master_state ? `${roles} (${n.master_state})` : roles;
            },
          },
          {







































            header: 'workspace',
            get: (c) => {

              const name = register.get(c.node)?.template
                ?? 'unknown';
              const rt = byName.get(c.node)?.workspace;
              if (rt?.state && rt.state !== 'running') return `${name} (${rt.state})`;
              if (rt?.probe === 'failing') return `${name} (!)`;
              return name;
            },
          },
          {








            header: 'doing',
            get: (c) => {
              const rec = byName.get(c.node);
              if (!rec?.workspace) return '-';
              if (rec.status !== 'active') return `(${rec.workspace.runtime_status ?? 'unknown'})`;
              return rec.workspace.runtime_status ?? 'unknown';
            },
          },






          { header: 'health', get: (c) => c.health },
          {




            header: 'status',
            get: (c) => {
              if (!reachable) return 'unknown';
              const rec = byName.get(c.node);
              if (rec) return statusText(rec.status);























              return registerVerdict(register.get(c.node));
            },
          },
          { header: 'last seen', get: (c) => (byName.get(c.node) ? age(byName.get(c.node)!.last_seen_age_sec) : '-') },
          { header: 'version', get: (c) => byName.get(c.node)?.agent_version ?? '-' },
          {







            header: 'origin',
            get: (c) => (c.origin === 'local' ? '' : c.origin === 'network' ? 'other install' : 'orphan'),
          },
    ];

    for (const net of [...new Set(local.map(groupOf))].sort()) {
      process.stdout.write(
        titled(
          net
            ? `NETWORK: ${net}   (nodes on this server)`




            : local.some((c) => groupOf(c) === '' && register.get(c.node)?.applied_to)
              ? 'NOT IN THIS NETWORK   (this control plane has no record of them)'
              : 'NOT IN ANY NETWORK   (created here, joined nothing)',
          table(local.filter((c) => groupOf(c) === net), columns, 'no BATON nodes are running.'),
        ) + '\n',
      );
    }




    if (reachable) {
      const elsewhere = nodes.filter((n) => !local.some((c) => c.node === n.display_name));
      if (elsewhere.length) {
        process.stdout.write(
          `\n${elsewhere.length} more enrolled on other servers: ` +
            `${elsewhere.map((n) => n.display_name).join(', ')}\n` +
            '  baton node list shows every record, including revoked ones.\n',
        );
      }
    }
  }

  if (!reachable) {



    process.stderr.write(
      `\ncontrol plane: UNREACHABLE at ${args.global.master}\n` +
        (failure ? `  ${failure.message}\n  ${failure.remediation}\n` : ''),
    );
    return ExitCode.UNREACHABLE;
  }
  return ExitCode.OK;
}








const PRIMARY_COMPONENT_NAME: Record<string, string> = {
  master: 'control-api',
  standby: 'control-api',
  agent: 'agent-daemon',
};

interface LocalNode {
  node: string;
  role: string;
  component: string;
  running: boolean;



  health: 'healthy' | 'unhealthy' | 'starting' | 'stopped' | 'unknown';













  origin: 'local' | 'network' | 'orphan';
}














function healthOf(record: NodeView | undefined, running: boolean): LocalNode['health'] {
  if (!record || !running) return 'unknown';

















  if (record.master_state === 'standby') {
    if (record.status === 'active') return 'healthy';
    return record.status === 'offline' ? 'unhealthy' : 'unknown';
  }

  if (record.status === 'pending') return 'starting';
  if (record.status !== 'active') return 'unknown';









  if (record.roles?.includes('master') && record.master_state === 'active') return 'healthy';

  if (!record.connected) return 'unhealthy';














  switch (record.workspace?.state) {
    case 'failed':
      return 'unhealthy';
    case 'exited':












      return (record.workspace.last_exit_code ?? 0) === 0 ? 'stopped' : 'unhealthy';
    case 'restarting':
    case 'starting':
    case 'pending':
      return 'starting';
    case 'stopped':
    case 'paused':

      return 'unknown';
  }









  switch (record.workspace?.probe) {
    case 'failing':
      return 'unhealthy';
    case 'pending':
      return 'starting';
    case 'passing':
    case 'not-declared':
    case undefined:
      return 'healthy';
    default:


      return 'unknown';
  }
}

function localNodes(records: NodeView[] | undefined, mine?: Set<string>): LocalNode[] {
  const byName = new Map((records ?? []).map((n) => [n.display_name, n]));
  return containerInventory('baton-').map((c) => {






    const [, role = '', ...rest] = c.name.split('-');
    const [node = '', component = ''] = rest.join('-').split('--');
    return {
      node,
      role,








      component: component || PRIMARY_COMPONENT_NAME[role] || '',
      running: c.running,
      health: healthOf(byName.get(node), c.running),



      origin: mine?.has(node) ? 'local' : byName.has(node) ? 'network' : 'orphan',
    };
  });
}


export async function node(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const sub = args.positionals[1] ?? 'list';

  if (sub === 'list') {



    const local = readRegister(args.global.dataDir ?? defaultDataDir());

    let items: NodeView[] = [];
    let reachable = true;
    let unreachableWhy: BatonError | undefined;
    try {
      items = await allNodes(newClient());
    } catch (err) {







      reachable = false;









      unreachableWhy =
        err instanceof BatonError
          ? err
          : new BatonError({
              code: 'UNREACHABLE',
              message: err instanceof Error ? err.message : String(err),
              remediation:
                'This is the error as it arrived, not a diagnosis — the CLI does not recognise it. ' +
                'Check that the address is the one this control plane publishes, and that ' +
                '--admin-dir names credentials issued by ITS CA.',
              exitCode: ExitCode.UNREACHABLE,
            });
    }














    const showAll = flagBool(args, 'all');
    const buried = items.filter((n) => n.status === 'revoked').length;
    if (!showAll) items = items.filter((n) => n.status !== 'revoked');

    const joined = new Set(items.map((n) => n.display_name));
    const unjoined = local.filter((o) => !joined.has(o.name));

    if (args.global.output === 'json') {












      const merged = [
        ...items.map((n) => ({ ...n, joined: true })),
        ...unjoined.map((o) => ({
          node_id: o.node_id ?? '',
          display_name: o.name,
          roles: [] as string[],
          status: 'not-enrolled',
          driver_network: o.driver_network ?? null,
          joined: false,
        })),
      ];
      process.stdout.write(json({ nodes: merged, control_plane_reachable: reachable }) + '\n');
      return ExitCode.OK;
    }











    const bound = new Map<string, string>();
    if (reachable) {
      try {
        const res = await newClient().get<{ identities: { name?: string; node_id?: string; bound?: boolean }[] }>(
          '/identities',
        );
        for (const i of res.identities ?? []) {
          if (i.bound && i.node_id && i.name) bound.set(i.node_id, i.name);
        }
      } catch {


      }
    }

    type Row = { name: string; view?: NodeView; reg?: RegisteredOffice };
    const rows: Row[] = [
      ...items.map((n) => ({ name: n.display_name, view: n, reg: local.find((o) => o.name === n.display_name) })),
      ...unjoined.map((o) => ({ name: o.name, reg: o })),
    ];

    process.stdout.write(
      table(
        rows,
        [
          {




            header: 'node id',
            get: (r) => (r.view?.node_id ?? r.reg?.node_id ?? '').slice(0, 8) || '-',
          },
          {
            header: 'agent',
            get: (r) => {
              const id = r.view?.node_id ? bound.get(r.view.node_id) : undefined;
              if (id) return `@${id}`;




              if (r.reg?.owner) return `@${r.reg.owner}`;
              return 'empty';
            },
          },
          {








            header: 'node_template',












            get: (r) => r.reg?.template ?? 'unknown',
          },











          {



















            header: 'network',
            get: (r) => r.view?.network ?? r.reg?.network_ref ?? 'not joined',
          },
          {
            header: 'roles',
            get: (r) => {
              const ordered = [...(r.view?.roles ?? [])].sort((a, b) =>
                a === 'agent' ? -1 : b === 'agent' ? 1 : 0,
              );
              const roles = ordered.join('+') || 'agent';
              return r.view?.master_state ? `${roles} (${r.view.master_state})` : roles;
            },
          },
          {





            header: 'status',
            get: (r) => {
              if (!r.view) {












                return registerVerdict(r.reg);
              }
              const live = statusText(r.view.status);
              return r.view.suspended ? `${live} (suspended)` : live;
            },
          },
          { header: 'last seen', get: (r) => (r.view ? age(r.view.last_seen_age_sec) : '-') },
        ],
        'no nodes on this server yet. A node comes with the agent that runs on it: `baton agent create --name <name>`.',
      ) + '\n' +
        (buried > 0 && !showAll
          ? `  ${buried} revoked ${buried === 1 ? 'record is' : 'records are'} hidden. baton node list --all shows them.\n`
          : ''),
    );
    if (!reachable) {




      process.stderr.write(
        `\ncontrol plane: UNREACHABLE at ${args.global.master} — the list above is this machine's LOCAL view only\n` +
          (unreachableWhy ? `  ${unreachableWhy.message}\n  ${unreachableWhy.remediation}\n` : ''),
      );
    }
    return ExitCode.OK;
  }















  if (sub !== 'show' && sub !== 'info' && sub !== 'revoke' && sub !== 'suspend' && sub !== 'resume' && sub !== 'extensions') {
    throw usageError(
      `node ${sub} is not a subcommand`,
      'Available: baton node list | show <name> | info <name> | extensions <name> | suspend <name> | resume <name> | revoke <name>',
    );
  }




  if (sub === 'info') return nodeInfo(args);


  if (sub === 'extensions') return nodeExtensions(args);

  const client = newClient();
  const name = args.positionals[2];
  if (!name) {
    throw usageError(`node ${sub} needs a node name`, `Try \`baton node ${sub} agent01\`.`);
  }

  if (sub === 'show') {
    const detail = await client.get<Record<string, unknown>>(`/nodes/${name}`);
    process.stdout.write(json(detail) + '\n');
    return ExitCode.OK;
  }

  if (sub === 'revoke') {
    if (!args.global.yes) {


      throw usageError(
        `revoking ${name} is permanent: its certificate is blocklisted immediately and it will refuse to re-enroll`,
        `Add --yes if that is what you want: \`baton node revoke ${name} --yes\``,
      );
    }
    const res = await client.post<Record<string, unknown>>(`/nodes/${name}/revoke`, {
      reason: flagString(args, 'reason') ?? 'revoked by operator',
    });
    process.stdout.write(
      args.global.output === 'json' ? json(res) + '\n' : `revoked ${name}\n`,
    );
    return ExitCode.OK;
  }














  if (sub === 'suspend' || sub === 'resume') {
    const body: Record<string, unknown> = {};
    if (sub === 'suspend') body.reason = flagString(args, 'reason') ?? 'suspended by operator';
    const res = await client.post<Record<string, unknown>>(`/nodes/${name}/${sub}`, body);
    if (args.global.output === 'json') {
      process.stdout.write(json(res) + '\n');
      return ExitCode.OK;
    }




    const status = typeof res.status === 'string' ? res.status : 'unknown';
    process.stdout.write(
      sub === 'suspend'
        ? `suspended ${name} — it keeps its certificate and its place in the network, ` +
          `and is refused the channel until \`baton node resume ${name}\`. Liveness: ${status}\n`
        : `resumed ${name} — its next reconnect is accepted. Liveness: ${status}, ` +
          `which stays as it was until the node comes back on its own\n`,
    );
    return ExitCode.OK;
  }

  throw usageError(
    `node ${sub} is not a subcommand`,
    'Available: baton node list | show <name> | info <name> | extensions <name> | suspend <name> | resume <name> | revoke <name>',
  );
}



















export function nodeExtensions(args: ParsedArgs): number {
  const name = args.positionals[2];
  if (!name) {
    throw usageError('node extensions needs a node name', 'Try `baton node extensions agent01`.');
  }
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const templateCopy = join(dataDir, 'compose', `agent-${name}.template.yaml`);
  const jsonOut = args.global.output === 'json';

  if (!existsSync(templateCopy)) {
    if (jsonOut) {
      process.stdout.write(json({ node: name, template: null, providers: [] }) + '\n');
    } else {
      process.stdout.write(`${name}: no template recorded on this machine — nothing was declared\n`);
    }
    return ExitCode.OK;
  }








































  const doc = parse(readFileSync(templateCopy, 'utf8')) as { plugins?: unknown } | null;
  const declared: Extension[] = Array.isArray(doc?.plugins)
    ? (doc.plugins as unknown[]).flatMap((e) => {

        if (typeof e === 'string' || e === null || typeof e !== 'object') return [];
        const entry = e as { id?: unknown; config?: unknown };
        if (typeof entry.id !== 'string' || entry.config === undefined) return [];
        return [{ provider: entry.id, config: entry.config as Record<string, unknown> }];
      })
    : [];
  const mountPath = workspacePath(dataDir, containerName('agent', name))?.path;

  if (!mountPath) {
    if (jsonOut) {
      process.stdout.write(json({ node: name, workspace: null, providers: [] }) + '\n');
    } else {
      process.stdout.write(`${name}: no workspace — nothing can be declared or delivered\n`);
    }
    return ExitCode.OK;
  }






  const container = containerName('agent', name);
  const seen = mkdtempSync(join(tmpdir(), 'baton-ext-'));
  try {


    const seenExt = join(seen, BATON_DIR, 'portable', EXT_DIR);
    mkdirSync(seenExt, { recursive: true });
    const got = copyOut(container, `${mountPath}/${BATON_DIR}/portable/${EXT_DIR}`, seenExt);
    const readable = got.ok;
    const states = new Map<string, DecorationState | 'unknown'>(
      declared.map((e) => [e.provider, readable ? decorationStateOf(seen, e.provider, declared) : 'unknown']),
    );




    const undeclaredOnDisk = readable
      ? readdirSync(seenExt, { withFileTypes: true }).filter((d) => d.isDirectory())
          .map((d) => d.name).filter((n) => !declared.some((e) => e.provider === n))
      : [];

    if (jsonOut) {
      process.stdout.write(
        json({
          node: name,
          workspace: mountPath,
          readable,
          providers: declared.map((e) => ({ provider: e.provider, state: states.get(e.provider) })),
          undeclared_on_disk: undeclaredOnDisk,
        }) + '\n',
      );
      return ExitCode.OK;
    }

    process.stdout.write(`${name}  workspace ${mountPath}\n`);
    if (declared.length === 0) process.stdout.write('  no extensions declared\n');
    for (const e of declared) {
      const st = states.get(e.provider)!;
      process.stdout.write(
        st === 'unknown'
          ? `  ${e.provider}: unknown — could not read ${container} (${(got.stderr || 'no detail').trim().split('\n')[0]})\n`
          : `  ${describeDecoration(e.provider, st)}\n`,
      );
    }
    for (const n of undeclaredOnDisk) {
      process.stdout.write(`  ${n}: on disk but not declared by this node's template\n`);
    }
    return ExitCode.OK;
  } finally {
    rmSync(seen, { recursive: true, force: true });
  }
}























export function nodeInfo(args: ParsedArgs): number {
  const name = args.positionals[2];
  if (!name) {
    throw usageError('node info needs a node name', 'Try `baton node info agent01`.');
  }
  const faces = storageFaces(args.global.dataDir ?? defaultDataDir(), name);

  if (args.global.output === 'json') {
    process.stdout.write(json({ node: name, storage: faces }) + '\n');
    return ExitCode.OK;
  }

  const gib = (n: number): string => `${(n / 1024 ** 3).toFixed(1)} GiB`;
  const volume = faces.volume.configured
    ? 'unknown' in faces.volume
      ? `unknown    could not read it — ${faces.volume.unknown}`
      : `${gib(faces.volume.sizeBytes).padEnd(10)}${gib(faces.volume.freeBytes)} free`
    : 'not configured  this node declared no volumeSize; /var/lib/baton is on the shared layer';

  process.stdout.write(
    `${name}\n` +
      `  disk (volume)   ${volume}\n` +
      '  rootfs          shared     not quota\'d — the container\'s / is shared with every node on this machine\n',
  );
  return ExitCode.OK;
}


export async function token(args: ParsedArgs, client: Client): Promise<number> {
  const sub = args.positionals[1] ?? 'list';

  if (sub === 'list') {
    const res = await client.get<{ items: Record<string, unknown>[] }>('/tokens');
    process.stdout.write(json(res.items ?? []) + '\n');
    return ExitCode.OK;
  }

  if (sub === 'create') {
    const role = flagString(args, 'role') ?? 'agent';





    if (role === 'agent') {
      throw usageError(
        'agents are admitted, not tokened: the master approves an agent\'s pending application by name with `baton network add --agent <name>`',
        'The agent applies from its machine — `baton agent join --name reviewer --network <this network>` — and ' +
          'this master admits it here. `token create` keeps --role master, which is what a standby enrols with.',
      );
    }







    if (role === 'standby') {
      throw usageError(
        'standby is not a role: a standby is a node with the master role, running as a read-only mirror of the control plane that refuses every write',
        'Use `--role master`. `scripts/poc-up.sh` has always posted `{"role":"master"}` for the mirror; ' +
          'this flag was the only place that said otherwise.',
      );
    }
    const ttl = flagString(args, 'ttl') ?? '15m';
    const maxUses = Number.parseInt(flagString(args, 'max-uses') ?? '1', 10);
    parseDuration(ttl);

    const res = await client.post<{ token_id: string; token: string; expires_at: string }>(
      '/tokens',
      { role, ttl, max_uses: maxUses },
    );

    const outFile = flagString(args, 'out-file');
    if (outFile) {






      const written = writeCredentialFile(outFile, res.token + '\n', 'enrollment token');
      process.stdout.write(
        args.global.output === 'json'
          ? json({ token_id: res.token_id, expires_at: res.expires_at, path: outFile }) + '\n'
          : `token written to ${outFile} (mode 0${written.toString(8)})\n` +
            `  expires ${res.expires_at}\n` +
            '  This is the only copy; only its hash is stored.\n',
      );
      return ExitCode.OK;
    }



    if (process.stdout.isTTY && args.global.output !== 'json') {
      process.stderr.write(
        'warning: printing a credential to the terminal puts it in scrollback.\n' +
          '         Prefer --out-file <path>.\n\n',
      );
    }
    process.stdout.write(args.global.output === 'json' ? json(res) + '\n' : res.token + '\n');
    return ExitCode.OK;
  }

  if (sub === 'revoke') {
    const id = args.positionals[2];
    if (!id) throw usageError('token revoke needs a token id', 'Find it with `baton token list`.');
    await client.delete(`/tokens/${id}`);
    process.stdout.write(`revoked ${id}\n`);
    return ExitCode.OK;
  }

  throw usageError(
    `token ${sub} is not a subcommand`,
    'Available: baton token create | list | revoke <id>',
  );
}


export async function capability(args: ParsedArgs, client: Client): Promise<number> {
  const sub = args.positionals[1] ?? 'list';

  if (sub === 'list') {
    const nodeName = flagString(args, 'node');
    const path = nodeName ? `/capabilities?node_id=${encodeURIComponent(nodeName)}` : '/capabilities';
    const res = await client.get<{ items: CapabilityView[] }>(path);
    const items = res.items ?? [];
    process.stdout.write(
      args.global.output === 'json'
        ? json(items) + '\n'
        : table(
            items,
            [
              { header: 'capability', get: (c) => `${c.name}@${c.version}` },
              { header: 'risk', get: (c) => c.risk },
              { header: 'health', get: (c) => c.health },



              { header: 'allowed', get: (c) => (c.revoked ? 'no' : 'yes') },


              { header: 'revoked by', get: (c) => (c.revoked ? c.revoked.by || 'unrecorded' : '') },
            ],
            'no capabilities are registered.',
          ) + '\n',
    );
    return ExitCode.OK;
  }

  if (sub === 'grant' || sub === 'revoke') {
    const id = args.positionals[2];
    if (!id) {
      throw usageError(
        `capability ${sub} needs a capability id`,
        'Find it with `baton capability list --output json`.',
      );
    }
    await client.post(`/capabilities/${id}/${sub}`);


    process.stdout.write(`${sub === 'grant' ? 'granted' : 'revoked'} ${id}\n`);
    return ExitCode.OK;
  }

  throw usageError(
    `capability ${sub} is not a subcommand`,
    'Available: baton capability list | grant <id> | revoke <id>',
  );
}








export async function call(args: ParsedArgs, client: Client): Promise<number> {
  const capabilityName = args.positionals[1];
  if (!capabilityName) {
    throw usageError(
      'call needs a capability name',
      'Try `baton call sys.echo --node agent01 --input \'{"message":"hi"}\'`.',
    );
  }

  const nodeName = flagString(args, 'node');
  if (!nodeName) {
    throw usageError(
      '--node is required',
      'Name the node to run this on, e.g. --node agent01.',
    );
  }

  let input: unknown = {};
  const inputFile = flagString(args, 'input-file');
  const inputRaw = flagString(args, 'input');
  try {
    if (inputFile) input = JSON.parse(readFileSync(inputFile, 'utf8'));
    else if (inputRaw) input = JSON.parse(inputRaw);
  } catch (err) {
    throw usageError(
      `the input is not valid JSON: ${String(err)}`,
      'Pass a JSON object, e.g. --input \'{"message":"hi"}\'.',
    );
  }




  const idempotencyKey = flagString(args, 'idempotency-key') ?? randomUUID();





  if (args.global.dryRun) {
    const plan = {
      would_call: capabilityName,
      node: nodeName,
      input,
      idempotency_key: idempotencyKey,
      timeout_sec: Math.floor(args.global.timeoutMs / 1000),
    };
    process.stdout.write(
      args.global.output === 'json'
        ? json(plan) + '\n'
        : `would call ${capabilityName} on ${nodeName} with ${JSON.stringify(input)}\n` +
            `  idempotency key ${idempotencyKey}; nothing was dispatched.\n`,
    );
    return ExitCode.OK;
  }

  const res = await client.post<CallView>('/calls?wait=true', {
    capability: capabilityName,
    node_name: nodeName,
    input,
    idempotency_key: idempotencyKey,
    timeout_sec: Math.floor(args.global.timeoutMs / 1000),
  });

  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
  } else if (res.status === 'succeeded') {
    if (res.replayed) {
      process.stdout.write(
        `(replayed: this idempotency key already ran; showing the original result)\n`,
      );
    }
    process.stdout.write(json(res.output ?? {}) + '\n');
  } else {
    process.stderr.write(
      `call ${res.status}: ${res.error_code ?? ''} ${res.error_message ?? ''}\n`,
    );
  }

  return res.status === 'succeeded' ? ExitCode.OK : ExitCode.PARTIAL;
}


export async function events(args: ParsedArgs, client: Client): Promise<number> {
  const params = new URLSearchParams();
  params.set('limit', flagString(args, 'limit') ?? '50');
  if (flagString(args, 'since-seq')) params.set('since_seq', flagString(args, 'since-seq')!);
  if (flagString(args, 'category')) params.set('category', flagString(args, 'category')!);
  if (flagString(args, 'event')) params.set('event', flagString(args, 'event')!);
  if (flagBool(args, 'verify')) params.set('verify', 'true');

  const res = await client.get<{
    items: EventView[];
    chain_valid?: boolean;
    chain_broken_at?: number;
  }>(`/events?${params.toString()}`);

  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
  } else {
    process.stdout.write(
      table(
        res.items ?? [],
        [
          { header: 'seq', get: (e) => String(e.seq), right: true },
          { header: 'time', get: (e) => e.ts.replace('T', ' ').replace(/\.\d+Z?$/, '') },
          { header: 'event', get: (e) => e.event },
          { header: 'actor', get: (e) => e.actor || '-' },
          { header: 'result', get: (e) => e.result || '-' },
        ],
        'no events yet.',
      ) + '\n',
    );

    if (res.chain_valid !== undefined) {
      process.stdout.write(
        res.chain_valid
          ? '\nhash chain intact — no record has been altered since it was written.\n' +
            '  (This detects tampering. On a single-server install it cannot prevent it.)\n'
          : `\nHASH CHAIN BROKEN at seq ${res.chain_broken_at}: a record was altered.\n`,
      );
    }
  }

  if (res.chain_valid === false) return ExitCode.PARTIAL;
  return ExitCode.OK;
}















export async function failover(args: ParsedArgs): Promise<number> {
  throw new BatonError({
    code: 'NOT_SUPPORTED',
    message: 'this CLI has no verb that promotes a standby: a standby here is a read-only mirror',
    remediation:
      'To recover from a lost control plane now, restore the mirror\'s data directory onto a ' +
      'new primary.\n  The control plane itself implements operator-run promotion — POST /api/v1alpha1/system/promote, ' +
      'addressed to the standby, super-admin only, raising the leader epoch — but this CLI has no verb for it; ' +
      '`baton transfer master` promotes only with an offer signed by a live master. You run it, so ' +
      'you are the witness that the old primary is gone.\n  Automatic promotion stays out — ' +
      'without a quorum a node cannot tell a dead peer from a partition, and two masters is ' +
      'worse than an outage because it is silent.',
    exitCode: ExitCode.UNSUPPORTED,
  });
}










interface RegisteredOffice {
  node_id: string;
  name: string;
  template: string | null;
  template_file: string | null;
  applied_to?: string | null;

  network_ref?: string | null;








  network_id?: string | null;
  network_fingerprint?: string | null;
  owner: string | null;









  driver_network: string | null;
}

function readRegister(dataDir: string): RegisteredOffice[] {
  const dir = join(dataDir, 'nodes');
  if (!existsSync(dir)) return [];
  const out: RegisteredOffice[] = [];
  for (const f of readdirSync(dir)) {
    if (!f.endsWith('.json')) continue;



    reconcileApplied(dataDir, f.replace(/\.json$/, ''));
    try {
      const path = join(dir, f);
      const raw = JSON.parse(readFileSync(path, 'utf8')) as RegisteredOffice & { network?: string | null };




      if (raw.network !== undefined) {
        raw.driver_network = raw.driver_network ?? raw.network;
        delete raw.network;
        try {
          writeFileSync(path, JSON.stringify(raw, null, 2) + '\n', { mode: 0o600 });
        } catch {



        }
      }
      out.push(raw);
    } catch {


    }
  }
  return out;
}
