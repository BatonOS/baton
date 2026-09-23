// SPDX-License-Identifier: Apache-2.0





















import { createHash } from 'node:crypto';
import { closeSync, existsSync, fsyncSync, mkdirSync, openSync, readFileSync, renameSync, statSync, writeFileSync, writeSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, join, relative, resolve, sep } from 'node:path';
import type { ParsedArgs } from '../args.js';
import { BatonError, ExitCode } from '../errors.js';
import { joinNetwork, officeOfAgent } from '../commands/agent.js';
import { nodeCreate } from '../commands/create.js';
import { cloneCreatePlan, restore, snapshot } from '../commands/snapshot.js';
import { defaultDataDir } from '../api/client.js';
import { OPERATION_ID_LABEL } from '../manifests/compose.js';
import { containerInventory } from '../runtime/engine.js';
import type { OperationLog } from './log.js';
import type { StepName } from './protocol.js';


export class Refused extends Error {
  constructor(message: string, readonly evidence: Record<string, unknown> = {}) {
    super(message);
  }
}






export class Failed extends Error {
  constructor(message: string, readonly evidence: Record<string, unknown> = {}) {
    super(message);
  }
}


export interface Workspace {
  id: string;
  dir: string;
}

export interface StepContext {

  args: ParsedArgs;
  log: OperationLog;
  operationID: string;

  opDir: string;

  workspace?: Workspace;
}

type Handler = (ctx: StepContext, params: Record<string, string>) => Promise<Record<string, unknown>>;

function quiet(ctx: StepContext, positionals: string[], flags: Map<string, string | boolean> = new Map(), repeated: Map<string, string[]> = new Map()): ParsedArgs {
  return { positionals, flags, repeated, global: { ...ctx.args.global, output: 'json', dryRun: false } };
}

function codeOf(err: unknown): string | undefined {
  return err instanceof BatonError ? err.code : undefined;
}



















export const TEST_OUTPUT_TAIL = 4096;

const ENROLL_REFUSALS = new Set([
  'APPLICATION_PENDING',
  'ALREADY_JOINED',
  'NETWORK_UNREACHABLE',
  'JOIN_REQUEST_STATE',
  'ADMISSION_CLOSED',
  'RATE_LIMITED',
]);




function servedWorkspace(ctx: StepContext, named: string): Workspace {
  if (!ctx.workspace) {
    throw new Refused('this Provider serves no workspace (it was started without --workspace-dir)');
  }
  if (named !== ctx.workspace.id) {
    throw new Refused(`this Provider serves workspace ${ctx.workspace.id}, not ${named}`, { served: ctx.workspace.id });
  }
  return ctx.workspace;
}


function inside(ws: Workspace, path: string): string {
  const full = resolve(ws.dir, path);
  const rel = relative(ws.dir, full);
  if (rel === '' || rel.startsWith('..') || rel.startsWith(sep) || resolve(ws.dir, rel) !== full) {
    throw new Refused(`${path} is not inside the workspace`);
  }
  return full;
}

export function sha256Of(bytes: Buffer | string): string {
  return 'sha256:' + createHash('sha256').update(bytes).digest('hex');
}












function recordExecution(ws: Workspace, step: string, operationID: string): void {
  const file = join(ws.dir, '.baton', 'executions.log');
  mkdirSync(dirname(file), { recursive: true });
  const fd = openSync(file, 'a', 0o644);
  try {
    writeSync(fd, `${step} ${operationID}\n`);
    fsyncSync(fd);
  } finally {
    closeSync(fd);
  }
}

export const STEPS: Record<StepName, Handler> = {

  async capture(ctx, p) {
    mkdirSync(ctx.opDir, { recursive: true, mode: 0o700 });
    const archive = join(ctx.opDir, 'state.tar.gz');
    let code: number;
    try {
      code = await snapshot(quiet(ctx, ['snapshot', p.source!], new Map([['out', archive]])));
    } catch (err) {

      throw new Refused(err instanceof Error ? err.message : String(err), { code: codeOf(err) ?? null });
    }
    if (code !== ExitCode.OK || !existsSync(archive)) {
      throw new Refused(`the snapshot of ${p.source} did not complete (exit ${code})`);
    }
    return { source: p.source, archive, bytes: statSync(archive).size };
  },

  async place(ctx, p) {
    let plan: ReturnType<typeof cloneCreatePlan>;
    try {
      plan = cloneCreatePlan(ctx.args, p.source!, p.name!);
    } catch (err) {

      throw new Refused(err instanceof Error ? err.message : String(err), { code: codeOf(err) ?? null });
    }



    const before = process.env.BATON_DRIVER_NETWORK;
    if (plan.network) process.env.BATON_DRIVER_NETWORK = plan.network;
    let code: number;
    try {
      code = await nodeCreate(quiet(ctx, ['node', 'create', p.name!], plan.flags, plan.repeated), { operationId: ctx.operationID });
    } catch (err) {


      if (codeOf(err) === 'CONFLICT') {
        throw new Refused(err instanceof Error ? err.message : String(err), { code: 'CONFLICT' });
      }
      throw err;
    } finally {
      if (before === undefined) delete process.env.BATON_DRIVER_NETWORK;
      else process.env.BATON_DRIVER_NETWORK = before;
    }
    if (code !== ExitCode.OK) {

      throw new Error(`node create ${p.name} exited ${code} without saying how far it got`);
    }
    return { node: p.name, container: `baton-agent-${p.name}`, label: { [OPERATION_ID_LABEL]: ctx.operationID } };
  },

  async enroll(ctx, p) {
    let code: number;
    try {
      code = await joinNetwork(quiet(ctx, ['agent', 'join', p.name!]), p.name!, ctx.args.global.master);
    } catch (err) {
      const c = codeOf(err);
      if (c && ENROLL_REFUSALS.has(c)) {
        throw new Refused(
          c === 'JOIN_REQUEST_STATE'
            ? `an application by ${p.name} is already pending, and it is not this operation's — ` +
              'a previous one by the same name is in the way; it has to be admitted, denied or expire first'
            : (err as Error).message,
          { code: c },
        );
      }
      throw err;
    }
    if (code !== ExitCode.OK) throw new Error(`agent join ${p.name} exited ${code} without saying how far it got`);




    const dataDir = ctx.args.global.dataDir ?? defaultDataDir();
    const requestID = officeOfAgent(ctx.args, dataDir, p.name!).applied_request_id;
    if (!requestID) {
      throw new Error(`${p.name} applied, and the id of its application could not be read back`);
    }
    return { node: p.name, request_id: requestID };
  },

  async materialize(ctx, p) {
    const captured = ctx.log.lookup(p.archive_operation_id!);
    const archive = captured.state === 'result' && captured.result.status === 'succeeded' ? captured.result.evidence.archive : undefined;
    if (typeof archive !== 'string' || !existsSync(archive)) {
      throw new Refused(`no captured archive for ${p.archive_operation_id} — capture did not succeed here, or its archive is gone`, {
        archive_operation_id: p.archive_operation_id,
      });
    }




    const code = await restore(quiet(ctx, ['restore', p.name!], new Map<string, string | boolean>([['from', archive], ['yes', true]])));
    if (code !== ExitCode.OK) throw new Error(`restore into ${p.name} exited ${code} without saying how far it got`);
    return { node: p.name, from_operation_id: p.archive_operation_id };
  },








  async 'file.write'(ctx, p) {
    const ws = servedWorkspace(ctx, p.workspace!);
    const file = inside(ws, p.path!);
    if (!existsSync(file)) throw new Refused(`${p.path} does not exist in ${ws.id}`, { path: p.path });
    const before = sha256Of(readFileSync(file));
    if (before !== p.expected_sha_before) {
      throw new Refused(`${p.path} is not the file the proposal was written against`, {
        path: p.path, expected_sha_before: p.expected_sha_before, actual_sha: before,
      });
    }
    recordExecution(ws, 'file.write', ctx.operationID);


    const tmp = `${file}.baton-${ctx.operationID}`;
    const fd = openSync(tmp, 'w', statSync(file).mode & 0o777);
    try {
      writeSync(fd, p.content!);
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    renameSync(tmp, file);
    return { path: p.path, sha_before: before, sha_after: sha256Of(p.content!) };
  },







  async 'test.run'(ctx, p) {
    const ws = servedWorkspace(ctx, p.workspace!);
    const file = inside(ws, p.path!);
    if (!existsSync(file)) throw new Refused(`${p.path} does not exist in ${ws.id}`, { path: p.path });
    if (!p.path!.endsWith('.py')) throw new Refused(`${p.path}: this Provider runs python test files (*.py) only`, { path: p.path });
    recordExecution(ws, 'test.run', ctx.operationID);
    const run = spawnSync('python3', [file], {
      cwd: ws.dir, encoding: 'utf8', timeout: 5 * 60_000,
      env: { ...process.env, BATON_OPERATION_ID: ctx.operationID },
    });






    mkdirSync(ctx.opDir, { recursive: true, mode: 0o700 });
    const stdout = run.stdout ?? '';
    const stderr = run.stderr ?? '';
    writeFileSync(join(ctx.opDir, 'stdout'), stdout, { mode: 0o600 });
    writeFileSync(join(ctx.opDir, 'stderr'), stderr, { mode: 0o600 });
    const tail = (s: string) => s.slice(-TEST_OUTPUT_TAIL);
    if (run.error || run.status === null) {

      throw new Error(`the test did not finish: ${run.error?.message ?? `signal ${run.signal}`}`);
    }
    const evidence = {
      path: p.path, exit_code: run.status,
      stdout_tail: tail(stdout), stdout_chars: stdout.length,
      stderr_tail: tail(stderr), stderr_chars: stderr.length,
      output_dir: ctx.opDir,
    };
    if (run.status !== 0) throw new Failed(`${p.path} failed (exit ${run.status})`, evidence);
    return evidence;
  },




















  async provision(ctx, p) {
    if (p.placement !== 'prov_local') {


      throw new Refused(`this Provider places on prov_local, not ${p.placement}`, { placement: p.placement });
    }
    const flags = new Map<string, string | boolean>([['template', p.harness!]]);
    let code: number;
    try {
      code = await nodeCreate(quiet(ctx, ['node', 'create', p.name!], flags), { operationId: ctx.operationID });
    } catch (err) {


      const c = codeOf(err);
      if (c === 'CONFLICT' || c === 'PRECONDITION' || c === 'NOT_FOUND') {
        throw new Refused(err instanceof Error ? err.message : String(err), { code: c });
      }
      throw err;
    }
    if (code !== ExitCode.OK) {

      throw new Error(`node create ${p.name} exited ${code} without saying how far it got`);
    }
    const container = `baton-agent-${p.name}`;
    return {
      workspace_id: p.name, agent_id: p.name, placement: p.placement,
      container, harness: p.harness,

      ...observed(container),
      label: { [OPERATION_ID_LABEL]: ctx.operationID },
    };
  },












  async 'runtime.read'(_ctx, p) {
    const container = `baton-agent-${p.workspace}`;
    return { workspace: p.workspace, container, ...observed(container) };
  },
};
























export function observed(container: string): {
  state: string;
  state_source: string;
  runtime_status: string;
  runtime_status_source: string;
} {
  const live = containerInventory(container);
  if (live.length === 0) {
    return {
      state: 'unknown',
      state_source: SOURCE_LIVE,
      runtime_status: 'unknown',
      runtime_status_source: SOURCE_NONE,
    };
  }
  const first = live[0]!;
  return {
    state: first.running ? 'running' : 'stopped',
    state_source: SOURCE_LIVE,
    runtime_status: 'unknown',
    runtime_status_source: SOURCE_NONE,
  };
}























const SOURCE_LIVE = 'provider:live-engine';
const SOURCE_NONE = 'not-observable-here';
