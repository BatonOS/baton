// SPDX-License-Identifier: Apache-2.0

























import { spawn, spawnSync } from 'node:child_process';
import { readFileSync, statSync } from 'node:fs';

import { LXCFS_ROOT, LXCFS_PATHS } from '../manifests/compose.js';











const ENGINE_NAME = 'docker';












export const MIN_ENGINE_MAJOR = 26;


export function engineMeetsMinimum(version: string): boolean {
  const major = Number.parseInt(version, 10);
  return !Number.isNaN(major) && major >= MIN_ENGINE_MAJOR;
}


export interface EngineResult {
  ok: boolean;
  stdout: string;
  stderr: string;
}

function run(
  argv: string[],
  opts: { input?: string; env?: NodeJS.ProcessEnv } = {},
): EngineResult {
  const res = spawnSync('docker', argv, {
    encoding: 'utf8',
    input: opts.input,
    env: opts.env,
  });
  return {
    ok: res.status === 0,
    stdout: res.stdout ?? '',
    stderr: res.stderr ?? '',
  };
}


function runAttached(
  argv: string[],
  opts: { quiet?: boolean; env?: NodeJS.ProcessEnv } = {},
): Promise<number> {
  const child = spawn('docker', argv, {
    stdio: opts.quiet ? 'ignore' : 'inherit',
    env: opts.env,
  });
  return new Promise((resolve) => {


    child.on('exit', (code, signal) => resolve(signal === 'SIGINT' ? 130 : (code ?? 1)));
  });
}

function runBlocking(argv: string[], opts: { quiet?: boolean; env?: NodeJS.ProcessEnv } = {}): boolean {
  return runBlockingSaid(argv, opts).ok;
}


















export function runBlockingSaid(
  argv: string[],
  opts: { quiet?: boolean; env?: NodeJS.ProcessEnv } = {},
): { ok: boolean; said: string } {
  const res = spawnSync('docker', argv, {
    stdio: opts.quiet ? ['ignore', 'ignore', 'pipe'] : ['inherit', 'inherit', 'pipe'],
    env: opts.env,
    encoding: 'utf8',
  });
  const said = res.stderr ?? '';
  if (!opts.quiet && said) process.stderr.write(said);
  return { ok: res.status === 0, said };
}





export function engineVersion(): { available: boolean; version: string } {
  const res = run(['version', '--format', '{{.Server.Version}}']);
  return { available: res.ok, version: res.stdout.trim() };
}


export function composeVersion(): { available: boolean; version: string } {
  const res = run(['compose', 'version', '--short']);
  return { available: res.ok, version: res.stdout.trim() };
}







export function hostSecurityOptions(): EngineResult {
  return run(['info', '-f', '{{json .SecurityOptions}}']);
}












export function imageEntrypoint(ref: string): string[] | undefined {
  const res = run(['image', 'inspect', '-f', '{{json .Config.Entrypoint}}', ref]);
  if (!res.ok) return undefined;
  try {
    const parsed: unknown = JSON.parse(res.stdout.trim());
    return Array.isArray(parsed) ? parsed.map(String) : [];
  } catch {
    return [];
  }
}


export function imageIsPresent(ref: string): boolean {
  return run(['image', 'inspect', '-f', '{{.Id}}', ref]).ok;
}










export function imageHasProgram(ref: string, program: string): boolean | undefined {







  const res = run([
    'run', '--rm', '--entrypoint', 'sh', ref, '-c',
    `if command -v ${program} >/dev/null 2>&1; then echo BATON_YES; else echo BATON_NO; fi`,
  ]);
  if (res.stdout.includes('BATON_YES')) return true;
  if (res.stdout.includes('BATON_NO')) return false;
  return undefined;
}


export function pullImage(ref: string): boolean {
  return run(['pull', '--quiet', ref]).ok;
}





export function containerExists(name: string): boolean {
  return run(['inspect', '-f', '{{.Id}}', name]).ok;
}


export function containerNames(prefix: string): string[] {
  const res = run(['ps', '--format', '{{.Names}}', '--filter', `name=^${prefix}`]);
  if (!res.ok) return [];
  return res.stdout.trim().split('\n').filter(Boolean);
}









export interface ContainerRecord {
  name: string;
  running: boolean;
}


export function containerInventory(prefix: string): ContainerRecord[] {
  const res = run(['ps', '-a', '--filter', `name=^${prefix}`, '--format', '{{.Names}}\t{{.State}}']);
  if (!res.ok) return [];
  return res.stdout
    .trim()
    .split('\n')
    .filter(Boolean)
    .map((line) => {
      const [name = '', state = ''] = line.split('\t');
      return { name, running: state === 'running' };
    });
}












export function execCapture(container: string, argv: string[], opts: { user?: string } = {}): EngineResult {
  return run(['exec', ...(opts.user ? ['-u', opts.user] : []), container, ...argv]);
}


export function execSucceeds(container: string, argv: string[]): boolean {
  return run(['exec', container, ...argv]).ok;
}


export function execInteractive(
  container: string,
  argv: string[],
  opts: { tty?: boolean; stdin?: boolean } = {},
): Promise<number> {
  return runAttached(execArgv(container, argv, opts));
}

function execArgv(
  container: string,
  argv: string[],
  opts: { tty?: boolean; stdin?: boolean },
): string[] {




  const flags: string[] = [];
  if (opts.stdin || opts.tty) flags.push('-i');
  if (opts.tty) flags.push('-t');
  return ['exec', ...flags, container, ...argv];
}









export function execPlan(
  container: string,
  argv: string[],
  opts: { tty?: boolean; stdin?: boolean } = {},
): string[] {
  return [ENGINE_NAME, ...execArgv(container, argv, opts)];
}



























export type RunState = 'running' | 'stopped' | 'unknown';

export function runState(container: string): RunState {
  const res = run(['inspect', '-f', '{{.State.Status}}', container]);
  if (!res.ok) return 'unknown';
  switch (res.stdout.trim()) {



    case 'running':
    case 'paused':
    case 'restarting':
      return 'running';
    case 'created':
    case 'exited':
    case 'dead':
    case 'removing':
      return 'stopped';
    default:


      return 'unknown';
  }
}

export type Readiness = 'healthy' | 'running' | 'starting' | 'unhealthy' | 'missing' | 'unknown';

export function readiness(container: string): Readiness {
  const res = run([
    'inspect',
    '-f',
    '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}',
    container,
  ]);
  if (!res.ok) return 'missing';
  switch (res.stdout.trim()) {
    case 'healthy':
      return 'healthy';
    case 'unhealthy':
      return 'unhealthy';
    case 'starting':
    case 'created':
    case 'restarting':
      return 'starting';
    case 'running':
      return 'running';
    case 'exited':
    case 'dead':
    case 'removing':
      return 'missing';
    default:
      return 'unknown';
  }
}


export function recentOutput(container: string, lines: number): string {
  return run(['logs', '--tail', String(lines), container]).stdout;
}


export function containerNamesOnNetwork(prefix: string, network: string): string[] {
  const res = run([
    'ps',
    '--format',
    '{{.Names}}',
    '--filter',
    `name=^${prefix}`,
    '--filter',
    `network=${network}`,
  ]);
  if (!res.ok) return [];
  return res.stdout.trim().split('\n').filter(Boolean);
}


export function networkExists(name: string): boolean {
  return run(['network', 'inspect', name]).ok;
}








export function removeContainer(name: string): EngineResult {
  return run(['rm', '-f', name]);
}




























export function volumesFor(container: string): string[] | undefined {
  const res = run(['volume', 'ls', '-q']);
  if (!res.ok) return undefined;
  return res.stdout
    .trim()
    .split('\n')
    .filter(Boolean)
    .filter((v) => volumeBelongsTo(v, container));
}









export function volumeBelongsTo(volume: string, container: string): boolean {
  const node = container.replace(/^baton-[a-z]+-/, '');
  const owns = (want: string) => volume === want || volume.endsWith(`_${want}`);
  return owns(`${node}-workspace`) || owns(`${container}-data`);
}


export function removeVolumesFor(container: string): void {
  for (const v of volumesFor(container) ?? []) {
    run(['volume', 'rm', '-f', v]);
  }
}


export function createNetwork(name: string): EngineResult {
  return run(['network', 'create', name]);
}














export function createInternalNetwork(name: string): EngineResult {
  return run(['network', 'create', '--internal', name]);
}


export function connectNetwork(network: string, container: string): EngineResult {
  return run(['network', 'connect', network, container]);
}


export function copyFileOut(container: string, from: string, to: string): EngineResult {
  return run(['cp', `${container}:${from}`, to]);
}











export function copyFileOutIfPresent(container: string, from: string, to: string): EngineResult & { present: boolean } {
  const res = run(['cp', `${container}:${from}`, to]);
  if (res.ok) return { ...res, present: true };
  return { ...res, present: !/Could not find the file/.test(res.stderr) };
}













export function initVolumeSubpath(volume: string, subpath: string, uid: number, image: string): EngineResult {
  return run([


    'run', '--rm', '--user', '0:0', '--entrypoint', 'sh', '-v', `${volume}:/v`, image,
    '-c', `mkdir -p /v/${subpath} && chown ${uid}:${uid} /v/${subpath}`,
  ]);
}


export function copyOut(container: string, from: string, to: string): EngineResult {
  return run(['cp', `${container}:${from}/.`, to]);
}


export function copyIn(from: string, container: string, to: string): EngineResult {
  return run(['cp', `${from}/.`, `${container}:${to}`]);
}


export function streamLogs(
  container: string,
  opts: { tail: string; follow: boolean },
): Promise<number> {
  return runAttached([
    'logs',
    ...(opts.follow ? ['-f'] : []),
    '--tail',
    opts.tail,
    container,
  ]);
}





export function projectUp(
  file: string,
  opts: { quiet?: boolean; env?: NodeJS.ProcessEnv } = {},
): boolean {
  return runBlocking(['compose', '-f', file, 'up', '-d'], opts);
}


export function projectUpSaid(
  file: string,
  opts: { quiet?: boolean; env?: NodeJS.ProcessEnv } = {},
): { ok: boolean; said: string } {
  return runBlockingSaid(['compose', '-f', file, 'up', '-d'], opts);
}


export function projectVerb(file: string, verbArgs: string[], opts: { quiet?: boolean } = {}): boolean {
  return runBlocking(['compose', '-f', file, ...verbArgs], opts);
}


export function projectRun(
  file: string,
  service: string,
  argv: string[],
  opts: { env?: Record<string, string> } = {},
): EngineResult {
  const envArgs = Object.entries(opts.env ?? {}).flatMap(([k, v]) => ['-e', `${k}=${v}`]);
  return run(['compose', '-f', file, 'run', '--rm', '--no-deps', '-T', ...envArgs, service, ...argv]);
}


export function projectDown(
  file: string,
  opts: { volumes: boolean; quiet?: boolean },
): boolean {
  const argv = ['compose', '-f', file, 'down', '--remove-orphans'];
  if (opts.volumes) argv.push('--volumes');
  return runBlocking(argv, { quiet: opts.quiet });
}













export interface LxcfsProbe {
  usable: boolean | undefined;
  detail: string;
}





















export function hostLxcfs(root: string = LXCFS_ROOT): LxcfsProbe {









  const dockerHost = process.env.DOCKER_HOST ?? '';
  if (dockerHost !== '' && !dockerHost.startsWith('unix://')) {
    return {
      usable: undefined,
      detail: `the engine at DOCKER_HOST=${dockerHost} resolves bind sources in its own filesystem, which this probe cannot see`,
    };
  }
  const missing: string[] = [];
  for (const rel of LXCFS_PATHS) {
    try {
      statSync(`${root}/${rel}`);
    } catch (err) {
      const code = (err as NodeJS.ErrnoException).code;
      if (code === 'ENOENT' || code === 'ENOTDIR') {
        missing.push(rel);
        continue;
      }

      return { usable: undefined, detail: `${root}/${rel}: ${code ?? String(err)}` };
    }
  }
  if (missing.length === LXCFS_PATHS.length) {
    return { usable: false, detail: `no lxcfs at ${root}` };
  }
  if (missing.length > 0) {
    return {
      usable: false,
      detail: `lxcfs at ${root} is missing ${missing.length} of ${LXCFS_PATHS.length} views (${missing.join(', ')})`,
    };
  }




  try {
    const text = readFileSync(`${root}/proc/meminfo`, 'utf8');
    if (!/^MemTotal:/m.test(text)) {
      return { usable: false, detail: `${root}/proc/meminfo has no MemTotal line` };
    }
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code;

    return { usable: false, detail: `${root}/proc/meminfo unreadable: ${code ?? String(err)}` };
  }

  return { usable: true, detail: `${LXCFS_PATHS.length} views at ${root}` };
}
