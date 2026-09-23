// SPDX-License-Identifier: Apache-2.0
















import { existsSync, statSync } from 'node:fs';
import { isAbsolute } from 'node:path';
import { preconditionError, usageError } from '../errors.js';
import type { RuntimeManifest } from '../manifests/compose.js';

const API_VERSION = 'baton.mailloop.dev/v1alpha1';
const KIND = 'AgentRuntime';
























export type KnownTree = { [key: string]: true | KnownTree | [KnownTree] };







export const SECRET_ENTRY_KNOWN: KnownTree = { name: true, from: true, mountPath: true, label: true, provider: true, help_url: true, required: true };

const RUNTIME_KNOWN: KnownTree = {
  apiVersion: true,
  kind: true,
  metadata: { name: true, labels: true },
  package: { image: true, install: true, command: true, workingDir: true },
  adapter: {
    terminal: { tty: true },
    workspace: { volume: true, mountPath: true, readOnly: true },
    lifecycle: { restart: true, stopGraceSec: true },
    health: { probe: true, intervalSec: true, tcp: true, exec: true, failureThreshold: true },



    status: { report: true },



    skills: { mountPath: true, discovery: true, runtimePath: true },
    persistence: [{ path: true, class: true }],











    inbox: { onMessage: true },
  },
  execution: {
    user: true,
    driver_network: true,
    network: { egress: { mode: true, allow: true } },
    checkpoint: { requested: true },
    secrets: [SECRET_ENTRY_KNOWN],
    env: [{ name: true, value: true }],
    resources: { cpu: true, memory: true },
  },
};


































export function refuseSocketSource(name: string, source: string, specPath: string): void {
  const engine = new Set(['/var/run/docker.sock', '/run/docker.sock']);
  const host = process.env.DOCKER_HOST ?? '';
  if (host.startsWith('unix://')) engine.add(host.slice('unix://'.length));
  if (engine.has(source)) {
    throw preconditionError(
      `${specPath}: secret ${name} reads from ${source}, the container engine's socket — mounting it hands the node root on this host`,
      'A secret is a file the node reads; the engine socket is not one. Remove it from execution.secrets. ' +
        'No BATON component needs the socket inside a container — the CLI drives the engine from outside.',
    );
  }
}


export function assertKnownFields(value: unknown, schema: KnownTree | [KnownTree], path: string, specPath: string): void {
  if (Array.isArray(schema)) {
    if (!Array.isArray(value)) return;
    value.forEach((entry, i) => assertKnownFields(entry, schema[0], `${path}[${i}]`, specPath));
    return;
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return;
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    if (child === undefined || child === null) continue;
    const known = schema[key];
    if (known === undefined) {
      throw usageError(
        `${specPath}: ${path ? `${path}.` : ''}${key} is not a field this build understands`,
        `${path || 'the runtime section'} has ${Object.keys(schema).join(', ')} — nothing else. The daemon parses ` +
          'this section with strict fields, so a key that survives here fails inside the container, after ' +
          '`create` has already reported success.',
      );
    }
    if (known !== true) assertKnownFields(child, known, path ? `${path}.${key}` : key, specPath);
  }
}


export interface RawSecret { name?: string; from?: string; mountPath?: string; required?: boolean }








export interface FacilitySecrets { facility: string; label: string; secrets: RawSecret[] }

interface RawSpec {
  apiVersion?: string;
  kind?: string;
  metadata?: { name?: string; labels?: Record<string, string> };

  package?: {
    image?: string;
    install?: unknown;
    command?: unknown;
    workingDir?: string;
  };
  adapter?: {
    terminal?: { tty?: boolean };
    workspace?: { volume?: string; mountPath?: string; readOnly?: boolean };










    skills?: { mountPath?: string };
  };
  execution?: {












    user?: string;
    secrets?: RawSecret[];
    resources?: { cpu?: string; memory?: string };
    env?: unknown;
    network?: unknown;
    driver_network?: unknown;
    checkpoint?: unknown;
    [other: string]: unknown;
  };
}

















const EXECUTION_SUPPORTED = new Set([
  'resources', 'secrets', 'env', 'network',




  'user',



  'checkpoint',




  'driver_network',
]);


const CHECKPOINT_SUPPORTED = new Set(['filesystem']);


















const EGRESS_SUPPORTED = new Set(['allow', 'deny']);











export function assertSecretSaysRequired(s: RawSecret, specPath: string): void {
  const name = s.name ?? '(unnamed)';
  if (typeof s.required !== 'boolean') {
    throw usageError(
      `${specPath}: secret ${name} does not say whether it is required`,
      'Write `required: true` (the node cannot do its job without it) or `required: false` ' +
        '(an alternative exists, such as an interactive login in the persisted home). ' +
        'Silence would be read as optional by whoever renders the credential card.',
    );
  }
}














export function resolveSecretMount(
  s: RawSecret,
  override: string | undefined,
  specPath: string,
  wsMountPath: string | undefined,
): { source: string; target: string } {
  const name = s.name ?? '(unnamed)';












  assertSecretSaysRequired(s, specPath);
  if (!s.from) {
    throw usageError(
      `${specPath}: secret ${name} has no from:`,
      'A template says where the value appears INSIDE the container (`from: file:/run/secrets/…`); ' +
        '`--secret NAME=<host file>` says where the source is ON THIS HOST. The flag cannot supply the ' +
        'first one — without it the daemon refuses the spec and the node never starts.',
    );
  }
  const from = override ? `file:${override}` : (s.from ?? '');
  if (!from.startsWith('file:')) {
    throw usageError(
      `${specPath}: secret ${name} has from: ${from || '(none)'}; only file: sources work`,
      'A secret is mounted as a file, never passed as an environment variable — ' +
        'an env var is visible in `docker inspect`, in /proc/<pid>/environ, and in ' +
        `crash reports. Write from: file:/path/to/${name.toLowerCase()}`,
    );
  }
  const source = from.slice('file:'.length);
  if (!isAbsolute(source)) {
    throw usageError(
      `${specPath}: secret ${name} path ${source} is not absolute`,
      'Use a full path; it is resolved on this host, not inside the container.',
    );
  }
  refuseSocketSource(name, source, specPath);








  if (existsSync(source)) {
    const st = statSync(source);




    if (st.isSocket()) {
      throw preconditionError(
        `secret ${name} points at a socket (${source}), and a secret cannot be one`,
        'Point it at a file holding the value. A service socket is not a credential and does not belong in execution.secrets.',
      );
    }
    if (st.isDirectory()) {
      throw usageError(
        `secret ${name} points at a directory (${source})`,
        'A secret source is a single file holding the value.',
      );
    }



    if ((st.mode & 0o077) !== 0) {
      throw preconditionError(
        `secret ${name} reads from ${source}, which is readable by other users ` +
          `(mode ${(st.mode & 0o777).toString(8).padStart(3, '0')})`,
        `Restrict it first: chmod 600 ${source}`,
      );
    }
  }

  const target = s.mountPath ?? `/run/secrets/${name}`;





  if (wsMountPath && isWithin(target, wsMountPath)) {
    throw usageError(
      `secret ${name} mounts at ${target}, inside the workspace ${wsMountPath}`,
      'Mount credentials outside the workspace — /run/secrets/ is the default ' +
        'for exactly this reason. The workspace is a git working tree, and a ' +
        'secret inside it is one `git add -A` from being pushed.',
    );
  }
  return { source, target };
}










export function loadRuntimeManifest(
  runtime: unknown,
  specPath: string,
  nodeName: string,
  secretOverrides: Map<string, string> = new Map(),
  facilitySecrets: FacilitySecrets[] = [],
): RuntimeManifest {




  const doc = runtime as RawSpec;
  if (!doc || typeof doc !== 'object') {
    throw usageError(`${specPath} is empty`, 'The runtime section needs apiVersion, kind, package — the AgentRuntime document.');
  }

  if (doc.apiVersion !== API_VERSION) {
    throw usageError(
      `${specPath} has apiVersion ${doc.apiVersion ?? '(none)'}; this build understands ${API_VERSION}`,
      `Set apiVersion: ${API_VERSION}`,
    );
  }
  if (doc.kind !== KIND) {
    throw usageError(`${specPath} has kind ${doc.kind ?? '(none)'}`, `Set kind: ${KIND}`);
  }




  const pkg = doc.package ?? {};
  const adapter = doc.adapter ?? {};
  const execution = doc.execution ?? {};







  if ((execution as { isolation?: unknown }).isolation !== undefined) {
    throw preconditionError(
      `${specPath} sets execution.isolation, which left the template on 2026-08-19`,
      'Whether a node is a container or a microVM is the provider\'s capability, not the runtime\'s ' +
        'request. Remove the line; nothing else changes. If this file was seeded by `baton init` ' +
        'before that date, replace it with the shipped one (`baton template list` marks stale copies).',
    );
  }
  for (const asked of Object.keys(execution)) {
    if (execution[asked] === undefined || execution[asked] === null) continue;
    if (!EXECUTION_SUPPORTED.has(asked)) {
      throw preconditionError(
        `${specPath} asks for execution.${asked}, which this build cannot provide`,
        `Remove execution.${asked} from the spec, or run a build that implements it. ` +
          'A condition that cannot be met is refused rather than dropped.',
      );
    }
  }






  assertKnownFields(doc, RUNTIME_KNOWN, '', specPath);
  const checkpoint = (execution.checkpoint as { requested?: unknown } | undefined)?.requested;
  if (checkpoint !== undefined && !CHECKPOINT_SUPPORTED.has(String(checkpoint))) {
    throw preconditionError(
      `${specPath} asks for execution.checkpoint.requested: ${String(checkpoint)}, and this build delivers ` +
        `only ${[...CHECKPOINT_SUPPORTED].join(', ')}`,
      'A requested condition that cannot be met is refused, never silently weakened: ask for filesystem, ' +
        'or run a build that delivers the kind you need.',
    );
  }

  const egressMode = (execution.network as { egress?: { mode?: string } } | undefined)?.egress?.mode;
  if (egressMode !== undefined && !EGRESS_SUPPORTED.has(egressMode)) {
    throw preconditionError(
      `${specPath} asks for execution.network.egress.mode: ${egressMode}, which this build cannot enforce`,
      `This build delivers ${[...EGRESS_SUPPORTED].join(' and ')}. ` +
        'Treating an unenforceable policy as `allow` would grant exactly the access ' +
        'it was written to withhold, so it is refused instead.',
    );
  }



  if (!pkg.image) {
    throw usageError(
      `${specPath} has no package.image`,
      'An agent-node is a container, so a spec needs the image to run: the node ' +
        'daemon plus one runtime, e.g. image: ghcr.io/batonos/baton-agent-dev:0.1.0',
    );
  }

  const command = pkg.command;
  if (!Array.isArray(command) || command.length === 0) {
    throw usageError(
      `${specPath} has no package.command`,
      'Give it as a list, never a string: command: ["your-runtime", "--workspace", "/workspace"]. ' +
        'A list is passed to exec directly, so an argument containing a space or a ' +
        'semicolon stays an argument.',
    );
  }

  const ws = adapter.workspace ?? {};
  if (ws.volume && !ws.mountPath) {
    throw usageError(
      `${specPath} sets adapter.workspace.volume without adapter.workspace.mountPath`,
      'A workspace needs a mount path, or neither field.',
    );
  }





  const workspaceVolume = ws.mountPath ? (ws.volume ?? `${nodeName}-workspace`) : undefined;





  const entries = [
    ...(execution.secrets ?? []).map((s) => ({ s, label: specPath })),
    ...facilitySecrets.flatMap((f) => f.secrets.map((s) => ({ s, label: f.label }))),
  ];




  const declaredBy = new Map<string, string>();
  for (const { s, label } of entries) {
    const name = s.name ?? '(unnamed)';
    const prior = declaredBy.get(name);
    if (prior !== undefined) {
      throw usageError(
        `secret ${name} is declared twice: by ${prior} and by ${label}`,
        'One name is one file. Rename it in one of them — two declarations of one credential would make ' +
          '`--secret NAME=…` mean two places at once.',
      );
    }
    declaredBy.set(name, label);
  }
  const declared = new Set(declaredBy.keys());
  for (const name of secretOverrides.keys()) {
    if (!declared.has(name)) {
      throw usageError(
        `--secret ${name}=… names a secret ${specPath} does not declare`,
        `That spec declares: ${[...declared].join(', ') || '(none)'}. ` +
          'A secret has to be declared before it can be pointed somewhere, because ' +
          'the spec is what says where it mounts — an override alone would put a ' +
          'credential on the host with nowhere to appear inside the container.',
      );
    }
  }








  const resolved = entries.map(({ s, label }) => ({
    name: s.name ?? '(unnamed)',
    ...resolveSecretMount(s, secretOverrides.get(s.name ?? '(unnamed)'), label, ws.mountPath),
  }));



  const byTarget = new Map<string, string>();
  for (const m of resolved) {
    const prior = byTarget.get(m.target);
    if (prior !== undefined) {
      throw usageError(
        `secrets ${prior} and ${m.name} both mount at ${m.target}`,
        'Give each secret its own mountPath — the default is /run/secrets/<name>, which is unique when the names are.',
      );
    }
    byTarget.set(m.target, m.name);
  }
  const secretMounts = resolved.map(({ source, target }) => ({ source, target }));








  const rawUser = execution.user === undefined ? undefined : String(execution.user).trim();
  if (rawUser !== undefined && !/^[0-9]+(:[0-9]+)?$/.test(rawUser)) {
    throw preconditionError(
      `${specPath} asks for execution.user: ${rawUser}, which is not a numeric uid or uid:gid`,
      'Give a number, for example `user: "65533"` or `user: "65533:65533"`. A NAME would be ' +
        'resolved inside the image, against a passwd file BATON never reads — so the same ' +
        'spec would mean different ids in different images while looking the same.',
    );
  }



  if (rawUser !== undefined && rawUser.split(':')[0] === '0') {
    throw preconditionError(
      `${specPath} asks for execution.user: 0, which is the container's root`,
      'The field exists so the runtime runs as a uid different from the daemon\'s and is NOT the owner of what it reads. ' +
        'Root ignores permission bits, so this asks for the state the field was added to end.',
    );
  }

  return {
    name: doc.metadata?.name ?? nodeName,
    image: pkg.image,
    specPath,
    runtimeUser: rawUser,
    workspaceVolume,
    workspaceMountPath: ws.mountPath,
    skillsMountPath: adapter.skills?.mountPath,
    workspaceReadOnly: ws.readOnly === true,
    cpu: cpuCores(execution.resources?.cpu, specPath),
    memory: memoryBytes(execution.resources?.memory, specPath),
    secretMounts,






    installPrograms: Array.isArray(pkg.install)
      ? pkg.install
          .map((entry) => (Array.isArray(entry) ? String(entry[0] ?? '') : ''))
          .filter((p) => p !== '')
      : [],









    commandPath: (() => {
      const first = Array.isArray(pkg.command) ? String(pkg.command[0] ?? '') : '';
      return first.startsWith('/') ? first : undefined;
    })(),
    egress: egressMode,
    driverNetwork: driverNetworkOf(execution, specPath),


    template: undefined,
  };
}















function driverNetworkOf(
  execution: { driver_network?: unknown },
  specPath: string,
): string | undefined {
  const raw = execution.driver_network;
  if (raw === undefined) return undefined;
  if (typeof raw !== 'string' || raw.trim() === '') {
    throw preconditionError(
      `${specPath} has execution.driver_network: ${JSON.stringify(raw)}, which is not a network name`,
      'Give the container engine\'s network as a non-empty string, e.g. ' +
        'driver_network: baton-poc_default. Leave it out to take the caller\'s network.',
    );
  }
  return raw;
}


function isWithin(target: string, dir: string): boolean {
  const d = dir.endsWith('/') ? dir.slice(0, -1) : dir;
  return target === d || target.startsWith(`${d}/`);
}























export function cpuCores(raw: string | undefined, specPath: string): string | undefined {
  if (!raw) return undefined;

  const text = raw.trim();



  if (text.endsWith('m')) {
    const milli = Number(text.slice(0, -1).trim());
    if (Number.isFinite(milli) && milli > 0) return String(milli / 1000);
  } else {
    const cores = Number(text);
    if (Number.isFinite(cores) && cores > 0) return String(cores);
  }

  throw usageError(
    `${specPath}: spec.resources.cpu is ${raw}`,
    'Use a positive number of cores like 2 or 1.5, or milli-cores like 500m.',
  );
}













export function memoryBytes(raw: string | undefined, specPath: string, field = 'spec.resources.memory'): string | undefined {
  if (!raw) return undefined;

  const factors: [string, number][] = [
    ['Ki', 1024], ['Mi', 1024 ** 2], ['Gi', 1024 ** 3], ['Ti', 1024 ** 4],
    ['K', 1e3], ['M', 1e6], ['G', 1e9], ['T', 1e12],
  ];

  const text = raw.trim();
  for (const [suffix, factor] of factors) {
    if (!text.endsWith(suffix)) continue;
    const n = Number(text.slice(0, -suffix.length).trim());
    if (Number.isFinite(n) && n > 0) return String(Math.round(n * factor));
    break;
  }

  const plain = Number(text);
  if (Number.isFinite(plain) && plain > 0) return String(Math.round(plain));




  throw usageError(
    `${specPath}: ${field} is ${raw}`,
    'Use a positive size like 512Mi, 2Gi, or a plain number of bytes.',
  );
}
