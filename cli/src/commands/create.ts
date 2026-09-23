import { Client, defaultDataDir } from '../api/client.js';
// SPDX-License-Identifier: Apache-2.0



















import {
  composeVersion,
  imageEntrypoint,
  imageHasProgram,
  imageIsPresent,
  pullImage,
  containerExists,
  containerNamesOnNetwork,
  copyFileOut,
  copyIn,
  copyOut,
  createInternalNetwork,
  createNetwork,
  engineVersion,
  execCapture,
  hostSecurityOptions,
  initVolumeSubpath,
  connectNetwork,
  networkExists,
  projectRun,
  projectVerb,
  projectUp,
  projectUpSaid,
  readiness,
  recentOutput,
  type Readiness,
  hostLxcfs,
  MIN_ENGINE_MAJOR,
  engineMeetsMinimum,
} from '../runtime/engine.js';
import { chmodSync, chownSync, cpSync, existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { hostname } from 'node:os';
import { setTimeout as sleep } from 'node:timers/promises';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { json } from '../output.js';
import { renderCompose } from '../manifests/compose.js';
import { materializeSeccompProfile } from '../manifests/seccomp.js';
import { canMount, createVolume, volumePaths } from '../runtime/volume.js';
import { cpuCores, loadRuntimeManifest, memoryBytes } from '../runtime/spec.js';
import { WORKSPACE_CONTRACT, BATON_DIR, ROOT_NORMS, contractVerdict, stageWalls } from '../runtime/workspace.js';
import { decorationStateOf, describeDecoration, stageParcels, type DecorationState, type Extension } from '../runtime/extensions.js';
import { stringify as stringifyYAML } from 'yaml';
import { facilitySecretsOf, ledgerFor, parcelsOf, PLUGIN_MANIFEST, PLUGINS_ROOT, PLUGINS_TREE_SUBPATH, PLUGINS_TREE_UID, PROVENANCE_FILE, type ResolvedPlugin } from '../runtime/plugins.js';
import { dataVolumeName } from '../manifests/compose.js';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { loadWorkspaceTemplate, materialise, NODE_DEFAULTS, resolveTemplateRef, resolveTemplateSkills, STOCKED_FILE, type NodeSection, type ResolvedTemplateSkill } from '../runtime/template.js';
import { CONTAINER, parseLabels } from '../runtime/driver.js';
import { containerName, validateNodeName, type ComponentRole } from '../runtime/target.js';
import { listManifests, manifestHostPort } from '../runtime/manifests.js';
import { driverNetwork, flagBool, flagString, type ParsedArgs } from '../args.js';
import { DEFAULT_IMAGE_TAG } from '../runtime/image-pins.js';


const READY_TIMEOUT_MS = 90_000;

interface CreateContext {
  role: ComponentRole;
  name: string;
  dataDir: string;
  composeFile: string;
  container: string;
  version: string;
  network: string;
  json: boolean;
  dryRun: boolean;
}









function secretOverrides(args: ParsedArgs): Map<string, string> {
  const out = new Map<string, string>();
  for (const raw of args.repeated.get('secret') ?? []) {
    const eq = raw.indexOf('=');
    if (eq <= 0) {
      throw usageError(
        `--secret ${raw} is not NAME=path`,
        'Give the secret\'s name from the spec and the host file backing it: ' +
          '--secret GIT_KEY=/etc/baton/secrets/dev/git-key',
      );
    }
    const name = raw.slice(0, eq);
    if (out.has(name)) {
      throw usageError(
        `--secret ${name} was given twice`,
        'One file per secret. Two would mean one of them silently wins.',
      );
    }
    out.set(name, raw.slice(eq + 1));
  }
  return out;
}








function usernsBaseUID(): number | undefined {
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















const NODE_DAEMON = 'baton-agent';







































function assertImageStartsTheDaemon(image: string, dryRun: boolean): void {
  let entrypoint = imageEntrypoint(image);














  if (entrypoint === undefined && dryRun) {
    process.stderr.write(
      `note: could not check whether ${image} starts ${NODE_DAEMON} — it is not on this ` +
        'machine, and --dry-run does not fetch it.\n' +
        '  The real create will check, and will refuse if it does not.\n',
    );
    return;
  }

  if (entrypoint === undefined) {
    pullImage(image);
    entrypoint = imageEntrypoint(image);
  }
  if (entrypoint === undefined) {



    return;
  }
  if (entrypoint.some((part) => part.includes(NODE_DAEMON))) return;

  const declared = entrypoint.length ? entrypoint.join(' ') : '(none)';
  throw preconditionError(
    `${image} does not start the node daemon (its ENTRYPOINT is ${declared})`,
    `An agent-node image runs \`${NODE_DAEMON}\` as PID 1; it is the process that enrolls the ` +
      'node, holds the connection to the control plane, and supervises the runtime your spec ' +
      'declares. Your runtime is started BY it, not instead of it — put your program in ' +
      '`package.command` and build FROM an agent-node image, or copy ' +
      `\`${NODE_DAEMON}\` into yours and make it the ENTRYPOINT. ` +
      'Without this the container runs and nothing joins.',
  );
}

















































function assertCommandExists(image: string, commandPath: string | undefined, dryRun: boolean): void {
  if (!commandPath) return;
  if (!imageIsPresent(image)) {
    if (dryRun) {
      process.stderr.write(
        `note: could not check whether ${image} has ${commandPath} — it is not on this machine, ` +
          'and --dry-run does not fetch it.\n  The real create will check.\n',
      );
      return;
    }
    pullImage(image);
    if (!imageIsPresent(image)) return;
  }
  const has = imageHasProgram(image, commandPath);
  if (has === undefined) {


    process.stderr.write(
      `note: could not check whether ${image} has ${commandPath} — it has no shell to ask with.\n`,
    );
    return;
  }
  if (has) return;
  throw preconditionError(
    `${image} does not have ${commandPath}, which package.command starts`,
    'That path is the runtime itself: the daemon execs it, and a miss is not a slow failure but an ' +
      'immediate one — the node enrols, the runtime exits, and the office is empty. Point command at a ' +
      'program this image has (its shell is usually /bin/sh), or install it in package.install first.',
  );
}

function assertInstallProgramsExist(image: string, programs: string[], dryRun: boolean): void {
  if (programs.length === 0) return;

  if (!imageIsPresent(image)) {
    if (dryRun) {
      process.stderr.write(
        `note: could not check whether ${image} has ${[...new Set(programs)].join(', ')} — ` +
          'it is not on this machine, and --dry-run does not fetch it.\n' +
          '  The real create will check, and will refuse if it does not.\n',
      );
      return;
    }
    pullImage(image);
    if (!imageIsPresent(image)) return;
  }

  const answers = [...new Set(programs)].map((p) => ({ p, has: imageHasProgram(image, p) }));
  const unknown = answers.filter((a) => a.has === undefined).map((a) => a.p);
  if (unknown.length) {



    process.stderr.write(
      `note: could not check whether ${image} has ${unknown.join(', ')} — it has no shell to ask with.\n` +
        '  package.install will still run there, and a failure is fatal to the runtime.\n',
    );
  }
  const missing = answers.filter((a) => a.has === false).map((a) => a.p);
  if (missing.length === 0) return;

  throw preconditionError(
    `${image} does not have ${missing.join(', ')}, which package.install needs`,
    'The install list runs INSIDE this image, before your runtime starts, and a failure there ' +
      'is fatal to the runtime — the node would enroll, hold its connection and report healthy ' +
      'while the thing you wanted never ran. Either name an image that already has the program ' +
      '(`package.image`), or drop the install entry. Baking the tool into the image is the ' +
      'better answer anyway: an install resolved at container start is not reproducible, and needs ' +
      'the network at creation time.',
  );
}

export function assertSecretsAreReadableInside(
  mounts: { source: string; target: string }[],
  base: number | undefined = usernsBaseUID(),
): void {
  if (mounts.length === 0) return;
  if (base === undefined) return;

  for (const m of mounts) {






    if (!existsSync(m.source)) continue;
    const owner = statSync(m.source).uid;
    if (owner === base) continue;
    throw preconditionError(
      `${m.source} is owned by uid ${owner}, and this host remaps container root to ` +
        `uid ${base} — the node would not be able to read it`,
      `chown ${base} ${m.source} && chmod 600 ${m.source}\n` +
        '  The file stays unreadable to every account on this host and becomes ' +
        'readable to the node, which is the only pair of properties that works ' +
        'under user-namespace remapping.',
    );
  }
}

export async function waitHealthy(container: string, node: string, label: string): Promise<void> {
  const deadline = Date.now() + READY_TIMEOUT_MS;
  let last: Readiness = 'unknown';
  while (Date.now() < deadline) {
    last = readiness(container);
    if (last === 'healthy' || last === 'running') return;
    if (last === 'missing') break;
    await sleep(1000);
  }



  const logs = recentOutput(container, 20);
  throw new BatonError({
    code: 'NOT_READY',
    message: `${label} did not become ready (last state: ${last})`,
    remediation: `Look at what it wrote: baton logs ${node}`,
    exitCode: ExitCode.PRECONDITION,
    details: { recent_logs: logs.split('\n').slice(-8) },
  });
}








function ensureNetwork(name: string): void {
  if (networkExists(name)) return;
  const res = createNetwork(name);
  if (!res.ok) {
    throw new BatonError({
      code: 'NETWORK_FAILED',
      message: `could not create the ${name} network: ${(res.stderr ?? '').trim()}`,
      remediation: 'Check `docker network ls` for a conflicting network.',
      exitCode: ExitCode.INTERNAL,
    });
  }
}


function noEgressNetwork(cluster: string): string {
  return `${cluster}-noegress`;
}














function networkForEgress(cluster: string, egress: string | undefined): string {
  if (egress !== 'deny') return cluster;

  const isolated = noEgressNetwork(cluster);
  if (!networkExists(isolated)) {
    const res = createInternalNetwork(isolated);
    if (!res.ok) {
      throw new BatonError({
        code: 'NETWORK_FAILED',
        message: `could not create the ${isolated} network: ${(res.stderr ?? '').trim()}`,
        remediation: 'Check `docker network ls` for a conflicting network.',
        exitCode: ExitCode.INTERNAL,
      });
    }
  }




  const controlPlane = [
    ...containerNamesOnNetwork('baton-master-', cluster),
    ...containerNamesOnNetwork('baton-standby-', cluster),
  ];
  if (controlPlane.length === 0) {
    throw new BatonError({
      code: 'NO_CONTROL_PLANE',
      message:
        `this spec asks for execution.network.egress: deny, and no control plane was found on ${cluster}`,
      remediation:
        'A node with egress denied reaches the control plane and nothing else, so the ' +
        'control plane has to be here to be reached. Start it first, or point --network ' +
        'at the cluster it is on.',
      exitCode: ExitCode.PRECONDITION,
    });
  }
  for (const c of controlPlane) {



    const res = connectNetwork(isolated, c);
    if (!res.ok && !(res.stderr ?? '').includes('already exists')) {
      throw new BatonError({
        code: 'NETWORK_FAILED',
        message: `could not put ${c} on ${isolated}: ${(res.stderr ?? '').trim()}`,
        remediation: 'A node with egress denied cannot reach a control plane that is not there.',
        exitCode: ExitCode.INTERNAL,
      });
    }
  }
  return isolated;
}


































export function resolveLoopbackMaster(
  dataDir: string,
  candidates: string[],
  port: string,
): { container: string } | { remediation: string } {
  const published = new Map<string, string | null>();
  for (const m of listManifests(dataDir)) {
    if (m.role !== 'master') continue;
    published.set(m.container, manifestHostPort(m.path));
  }
  const matched = port ? candidates.filter((c) => published.get(c) === port) : [];
  if (matched.length === 1) return { container: matched[0]! };
  return {
    remediation:
      'A loopback address does not say which one: from inside the office it is the office itself, so it ' +
      'has to be rewritten, and this machine\'s records do not tie the port you dialled to exactly one of ' +
      'them. Two ways to say it unambiguously:\n' +
      '    --network https://<this machine\'s LAN or public address>:<port>   an address the office can dial ' +
      'and this machine can verify (`baton network show` lists what a network publishes; `baton network ' +
      'publish --endpoint …` records one).\n' +
      '    BATON_DRIVER_NETWORK=<engine network> baton agent create …        open the office on the engine ' +
      'network of the control plane it should join, where it is the only one.\n' +
      '  Not the master\'s container name: that address resolves only inside the engine network, and ' +
      '--network is verified from here first.',
  };
}






export function masterURLForContainer(masterURL: string, network: string, dataDir: string): string {
  let parsed: URL;
  try {
    parsed = new URL(masterURL);
  } catch {
    throw usageError(
      `--network ${masterURL} is not a URL`,
      'Use a full URL, e.g. --network https://127.0.0.1:8443.',
    );
  }

  const loopback = parsed.hostname === '127.0.0.1' || parsed.hostname === 'localhost' ||
    parsed.hostname === '::1';
  if (!loopback) return masterURL;

  const candidates = containerNamesOnNetwork('baton-master-', network);

  if (candidates.length === 0) {












    const promotable = listManifests(dataDir).length > 0;
    throw preconditionError(
      `no control plane is running on the ${network} network`,
      'The token was issued by a control plane; point at it with --network <url>' +
        (promotable
          ? ', or promote one of this machine\'s nodes with `baton setup master`.'
          : '. This machine has no node to promote, so there is nothing here to point at yet.'),
    );
  }
  if (candidates.length > 1) {
    const decided = resolveLoopbackMaster(dataDir, candidates, parsed.port);
    if ('container' in decided) return `https://${decided.container}:8443`;
    throw new BatonError({
      code: 'AMBIGUOUS',
      message: `several control planes are on the ${network} network: ${candidates.join(', ')}`,
      remediation: decided.remediation,
      exitCode: ExitCode.CONFLICT,
    });
  }



  return `https://${candidates[0]}:8443`;
}


function preflight(): void {
  const engine = engineVersion();
  if (!engine.available) {
    throw preconditionError(
      'the Docker engine is not reachable',
      'Install Docker 26+ and make sure `docker version` works without sudo.',
    );
  }






  if (!engineMeetsMinimum(engine.version)) {
    throw preconditionError(
      `the Docker engine is ${engine.version || 'of an unreadable version'}; this build needs ${MIN_ENGINE_MAJOR}+`,
      `A node's plugin tree is a subpath of its data volume (\`volume-subpath\`), which the engine mounts from ${MIN_ENGINE_MAJOR}. Upgrade the engine; nothing was created.`,
    );
  }
  if (!composeVersion().available) {
    throw preconditionError(
      'Docker Compose v2 is not available',
      'Install the Compose v2 plugin. The v1 `docker-compose` script is not used.',
    );
  }
}

function writeManifest(ctx: CreateContext, body: string): void {
  mkdirSync(join(ctx.dataDir, 'compose'), { recursive: true });
  writeFileSync(ctx.composeFile, body, { mode: 0o644 });
}





















export function portAlreadyTaken(said: string): string | undefined {


  const modern = /failed to bind host port (\S+?)\/tcp: address already in use/.exec(said);
  if (modern) return modern[1];


  const legacy = /Bind for (\S+?) failed: port is already allocated/.exec(said);
  return legacy?.[1];
}

function composeUp(ctx: CreateContext): void {
  const { ok, said } = projectUpSaid(ctx.composeFile, { quiet: ctx.json });
  if (ok) return;













  const port = portAlreadyTaken(said);
  if (port) {
    throw preconditionError(
      `${port} is already in use, so ${ctx.container} could not start`,
      `Something else on this machine is listening there — on a shared host it may be another tenant's ` +
        `control plane (\`docker ps --format '{{.Names}}\\t{{.Ports}}' | grep ${port.split(':').pop()}\`). ` +
        'Choose another with `--port <n>`. The generated manifest is not the problem: ' +
        `${ctx.composeFile}`,
    );
  }
  throw new BatonError({
    code: 'COMPOSE_FAILED',
    message: `could not start ${ctx.container}`,



    remediation:
      (said.trim() ? `The engine said: ${said.trim().split('\n').slice(-3).join(' ')}\n  ` : '') +
      `Inspect the generated manifest: ${ctx.composeFile}`,
    exitCode: ExitCode.INTERNAL,
  });
}

























function refuseReadOnlyWorkspace(name: string, mountPath: string | undefined, readOnly: boolean | undefined): void {
  if (!mountPath || !readOnly) return;
  throw new BatonError({
    code: 'PRECONDITION',
    message: `${name} declares a read-only workspace, which cannot hold the workspace contract`,
    remediation:
      `${WORKSPACE_CONTRACT} requires ${BATON_DIR}/ to exist inside the workspace, and reserves ` +
      'one path in it for the runtime to write. A workspace mounted read-only can hold neither. ' +
      'Remove `readOnly: true` from the template\'s adapter.workspace, or use a template without a workspace.',
    exitCode: ExitCode.PRECONDITION,
  });
}












export function refuseParcelsWithoutAWorkspace(name: string, mountPath: string | undefined, extensions: Extension[]): void {
  if (mountPath || extensions.length === 0) return;
  throw new BatonError({
    code: 'PRECONDITION',
    message: `${name} declares ${extensions.length} extension(s) but has no workspace to put them in`,
    remediation:
      'A parcel is written into the workspace, so a template with `extensions:` needs a runtime that ' +
      'declares `adapter.workspace.mountPath`. Either add one, or remove the extensions.',
    exitCode: ExitCode.PRECONDITION,
  });
}

























function stagePlugins(ctx: CreateContext, plugins: ResolvedPlugin[], dataDir: string): string[] {
  if (plugins.length === 0) return [];
  const staging = mkdtempSync(join(tmpdir(), 'baton-plugins-'));
  try {
    const named: string[] = [];
    for (const p of plugins) {
      const dest = join(staging, p.id);
      if (p.dir) {
        cpSync(p.dir, dest, { recursive: true });
        named.push(p.id);
      } else {
        mkdirSync(dest, { recursive: true });
        writeFileSync(join(dest, PLUGIN_MANIFEST), stringifyYAML(p.manifest));
      }
    }
    writeFileSync(join(staging, PROVENANCE_FILE), ledgerFor(named, dataDir));
    const put = copyIn(staging, ctx.container, PLUGINS_ROOT);
    if (!put.ok) {
      throw new BatonError({
        code: 'INTERNAL',
        message: `could not put ${plugins.length} facilities into ${ctx.container}:${PLUGINS_ROOT}`,
        remediation:
          `None of ${plugins.map((p) => p.id).join(', ')} was written, so this node would start with no ` +
          `manual, no skills and no facility state. ` +
          ((put.stderr ?? '').trim() || `Check that ${PLUGINS_ROOT} exists in the image.`),
        exitCode: ExitCode.INTERNAL,
      });
    }
    return plugins.map((p) => p.id);
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}



























function stageTemplateSkills(
  ctx: CreateContext,
  skills: ResolvedTemplateSkill[],
  mountPath: string | undefined,
  templateName: string,
): string[] {
  if (skills.length === 0) return [];
  if (!mountPath) {




    throw preconditionError(
      `${templateName} stocks ${skills.length} skill(s) but its runtime declares no adapter.skills.mountPath`,
      'Where a skill goes is a property of the runtime: the runtime spec must state adapter.skills.mountPath, and there is no default. ' +
        'Add `adapter.skills.mountPath` to the runtime, or take `skills:` out of the template.',
    );
  }
  const staging = mkdtempSync(join(tmpdir(), 'baton-skills-'));
  try {
    for (const sk of skills) cpSync(sk.dir, join(staging, sk.name), { recursive: true });


    writeFileSync(
      join(staging, STOCKED_FILE),
      skills.map((sk) => `${sk.name}  ${templateName}`).join('\n') + '\n',
    );
    const put = copyIn(staging, ctx.container, mountPath);
    if (!put.ok) {
      throw new BatonError({
        code: 'INTERNAL',
        message: `could not put ${skills.length} skill(s) into ${ctx.container}:${mountPath}`,
        remediation:
          `None of ${skills.map((sk) => sk.name).join(', ')} was written, so this node would come up without ` +
          `the procedures ${templateName} stocks it with. ` +
          ((put.stderr ?? '').trim() || `Check that ${mountPath} exists in the image.`),
        exitCode: ExitCode.INTERNAL,
      });
    }
    return skills.map((sk) => sk.name);
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

function buildWalls(
  ctx: CreateContext,
  mountPath: string | undefined,
  extensions: Extension[],
): { built: string[]; parcels: string[]; states: Map<string, DecorationState> } | undefined {


  if (!mountPath) return undefined;






  const found = mkdtempSync(join(tmpdir(), 'baton-contract-'));




  let preV2 = true;
  try {
    const got = copyFileOut(ctx.container, `${mountPath}/${BATON_DIR}/contract.json`, join(found, 'contract.json'));
    if (got.ok) {
      let version: unknown;
      try {
        version = (JSON.parse(readFileSync(join(found, 'contract.json'), 'utf8')) as { contract?: unknown }).contract;
      } catch {
        version = undefined;
      }
      const verdict = contractVerdict(version);
      if (verdict === 'newer' || verdict === 'unreadable') {
        throw new BatonError({
          code: 'CONFLICT',
          message: `the volume behind ${ctx.name} already carries a workspace contract this build cannot honour (${verdict})`,
          remediation:
            `It says ${typeof version === 'string' ? version : 'nothing readable'}; this build writes ` +
            `${WORKSPACE_CONTRACT}. Rewriting it would discard whatever that version recorded, so this ` +
            'refuses instead. Use a newer baton, or create the node under a different name.',
          exitCode: ExitCode.CONFLICT,
        });
      }
      preV2 = verdict === 'stale';
    }
  } finally {
    rmSync(found, { recursive: true, force: true });
  }






  if (preV2) {
    for (const norm of ROOT_NORMS) {
      const probe = mkdtempSync(join(tmpdir(), 'baton-norm-'));
      try {
        const asDir = copyOut(ctx.container, `${mountPath}/${norm}`, probe);
        if (asDir.ok) {
          if (readdirSync(probe).length === 0) continue;
          throw new BatonError({
            code: 'CONFLICT',
            message: `the volume behind ${ctx.name} already has its own ${norm}/ from before the sharing boundary existed`,
            remediation:
              `${norm}/ becomes the sharing boundary under ${WORKSPACE_CONTRACT}: what is in it is read by name by ` +
              'the publish verb, the panel and the network shared folder. Adopting its current content would mark it ' +
              '"willing to give out" without anyone saying so. Rename it inside the workspace, then create again.',
            exitCode: ExitCode.CONFLICT,
          });
        }
        const asFile = copyFileOut(ctx.container, `${mountPath}/${norm}`, join(probe, 'squatter'));
        if (asFile.ok) {
          throw new BatonError({
            code: 'CONFLICT',
            message: `the volume behind ${ctx.name} has a FILE named ${norm}, and ${norm}/ is a root norm this contract requires`,
            remediation: `Rename the file inside the workspace, then create again. Deleting it for you is not this command's to do: the file is the workspace owner's, so it is refused with a remedy, never deleted or overwritten.`,
            exitCode: ExitCode.CONFLICT,
          });
        }

      } finally {
        rmSync(probe, { recursive: true, force: true });
      }
    }
  }

  const staging = mkdtempSync(join(tmpdir(), 'baton-walls-'));
  try {
    const built = stageWalls(staging);




    const parcels = stageParcels(staging, extensions);





    const seen = mkdtempSync(join(tmpdir(), 'baton-seen-'));
    const seenExt = join(seen, BATON_DIR, 'portable', 'ext');
    mkdirSync(seenExt, { recursive: true });
    copyOut(ctx.container, `${mountPath}/${BATON_DIR}/portable/ext`, seenExt);
    const states = new Map(extensions.map((e) => [e.provider, decorationStateOf(seen, e.provider, extensions)]));
    rmSync(seen, { recursive: true, force: true });

    const put = copyIn(staging, ctx.container, mountPath);
    if (!put.ok) {
      throw new BatonError({
        code: 'INTERNAL',
        message: `could not build the workspace structure in ${ctx.container}:${mountPath}`,
        remediation:
          `Nothing of ${[...built, ...parcels].join(', ')} was written. ` +
          ((put.stderr ?? '').trim() || `Check that ${mountPath} is a writable mount in the container.`),
        exitCode: ExitCode.INTERNAL,
      });
    }


    return { built, parcels, states };
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

function note(ctx: CreateContext, text: string): void {
  if (!ctx.json) process.stderr.write(`\x1b[36m==>\x1b[0m ${text}\n`);
}


















export async function nodeCreate(args: ParsedArgs, opts: { operationId?: string } = {}): Promise<number> {




  const nodeID = randomUUID();
  const name = args.positionals[2] ?? `node-${nodeID.slice(0, 8)}`;
  validateNodeName(name);

  const dataDir = args.global.dataDir ?? defaultDataDir();


  const registerDir = join(dataDir, 'nodes');
  const statePath = join(registerDir, `${name}.json`);
  if (existsSync(statePath)) {
    throw new BatonError({
      code: 'CONFLICT',
      message: `this server already has a node called ${name}`,
      remediation: 'Pick another name, or omit it and one will be generated.',
      exitCode: ExitCode.CONFLICT,
    });
  }


  const driver = CONTAINER;
  preflight();

  const ctx: CreateContext = {
    role: 'agent',
    name,
    dataDir,
    composeFile: join(dataDir, 'compose', `agent-${name}.yml`),
    container: `baton-agent-${name}`,
    version: flagString(args, 'version') ?? DEFAULT_IMAGE_TAG,
    network: driverNetwork(args).name,
    json: args.global.output === 'json',
    dryRun: args.global.dryRun,
  };








  if (flagString(args, 'runtime')) {
    throw usageError(
      '--runtime is gone: a spec file is a Workspace Template, named by --template',
      'Give --template <file> (a WorkspaceTemplate document; a bare AgentRuntime is refused, not wrapped) ' +
        'or --template <name> for one in <data-dir>/templates/.',
    );
  }




  const templateRef = flagString(args, 'template');
  const owner = flagString(args, 'owner');
  if (owner) validateNodeName(owner);

















  const secrets = secretOverrides(args);
  if (secrets.size > 0 && !templateRef) {
    throw usageError(
      '--secret needs --template',
      'A secret is declared by the template\'s runtime section. Without one there is nothing declaring ' +
        'where it would mount.',
    );
  }




  const nodeTemplate = templateRef ? loadWorkspaceTemplate(resolveTemplateRef(templateRef, dataDir), dataDir) : undefined;
  const runtimeFile = join(dataDir, 'compose', `agent-${name}.runtime.yaml`);
  const templateCopy = join(dataDir, 'compose', `agent-${name}.template.yaml`);












  const runtime = nodeTemplate?.runtime
    ? loadRuntimeManifest(nodeTemplate.runtime, runtimeFile, name, secrets, facilitySecretsOf(nodeTemplate.plugins))
    : undefined;







  refuseReadOnlyWorkspace(name, runtime?.workspaceMountPath, runtime?.workspaceReadOnly);
  refuseParcelsWithoutAWorkspace(name, runtime?.workspaceMountPath, parcelsOf(nodeTemplate?.plugins ?? []));

  if (nodeTemplate && runtime) {
    const capCPU = cpuCores(nodeTemplate.node.vcpu, nodeTemplate.path);
    const capMem = memoryBytes(nodeTemplate.node.memory, nodeTemplate.path);
    if (capCPU !== undefined && runtime.cpu !== undefined && Number(runtime.cpu) > Number(capCPU)) {
      throw usageError(
        `${nodeTemplate.name}: the runtime asks for ${runtime.cpu} CPU but node.vcpu caps the office at ${nodeTemplate.node.vcpu}`,
        'The runtime section is the request, the node section is the limit; the request has to fit. Raise node.vcpu or lower execution.resources.cpu.',
      );
    }
    if (capMem !== undefined && runtime.memory !== undefined && BigInt(runtime.memory) > BigInt(capMem)) {
      throw usageError(
        `${nodeTemplate.name}: the runtime asks for more memory than node.memory (${nodeTemplate.node.memory}) allows`,
        'The runtime section is the request, the node section is the limit; the request has to fit.',
      );
    }
  }






































  let dataVolumePath: string | undefined;
  if (nodeTemplate?.node.volumeSize) {
    const size = memoryBytes(nodeTemplate.node.volumeSize, nodeTemplate.path, 'node.volumeSize');



    if (!ctx.dryRun && !canMount()) {
      throw preconditionError(
        `${nodeTemplate.name}: node.volumeSize is ${nodeTemplate.node.volumeSize}, and executing it needs root`,
        'A sized volume is a loop-mounted image (mount, losetup), which needs CAP_SYS_ADMIN, so run this as root. ' +
          'Leaving node.volumeSize out of the template does not avoid it: an unwritten size is the default, ' +
          `${NODE_DEFAULTS.volumeSize}. A size is always executed or refused, never quietly skipped.`,
      );
    }
    const paths = volumePaths(dataDir, name);
    dataVolumePath = paths.mount;
    if (!ctx.dryRun) {





      const base = usernsBaseUID() ?? 0;
      const made = createVolume(paths, size ?? nodeTemplate.node.volumeSize, base);
      if (!made.ok) {
        throw preconditionError(
          `${nodeTemplate.name}: could not create the ${nodeTemplate.node.volumeSize} data volume (${made.step})`,
          `${made.detail ?? 'no detail'} — nothing was started. The image lives at ${paths.img}.`,
        );
      }













      const parts = PLUGINS_TREE_SUBPATH.split('/');
      const owners = [base, base + PLUGINS_TREE_UID];
      for (const [i] of parts.entries()) {
        const dir = join(paths.mount, ...parts.slice(0, i + 1));
        const uid = owners[i] ?? base;
        mkdirSync(dir, { recursive: true });
        try {
          chownSync(dir, uid, uid);
        } catch (err) {
          throw preconditionError(
            `${nodeTemplate.name}: could not own ${dir} to uid ${uid}`,
            `${(err as Error).message} — the node could not write its plugin readout, or its runtime could not read the tree. Nothing was started.`,
          );
        }
      }
      process.stderr.write(
        `\x1b[36m==>\x1b[0m ${nodeTemplate.name}: node.volumeSize ${nodeTemplate.node.volumeSize} enforced — ` +
          `/var/lib/baton is a sized volume (${paths.img}); the container's rootfs is shared and not covered, ` +
          `so \`df /\` inside it still shows the machine's storage layer.\n`,
      );
    } else {
      process.stderr.write(
        `\x1b[36m==>\x1b[0m ${nodeTemplate.name}: would create a ${nodeTemplate.node.volumeSize} volume at ${paths.img} ` +
          'and mount it at /var/lib/baton. Nothing was created.\n',
      );
    }
  }







  if (runtime && !ctx.dryRun) {
    const base = usernsBaseUID();
    for (const m of runtime.secretMounts ?? []) {
      if (existsSync(m.source)) continue;
      mkdirSync(join(m.source, '..'), { recursive: true });
      writeFileSync(m.source, '', { mode: 0o600 });
      if (base !== undefined) { try { chownSync(m.source, base, -1); } catch {  } }
    }
  }
  if (runtime) assertSecretsAreReadableInside(runtime.secretMounts ?? []);
  if (runtime?.image) assertImageStartsTheDaemon(runtime.image, ctx.dryRun);
  if (runtime?.image) assertInstallProgramsExist(runtime.image, runtime.installPrograms ?? [], ctx.dryRun);
  if (runtime?.image) assertCommandExists(runtime.image, runtime.commandPath, ctx.dryRun);








  if (runtime?.driverNetwork) ctx.network = runtime.driverNetwork;







  const nodeNetwork = ctx.dryRun
    ? (runtime?.egress === 'deny' ? noEgressNetwork(ctx.network) : ctx.network)
    : networkForEgress(ctx.network, runtime?.egress);

  const manifest = renderCompose({
    role: 'agent',




    seccompProfile: materializeSeccompProfile(ctx.dataDir),
    name,
    version: ctx.version,
    registry: flagString(args, 'registry'),


    masterURL: undefined,
    labels: flagString(args, 'labels'),
    operationId: opts.operationId,


    allowRemoteShell: flagBool(args, 'allow-remote-shell'),
    network: nodeNetwork,
    runtime,








    lxcfs: hostLxcfs().usable === true,


    dataVolumePath,
  });

  if (ctx.dryRun) {
    process.stdout.write(
      ctx.json
        ? json({ node_id: nodeID, name, roles: ['agent'], owner: owner ?? null, driver, manifest }) + '\n'
        : `# would write ${ctx.composeFile}\n${manifest}\n`,
    );
    return ExitCode.OK;
  }

  mkdirSync(registerDir, { recursive: true });
  chmodSync(dataDir, 0o700);
  if (nodeTemplate) materialise(nodeTemplate, dataDir, name);

  ensureNetwork(ctx.network);
  writeManifest(ctx, manifest);




  note(ctx, `opening ${ctx.container}`);











  if (!projectVerb(ctx.composeFile, ['create'], { quiet: ctx.json })) {
    throw new BatonError({
      code: 'COMPOSE_FAILED',
      message: `could not create ${ctx.container}`,
      remediation: `Inspect the generated manifest: ${ctx.composeFile}`,
      exitCode: ExitCode.INTERNAL,
    });
  }





  if (runtime && !dataVolumePath) {
    const made = initVolumeSubpath(dataVolumeName('agent', name), PLUGINS_TREE_SUBPATH, PLUGINS_TREE_UID, runtime.image ?? '');
    if (!made.ok) {
      throw new BatonError({
        code: 'INTERNAL',
        message: `could not make ${PLUGINS_TREE_SUBPATH} in ${ctx.container}'s data volume`,
        remediation:
          `The node would not start: the engine mounts that path at ${PLUGINS_ROOT} and refuses a subpath that is not there. ` +
          ((made.stderr ?? '').trim() || 'The engine gave no reason.'),
        exitCode: ExitCode.INTERNAL,
      });
    }
  }



  const walls = buildWalls(ctx, runtime?.workspaceMountPath, parcelsOf(nodeTemplate?.plugins ?? []));


  const facilities = stagePlugins(ctx, nodeTemplate?.plugins ?? [], dataDir);



  const stocked = stageTemplateSkills(
    ctx,
    resolveTemplateSkills(nodeTemplate?.skills ?? [], dataDir, nodeTemplate?.name ?? 'template'),
    runtime?.skillsMountPath,
    nodeTemplate?.name ?? 'template',
  );

  if (!projectVerb(ctx.composeFile, ['start'], { quiet: ctx.json })) {
    throw new BatonError({
      code: 'COMPOSE_FAILED',
      message: `${ctx.container} was created but would not start`,
      remediation:
        'The workspace structure is already in its volume, so this is not a wall failure. ' +
        `Inspect the generated manifest: ${ctx.composeFile}`,
      exitCode: ExitCode.INTERNAL,
    });
  }




  const state: LocalNodeState = {
    node_id: nodeID,
    name,
    roles: ['agent'],




    template: nodeTemplate?.name ?? null,



    agent: name,
    template_file: nodeTemplate ? templateCopy : null,



    owner: owner ?? null,
    created_at: new Date().toISOString(),






    driver_network: null,
    applied_to: null,




    workspace_contract: walls ? WORKSPACE_CONTRACT : null,



    ...(flagString(args, 'labels') ? { labels: parseLabels(flagString(args, 'labels')) } : {}),
  };
  writeFileSync(statePath, JSON.stringify(state, null, 2) + '\n', { mode: 0o600 });

  if (ctx.json) {
    process.stdout.write(json(state) + '\n');
    return ExitCode.OK;
  }

  process.stdout.write(
    `\n  Node opened on this server.\n\n` +
      `    node id  ${nodeID}\n` +
      `    name     ${name}\n` +
      `    fit-out  ${nodeTemplate?.name ?? 'none — an empty office'}\n` +
      `    space    ${walls ? `${WORKSPACE_CONTRACT} — ${walls.built.length} paths laid out` : 'none — this office has no workspace'}\n` +


      (facilities.length ? `    fitted   ${facilities.join(', ')}\n` : '') +
      (parcelsOf(nodeTemplate?.plugins ?? []).length
        ? parcelsOf(nodeTemplate?.plugins ?? [])








            .map((e, i) => `    ${i === 0 ? 'decor  ' : '       '}  ${describeDecoration(e.provider, walls?.states.get(e.provider) ?? 'parcel-placed')}\n`)
            .join('')
        : '') +
      `    owner    ${owner ?? 'none yet'}\n` +
      `    company  none yet\n\n` +
      ("  It is on this machine's register and visible to nobody else, in no\n" +
          '  network: nothing was founded, no certificate authority exists for it.\n' +
          '  `baton node list` shows it as not-enrolled, which is a state, not a\n' +
          '  failure. Two ways into a network:\n\n' +
          `    baton setup master ${name}          this machine becomes the control plane\n` +
          `    baton agent join --name <agent> --network <network>\n` +
          '                                       (once an agent has moved in, it joins one that exists)\n\n' +
          '  An owner moving in founds one when there is none: `baton agent create`.\n'),
  );
  return ExitCode.OK;
}


export interface LocalNodeState {
  node_id: string;
  name: string;
  roles: string[];


  template: string | null;

  template_file: string | null;

  owner: string | null;

















  workspace_contract?: string | null;

  agent: string | null;
  created_at: string;







  driver_network: string | null;























  network_ref?: string | null;
  network_id?: string | null;







  network_fingerprint?: string | null;
  applied_request_id?: string | null;

  applied_entry?: string | null;

  applied_to?: string | null;

  labels?: Record<string, string>;
}

















export function entryPointOf(args: ParsedArgs): string {
  return (
    flagString(args, 'advertise-url')
    ?? `https://127.0.0.1:${Number.parseInt(flagString(args, 'port') ?? '8443', 10)}`
  );
}






















export async function setupStandby(args: ParsedArgs): Promise<number> {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const name = args.positionals[2] ?? 'standby';

  const masterURL = flagString(args, 'master');
  if (!masterURL) {
    throw usageError(
      'setup standby needs --master <url>',
      'The control plane it mirrors: baton setup standby --master https://<primary>:8443 --token ./standby.token',
    );
  }
  if (!/^https:\/\//.test(masterURL)) {
    throw usageError(
      `--master must be https://host:port, got ${masterURL}`,
      'A mirror pulls whole control-plane snapshots; it will not do that over plaintext.',
    );
  }

  const tokenFile = flagString(args, 'token');
  if (!tokenFile) {
    throw usageError(
      'setup standby needs --token <file>',
      'Mint it on the primary: `baton token create --role master --out-file ./standby.token` — and pass the FILE, never the value.',
    );
  }
  if (!existsSync(tokenFile)) {
    throw preconditionError(
      `${tokenFile} does not exist`,
      'Mint one on the primary: `baton token create --role master --out-file ./standby.token`.',
    );
  }




  const token = readFileSync(tokenFile, 'utf8').trim();
  if (!token) {
    throw preconditionError(
      `${tokenFile} is empty`,
      'A truncated mint leaves an empty file; mint it again on the primary.',
    );
  }

  const ctx: CreateContext = {
    role: 'standby',
    name,
    dataDir,
    composeFile: join(dataDir, 'compose', `standby-${name}.yml`),
    container: containerName('standby', name),
    version: flagString(args, 'version') ?? DEFAULT_IMAGE_TAG,





    network: driverNetwork(args).name,
    dryRun: args.global.dryRun === true,
    json: args.global.output === 'json',
  };


  process.env.BATON_STANDBY_TOKEN = token;

  return createMaster(ctx, args, { withAgentRole: false, role: 'standby', masterURL });
}

export async function setupMaster(args: ParsedArgs): Promise<number> {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const registerDir = join(dataDir, 'nodes');




  const wanted = args.positionals[2];
  const offices = existsSync(registerDir)
    ? readdirSync(registerDir).filter((f) => f.endsWith('.json')).map((f) => f.replace(/\.json$/, ''))
    : [];




  if (args.global.dryRun && offices.length === 0 && !wanted) {
    const ctx: CreateContext = {
      role: 'master', name: 'planned', dataDir,
      composeFile: join(dataDir, 'compose', 'master-planned.yml'),
      container: 'baton-master-planned',
      version: flagString(args, 'version') ?? DEFAULT_IMAGE_TAG,
      network: driverNetwork(args).name,
      json: args.global.output === 'json', dryRun: true,
    };
    return createMaster(ctx, args, { withAgentRole: true });
  }

  if (offices.length === 0) {
    throw preconditionError(
      'this server has no node yet',




      'This promotes a node that already exists, and there is none here yet.\n' +
        '  The order is:\n' +
        '    baton doctor                     is this machine able to run it\n' +
        '    baton agent create --name <n>    an agent, and the node it runs on\n' +
        '    baton panel                      open the panel and look at it\n' +
        '\n' +
        '  `agent create` founds a control plane here if there is none, so most\n' +
        '  people never type this command.',
    );
  }
  const officeName = wanted ?? (offices.length === 1 ? offices[0]! : undefined);
  if (!officeName) {
    throw usageError(
      `this server has ${offices.length} nodes; say which one becomes the control plane`,
      `For example: baton setup master ${offices[0]}`,
    );
  }
  const statePath = join(registerDir, `${officeName}.json`);
  if (!existsSync(statePath)) {
    throw preconditionError(
      `this server has no node called ${officeName}`,
      `On the register: ${offices.join(', ')}.`,
    );
  }
  const state = JSON.parse(readFileSync(statePath, 'utf8')) as LocalNodeState;
  if (state.driver_network) {
    throw new BatonError({
      code: 'CONFLICT',
      message: `${officeName} is already in the company ${state.driver_network}`,
      remediation: 'A node belongs to one company. Evict and re-open it to move it.',
      exitCode: ExitCode.CONFLICT,
    });
  }

  preflight();
  const ctx: CreateContext = {
    role: 'master',
    name: state.name,
    dataDir,
    composeFile: join(dataDir, 'compose', `master-${state.name}.yml`),
    container: `baton-master-${state.name}`,
    version: flagString(args, 'version') ?? DEFAULT_IMAGE_TAG,
    network: driverNetwork(args).name,
    json: args.global.output === 'json',
    dryRun: args.global.dryRun,
  };
  mkdirSync(dataDir, { recursive: true });
  chmodSync(dataDir, 0o700);
  ensureNetwork(ctx.network);




  const code = await createMaster(ctx, args, { withAgentRole: true });
  if (code !== ExitCode.OK || ctx.dryRun) return code;

  state.roles = ['agent', 'master'];
  state.driver_network = ctx.network;
  writeFileSync(statePath, JSON.stringify(state, null, 2) + '\n', { mode: 0o600 });






















  const entry = entryPointOf(args);
  try {
    await new Client({
      baseURL: entry,
      adminDir: args.global.adminDir ?? join(dataDir, 'admin'),
      timeoutMs: args.global.timeoutMs,
    }).post('/networks/self/endpoints', { endpoint: entry, replace: true });
    if (!ctx.json) process.stdout.write(`  published ${entry} as this network's entry point.\n`);
  } catch (err) {




    throw new BatonError({
      code: 'PUBLISH_FAILED',
      message: `${state.name} holds both roles, but its entry point is not published`,
      remediation:
        `Until an entry point is published, the network serves no signed descriptor, so no applicant can find it. Repeat just this step:\n` +
        `    baton network publish --endpoint ${entry}\n  ` +
        (err instanceof Error ? err.message : String(err)),
      exitCode: ExitCode.INTERNAL,
    });
  }

  const panelPort = flagString(args, 'panel-port') ?? '8043';
  if (!ctx.json) {
    process.stdout.write(
      `\n  ${state.name} now holds both roles.\n\n` +
        `    roles   agent + master\n` +
        `    network ${ctx.network}\n\n` +
        '  Open the panel:\n\n' +
        `    baton panel --port ${panelPort} --dist <panel build>\n\n` +
        '  Create your first agent:\n\n' +





        '    baton agent create --name coder\n',
    );
  }
  return ExitCode.OK;
}














export async function foundMaster(args: ParsedArgs, officeName: string): Promise<{ code: number; entry: string }> {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  preflight();






  const masterName = `${officeName}-master`;
  const ctx: CreateContext = {
    role: 'master',
    name: masterName,
    dataDir,
    composeFile: join(dataDir, 'compose', `master-${masterName}.yml`),
    container: `baton-master-${masterName}`,
    version: flagString(args, 'version') ?? DEFAULT_IMAGE_TAG,
    network: driverNetwork(args).name,
    json: args.global.output === 'json',
    dryRun: args.global.dryRun,
  };
  const port = Number.parseInt(flagString(args, 'port') ?? '8443', 10);
  const entry = flagString(args, 'advertise-url') ?? `https://127.0.0.1:${port}`;
  const code = await createMaster(ctx, args, { withAgentRole: false });
  return { code, entry };
}

async function createMaster(
  ctx: CreateContext,
  args: ParsedArgs,
  opts: { withAgentRole?: boolean; role?: 'master' | 'standby'; masterURL?: string } = {},
): Promise<number> {
  const port = Number.parseInt(flagString(args, 'port') ?? '8443', 10);
  const advertiseURL = flagString(args, 'advertise-url') ?? `https://127.0.0.1:${port}`;







  const bindAddress = flagString(args, 'bind') ?? '127.0.0.1';









  const advertisedHost = (() => {
    try {
      return new URL(advertiseURL).hostname;
    } catch {
      return '';
    }
  })();
  const sans = ['localhost', '127.0.0.1'];
  if (advertisedHost && !sans.includes(advertisedHost)) sans.push(advertisedHost);

  const manifest = renderCompose({
    role: opts.role ?? 'master',
    masterURL: opts.masterURL,
    seccompProfile: materializeSeccompProfile(ctx.dataDir),
    name: ctx.name,
    version: ctx.version,
    registry: flagString(args, 'registry'),
    port,
    bindAddress,
    advertiseURL,
    tlsSANs: sans,
    network: ctx.network,
    withAgentRole: opts.withAgentRole,
  });

  if (ctx.dryRun) {
    process.stdout.write(
      ctx.json
        ? json({ compose_file: ctx.composeFile, manifest, container: ctx.container }) + '\n'
        : `# would write ${ctx.composeFile}\n${manifest}\n`,
    );
    return ExitCode.OK;
  }

  if (containerExists(ctx.container)) {
    note(ctx, `${ctx.container} already exists; making sure it is running`);
  }

  writeManifest(ctx, manifest);
  note(ctx, `starting the control plane (${ctx.container})`);
  composeUp(ctx);
  await waitHealthy(ctx.container, ctx.name, 'the control plane');










  if (opts.role === 'standby') {
    if (!ctx.json) {
      process.stdout.write(
        `\n  ${ctx.container} is mirroring ${opts.masterURL} (read-only).\n` +
          `  Use the PRIMARY's operator credentials against it; a mirror issues nothing.\n` +
          `  It is a transfer target: \`baton transfer master --to ${ctx.name}\` on the primary.\n`,
      );
    }
    return ExitCode.OK;
  }




  const adminDir = args.global.adminDir ?? join(ctx.dataDir, 'admin');
  mkdirSync(adminDir, { recursive: true });
  chmodSync(adminDir, 0o700);

  if (!existsSync(join(adminDir, 'admin.crt'))) {
    note(ctx, 'issuing operator credentials');
    const res = execCapture(ctx.container, [
      'control-api', 'bootstrap-admin', '--out-dir', '/var/lib/baton/admin',
    ]);
    if (!res.ok) {
      throw new BatonError({
        code: 'BOOTSTRAP_FAILED',
        message: `could not issue operator credentials: ${(res.stderr ?? '').trim()}`,
        remediation: `Look at: docker logs ${ctx.container}`,
        exitCode: ExitCode.INTERNAL,
      });
    }
    for (const file of ['admin.crt', 'admin.key', 'ca.crt']) {
      const copy = copyFileOut(ctx.container, `/var/lib/baton/admin/${file}`, join(adminDir, file));
      if (!copy.ok) {
        throw new BatonError({
          code: 'BOOTSTRAP_FAILED',
          message: `could not copy ${file} out of the container`,
          remediation: 'Check that the container is running and the data volume is mounted.',
          exitCode: ExitCode.INTERNAL,
        });
      }
    }

    chmodSync(join(adminDir, 'admin.key'), 0o600);
    chmodSync(join(adminDir, 'admin.crt'), 0o600);
  }

  const result = {
    role: 'master',
    name: ctx.name,
    container: ctx.container,
    url: advertiseURL,
    admin_dir: adminDir,
    compose_file: ctx.composeFile,
  };

  if (ctx.json) {
    process.stdout.write(json(result) + '\n');
  } else {


    process.stdout.write(
      `\ncontrol plane ready\n\n` +
        `  url         ${advertiseURL}\n` +
        `  credentials ${adminDir} (mode 0600; not printed)\n` +
        `  manifest    ${ctx.composeFile}\n\n` +
        `next:\n` +
        `  baton agent create --name coder                 an agent here\n` +
        `  baton network requests                          who has applied to join\n` +
        `  baton network add --agent <name>                admit one\n`,
    );
  }
  return ExitCode.OK;
}


export function readTokenFile(path: string | undefined, role: string): string {
  if (!path) {
    throw usageError(
      '--enrollment-token-file is required',
      `Mint one first:\n` +
        `    baton token create --role ${role} --ttl 15m --out-file ./${role}.token\n` +
        '  A token is never accepted as a flag value: flags land in shell history ' +
        'and in the process list, both of which outlive the token.',
    );
  }
  if (!existsSync(path)) {
    throw preconditionError(`${path} does not exist`, 'Create the token file first.');
  }
  const token = readFileSync(path, 'utf8').trim();
  if (!token) {
    throw preconditionError(`${path} is empty`, 'Mint a token with `baton token create`.');
  }
  return token;
}


function extractJSON(text: string): Record<string, unknown> {
  const start = text.lastIndexOf('{');
  if (start < 0) return {};
  try {
    return JSON.parse(text.slice(start, text.lastIndexOf('}') + 1)) as Record<string, unknown>;
  } catch {
    return {};
  }
}

export { flagBool };
