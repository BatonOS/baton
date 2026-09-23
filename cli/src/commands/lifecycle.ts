// SPDX-License-Identifier: Apache-2.0



import {
  composeVersion,
  engineVersion,
  projectDown,
  projectVerb,
  containerExists,
  removeContainer,
  removeVolumesFor,
  volumesFor,
  hostLxcfs,
  MIN_ENGINE_MAJOR,
  engineMeetsMinimum,
} from '../runtime/engine.js';
import { removeVolume, unmountVolume, volumePaths } from '../runtime/volume.js';
import { existsSync, readFileSync, rmSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { createConnection } from 'node:net';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { json, table } from '../output.js';
import {
  listManifests,
  manifestVolumes,
  parseRole,
  resolveManifest,
  selectManifests,
} from '../runtime/manifests.js';
import type { Client } from '../api/client.js';
import { defaultDataDir } from '../api/client.js';
import { driverNetwork, flagBool, flagString, type ParsedArgs } from '../args.js';
import type { LocalNodeState } from './create.js';











function registerJoinedNetwork(args: ParsedArgs, node: string): string | null {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  try {
    const reg = JSON.parse(
      readFileSync(join(dataDir, 'nodes', `${node}.json`), 'utf8'),
    ) as { network_ref?: string | null; network_id?: string | null };
    return reg.network_ref ?? reg.network_id ?? null;
  } catch {



    return null;
  }
}

interface Check {
  name: string;
  status: 'ok' | 'warn' | 'fail';
  detail: string;
  remediation?: string;
}
























export const NODE_RANGE = '^20.17.0 || ^22.6.0 || 24';
export function nodeSupported(version: string): boolean {
  const [maj, min] = version.replace(/^v/, '').split('.').map((x) => Number.parseInt(x, 10));
  if (maj === undefined || Number.isNaN(maj)) return false;
  const m = Number.isNaN(min as number) || min === undefined ? 0 : min;
  if (maj === 20) return m >= 17;
  if (maj === 22) return m >= 6;
  return maj === 24;
}

export async function doctor(args: ParsedArgs): Promise<number> {
  const checks: Check[] = [];





  const engine = engineVersion();
  checks.push(
    !engine.available
      ? {
          name: 'container engine',
          status: 'fail',
          detail: 'the container engine is not reachable',
          remediation:
            'Install Docker 26+ and make sure your user can reach the socket ' +
            '(`docker version` should work without sudo).',
        }
      : engineMeetsMinimum(engine.version)
        ? { name: 'container engine', status: 'ok', detail: `engine ${engine.version}` }
        : {
            name: 'container engine',
            status: 'fail',
            detail: `engine ${engine.version || '(unreadable version)'} — this build needs ${MIN_ENGINE_MAJOR}+`,
            remediation:
              `\`baton agent create\` refuses engines below ${MIN_ENGINE_MAJOR}: a node's plugin tree is a ` +
              'subpath of its data volume, which the engine mounts from that release. Upgrade Docker.',
          },
  );

  const compose = composeVersion();
  checks.push(
    compose.available
      ? { name: 'compose', status: 'ok', detail: `v${compose.version}` }
      : {
          name: 'compose',
          status: 'fail',
          detail: 'Docker Compose v2 is not available',
          remediation: 'Install the Compose v2 plugin; the v1 `docker-compose` script is not used.',
        },
  );

















  const dn = driverNetwork(args);
  checks.push({
    name: 'driver network',
    status: 'ok',
    detail: `${dn.name} (${dn.source})`,
  });









  const lx = hostLxcfs();
  checks.push({
    name: 'lxcfs',
    status: lx.usable === true ? 'ok' : 'warn',
    detail:
      lx.usable === undefined
        ? `could not tell — ${lx.detail}`
        : lx.detail,
    remediation:
      lx.usable === true
        ? undefined
        : 'containers will read this machine /proc, not their own limits; cgroup files stay accurate either way',
  });








  const node = process.versions.node;
  checks.push(
    nodeSupported(node)
      ? { name: 'node', status: 'ok', detail: `v${node}` }
      : {
          name: 'node',
          status: 'warn',
          detail: `v${node} is outside the supported range (${NODE_RANGE})`,




          remediation:
            'Install Node.js 20.17+, 22.6+ or 24. On this Node every command except `baton connector` works; ' +
            '`baton connector` will fail to start (it needs node:stream duplexPair).',
        },
  );

  for (const port of [8443, 8444]) {
    const free = await portFree(port);
    checks.push(
      free
        ? { name: `port ${port}`, status: 'ok', detail: 'free' }
        : {
            name: `port ${port}`,
            status: 'warn',
            detail: 'in use',
            remediation:
              `Something already listens on ${port}. That is expected if BATON is running; ` +
              'otherwise pick another port with --listen-address.',
          },
    );
  }

  const adminDir = args.global.adminDir;
  if (adminDir && existsSync(adminDir)) {
    const keyPath = `${adminDir}/admin.key`;
    if (existsSync(keyPath)) {
      const mode = statSync(keyPath).mode & 0o777;
      checks.push(
        mode === 0o600
          ? { name: 'admin key', status: 'ok', detail: 'mode 0600' }
          : {
              name: 'admin key',
              status: 'fail',
              detail: `mode ${mode.toString(8)} — readable by others`,
              remediation: `Run: chmod 600 ${keyPath}`,
            },
      );
    }
  }

  if (args.global.output === 'json') {
    process.stdout.write(json({ checks }) + '\n');
  } else {
    process.stdout.write(
      table(
        checks,
        [
          { header: 'check', get: (c) => c.name },
          { header: 'status', get: (c) => c.status },
          { header: 'detail', get: (c) => c.detail },
        ],
        'nothing to check',
      ) + '\n',
    );
    const actionable = checks.filter((c) => c.status !== 'ok' && c.remediation);
    if (actionable.length > 0) {
      process.stdout.write('\n');
      for (const c of actionable) process.stdout.write(`  ${c.name}: ${c.remediation}\n`);
    }
  }

  return checks.some((c) => c.status === 'fail') ? ExitCode.PRECONDITION : ExitCode.OK;
}

function portFree(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const socket = createConnection({ port, host: '127.0.0.1' });
    const done = (free: boolean) => {
      socket.destroy();
      resolve(free);
    };
    socket.setTimeout(400);
    socket.on('connect', () => done(false));
    socket.on('timeout', () => done(true));
    socket.on('error', () => done(true));
  });
}













export async function lifecycle(
  args: ParsedArgs,
  verb: 'start' | 'stop' | 'restart',
): Promise<number> {
  const role = parseRole(flagString(args, 'role'));
  const selected = selectManifests(args.global.dataDir, args.positionals[1], role);
  const targets = verb === 'stop' ? [...selected].reverse() : selected;

  if (targets.length === 0) {
    throw preconditionError(
      `no ${role} nodes have been created`,
      'Drop --role to act on every node, or create one with `baton agent create --name <n>`.',
    );
  }

  const verbArgs = verb === 'start' ? ['up', '-d'] : verb === 'stop' ? ['stop'] : ['restart'];

  if (args.global.dryRun) {
    for (const m of targets) {
      process.stdout.write(`would run: docker compose -f ${m.path} ${verbArgs.join(' ')}\n`);
    }
    return ExitCode.OK;
  }

  const results: { node: string; role: string; ok: boolean }[] = [];
  for (const m of targets) {
    const ok = projectVerb(m.path, verbArgs, { quiet: args.global.output === 'json' });
    results.push({ node: m.node, role: m.role, ok });
  }

  const failed = results.filter((r) => !r.ok);

  if (args.global.output === 'json') {
    process.stdout.write(json({ verb, results }) + '\n');
  } else if (failed.length > 0) {
    process.stderr.write(
      `\n${verb} failed for: ${failed.map((f) => f.node).join(', ')}\n` +
        'The compose output above says why for each.\n',
    );
  }

  if (failed.length === 0) return ExitCode.OK;
  return failed.length === results.length ? ExitCode.INTERNAL : ExitCode.PARTIAL;
}




















export async function rebuild(args: ParsedArgs): Promise<number> {
  const role = parseRole(flagString(args, 'role'));
  const targets = selectManifests(args.global.dataDir, args.positionals[1], role);

  if (targets.length === 0) {
    throw preconditionError(
      `no ${role} nodes have been created`,
      'Drop --role to rebuild every node, or create one with `baton agent create --name <n>`.',
    );
  }



  const verbArgs = ['up', '-d', '--force-recreate'];

  if (args.global.dryRun) {
    for (const m of targets) {
      process.stdout.write(`would run: docker compose -f ${m.path} ${verbArgs.join(' ')}\n`);
    }
    return ExitCode.OK;
  }

  const results: { node: string; role: string; ok: boolean }[] = [];
  for (const m of targets) {
    const ok = projectVerb(m.path, verbArgs, { quiet: args.global.output === 'json' });
    results.push({ node: m.node, role: m.role, ok });
  }

  const failed = results.filter((r) => !r.ok);
  if (args.global.output === 'json') {
    process.stdout.write(json({ verb: 'rebuild', results }) + '\n');
  } else {
    process.stdout.write(
      failed.length === 0
        ? `rebuilt ${results.length} node(s); workspaces and identities kept\n`
        : `\nrebuild failed for: ${failed.map((f) => f.node).join(', ')}\n`,
    );
  }

  if (failed.length === 0) return ExitCode.OK;
  return failed.length === results.length ? ExitCode.INTERNAL : ExitCode.PARTIAL;
}



















export function destroyDataLine(
  volumes: string[] | undefined,
  keepData: boolean,
  manifestPath: string,
): string {
  if (keepData) return 'data       kept (--keep-data)';
  if (volumes === undefined) {
    return `data       unknown — ${manifestPath} did not parse, so what will be deleted could not be read`;
  }
  if (volumes.length === 0) return 'data       no volumes in this project';
  const noun = volumes.length === 1 ? 'volume' : `${volumes.length} volumes`;
  return `data       ${noun} ${volumes.join(', ')} — deleted`;
}




























export interface DestroyOptions {
  identityRevoked?: string;
}






export function identityStep(
  role: string,
  node: string,
  opts: DestroyOptions,
): { revoke: false; identity: string } | { revoke: true } {
  if (role === 'master') return { revoke: false, identity: 'not applicable' };
  if (opts.identityRevoked) {
    return { revoke: false, identity: `revoked earlier (node ${opts.identityRevoked}); ${node} not revoked again by name` };
  }
  return { revoke: true };
}

export async function destroy(args: ParsedArgs, newClient: () => Client, opts: DestroyOptions = {}): Promise<number> {
  const name = args.positionals[1];
  if (!name) {
    throw usageError('destroy needs a node name', 'Try `baton destroy agent01 --yes`.');
  }

  const role = parseRole(flagString(args, 'role'));

















  let m: ReturnType<typeof resolveManifest>;
  try {
    m = resolveManifest(args.global.dataDir, name, role);
  } catch (err) {
    const containers = ['agent', 'master', 'standby']
      .map((r) => `baton-${r}-${name}`)
      .filter((c) => containerExists(c));
    if (containers.length === 0) throw err;












    const orphanVolumes = containers.map((c) => ({ c, v: volumesFor(c) }));
    const orphanLines = [
      `container  ${containers.join(', ')}`,
      ...orphanVolumes.map(({ c, v }) =>
        v === undefined
          ? `data       ${c}: unknown — the engine did not answer, so what will be deleted could not be read`
          : v.length === 0
            ? `data       ${c}: no volumes found`
            : `data       ${c}: ${v.join(', ')} — deleted`,
      ),
      'identity   not touched — this server has no record of which one it was.',
    ];

    if (args.global.dryRun) {








      process.stdout.write(
        args.global.output === 'json'
          ? json({
              node: name,
              containers,
              volumes: Object.fromEntries(orphanVolumes.map(({ c, v }) => [c, v ?? null])),
              identity: 'not touched (no record here)',
              dry_run: true,
            }) + '\n'
          : `would destroy ${name} (no record on this server — only containers):\n  ` +
              orphanLines.join('\n  ') + '\n',
      );
      return ExitCode.OK;
    }

    if (!args.global.yes) {
      throw usageError(
        `${name} is running here with no record on this server — only the container is left`,
        `\`baton destroy ${name} --yes\` removes it and its volumes. Its identity, if it ` +
          'had one, stays as it is: nothing here knows which registry row it was.',
      );
    }
    for (const c of containers) {
      removeContainer(c);
      removeVolumesFor(c);
    }
    process.stdout.write(
      args.global.output === 'json'
        ? json({ node: name, removed: containers, identity: 'not touched (no record here)' }) + '\n'
        : `\n  removed ${containers.join(', ')} and its volumes.\n` +
            '  identity   not touched — this server had no record of which one it was.\n',
    );
    return ExitCode.OK;
  }
  const keepData = flagBool(args, 'keep-data');











  const volumesDeclared = manifestVolumes(m.path);





  const sizedVolume = volumePaths(args.global.dataDir ?? defaultDataDir(), m.node);
  const hasSizedVolume = existsSync(sizedVolume.img);

  const willDelete = [
    `container  ${m.container}`,
    destroyDataLine(volumesDeclared, keepData, m.path),
    ...(hasSizedVolume
      ? [
          keepData
            ? `volume     ${sizedVolume.img} — unmounted, kept`
            : `volume     ${sizedVolume.img} — unmounted and deleted`,
        ]
      : []),
    `manifest   ${m.path}`,
    m.role === 'master' ? 'identity   n/a (a control plane is not enrolled)' : `identity   revoked on the control plane`,
  ];

  if (args.global.dryRun) {


    process.stdout.write(
      args.global.output === 'json'
        ? json({
            node: m.node,
            role: m.role,
            container: m.container,
            volumes: keepData ? null : (volumesDeclared ?? null),
            keep_data: keepData,
            manifest: m.path,
            identity: m.role === 'master' ? 'not applicable' : 'revoked on the control plane',
            dry_run: true,
          }) + '\n'
        : `would destroy ${m.role} ${m.node}:\n  ` + willDelete.join('\n  ') + '\n',
    );
    return ExitCode.OK;
  }

  if (!args.global.yes) {
    throw usageError(
      `destroying ${m.role} ${m.node} removes:\n  ` + willDelete.join('\n  '),
      `Add --yes if that is what you want, or keep the volume with ` +
        `\`baton destroy ${name} --keep-data --yes\`.`,
    );
  }





































  let identity = 'not applicable';



  const step = identityStep(m.role, m.node, opts);
  if (!step.revoke) {
    identity = step.identity;
  } else {
    try {
      await newClient().post(`/nodes/${m.node}/revoke`, { reason: 'destroyed by operator' });
      identity = 'revoked';
    } catch (err) {
      const code = (err as { code?: string }).code ?? '';
      const exit = (err as { exitCode?: number }).exitCode;
      if (exit === ExitCode.PRECONDITION || code === 'NODE_NOT_FOUND' || code === 'NOT_FOUND') {





















        const joined = registerJoinedNetwork(args, m.node);








        identity = joined
          ? `not revoked — this control plane never registered it, and the register says it joined ${joined}. ` +
            `The certificate is live there until an operator of ${joined} revokes it; this machine holds no ` +
            `operator identity for that network and cannot do it for you.`
          : 'none — this control plane has no registration for it';
      } else if (exit === ExitCode.UNREACHABLE) {
        identity = `not revoked: the control plane did not answer (${err instanceof Error ? err.message : String(err)})`;
      } else {
        identity = `not revoked: ${err instanceof Error ? err.message : String(err)}`;
      }
    }
  }

  if (!projectDown(m.path, { volumes: !keepData, quiet: args.global.output === 'json' })) {
    throw new BatonError({
      code: 'DESTROY_FAILED',
      message: `could not remove ${m.container}`,
      remediation: `The manifest is still at ${m.path}; re-run once Docker is reachable.`,
      exitCode: ExitCode.INTERNAL,
      details: { identity },
    });
  }




  rmSync(m.path, { force: true });





  const dataDir = args.global.dataDir ?? defaultDataDir();
  rmSync(join(dataDir, 'nodes', `${m.node}.json`), { force: true });












  if (hasSizedVolume) {
    const gone = keepData ? unmountVolume(sizedVolume) : removeVolume(sizedVolume);
    if (!gone.ok) {


      process.stderr.write(
        `\x1b[33m==>\x1b[0m ${m.node}: the data volume could not be released (${gone.step}): ` +
          `${gone.detail ?? 'no detail'}. Check \`losetup -a\` for ${sizedVolume.img}.\n`,
      );
    }
  }

  const orphanedIdentity = identity.startsWith('not revoked');










  let policyLine = '';
  let policyJSON: unknown = null;







  if (m.role !== 'master' && !identity.startsWith('none')) {
    try {
      const pol = await newClient().get<{ act_on: string; allow_senders: string[]; allow_networks: string[]; policy_set: boolean }>(
        `/inbox/policy/${encodeURIComponent(m.node)}`,
      );
      policyJSON = pol;
      if (pol.policy_set) {
        policyLine = `  policy   @${m.node} keeps its inbox policy (act_on ${pol.act_on}` +
          (pol.act_on === 'allowlist' ? `, ${pol.allow_senders.length} sender(s), ${pol.allow_networks.length} network(s)` : '') +
          `) — the next node created with this name inherits it; \`baton inbox policy ${m.node} --act-on everyone\` clears it\n`;
      }
    } catch {
      policyJSON = 'unknown';
      policyLine = `  policy   @${m.node}: could not read its inbox policy; if one was set, the next node with this name inherits it\n`;
    }
  }












  const dataDone = destroyDataLine(volumesDeclared, keepData, m.path).replace(/^data\s+/, '');

  if (args.global.output === 'json') {
    process.stdout.write(
      json({
        role: m.role,
        node: m.node,
        identity,
        data: keepData ? 'kept' : 'deleted',


        volumes_deleted: keepData ? null : (volumesDeclared ?? null),



        inbox_policy: policyJSON,
      }) + '\n',
    );
  } else {
    process.stdout.write(
      `\ndestroyed ${m.role} ${m.node}\n` +
        `  identity ${identity}\n` +
        `  data     ${dataDone}\n` +
        policyLine,
    );
  }

  if (orphanedIdentity) {



    process.stderr.write(
      '\nwarning: the local node is gone but its registration may not be. ' +
        `Once the control plane answers, remove it:\n  baton node revoke ${m.node} --yes\n`,
    );
    return ExitCode.PARTIAL;
  }
  return ExitCode.OK;
}









export async function uninstall(args: ParsedArgs): Promise<number> {
  const purge = args.flags.get('purge-data') === true;
  const manifests = listManifests(args.global.dataDir);

  if (manifests.length === 0) {
    process.stdout.write('nothing to uninstall: no nodes were created here.\n');
    return ExitCode.OK;
  }


  const targets = [...manifests].reverse();

  if (!purge) {
    const failed = tearDown(targets, false);
    process.stdout.write(
      '\ncontainers removed; data kept.\n' +
        'To destroy the CA, audit log, and node identities as well:\n' +
        '  baton uninstall --purge-data --confirm-cluster <name>\n',
    );
    return exitFor(failed, targets.length);
  }

  const confirm = flagString(args, 'confirm-cluster');
  if (!confirm) {


    throw usageError(
      '--purge-data destroys the certificate authority, the audit log, and every node identity',
      'If that is what you want, name the cluster: --purge-data --confirm-cluster <name>',
    );
  }

  const failed = tearDown(targets, true);


  for (const m of targets) {
    if (!failed.includes(m.node)) rmSync(m.path, { force: true });
  }
  process.stdout.write(`\npurged cluster ${confirm}.\n`);
  return exitFor(failed, targets.length);
}

function tearDown(targets: { node: string; path: string }[], volumes: boolean): string[] {
  const failed: string[] = [];
  for (const m of targets) {
    if (!projectDown(m.path, { volumes })) failed.push(m.node);
  }
  return failed;
}

function exitFor(failed: string[], total: number): number {
  if (failed.length === 0) return ExitCode.OK;
  process.stderr.write(`\nfailed for: ${failed.join(', ')}\n`);
  return failed.length === total ? ExitCode.INTERNAL : ExitCode.PARTIAL;
}

