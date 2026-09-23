// SPDX-License-Identifier: Apache-2.0










import { randomBytes } from 'node:crypto';
import { chmodSync, closeSync, existsSync, fsyncSync, mkdirSync, openSync, readFileSync, statSync, unlinkSync, writeSync } from 'node:fs';
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { connect } from 'node:net';
import { basename, join, resolve } from 'node:path';
import type { ParsedArgs } from '../args.js';
import { defaultDataDir } from '../api/client.js';
import { preconditionError } from '../errors.js';
import { flagString } from '../args.js';
import { OperationLog, instanceIdAt as sharedInstanceIdAt } from './log.js';
import { ExecuteRequest, OperationID, StepParams, type Answer, type GovernanceFact, type NotFound, type ProtocolError, type ReceivedNoResult, type StepName } from './protocol.js';
import { EnvironmentFactsRequest, environmentFacts } from './environment.js';
import { Failed, Refused, STEPS, type StepContext, type Workspace } from './steps.js';











export const MAX_SOCKET_PATH_BYTES = 107;

export interface ProviderPaths {

  root: string;

  run: string;
  socket: string;
  log: string;
  ops: string;

  instance: string;






  requests: string;
}

export function providerPaths(dataDir: string): ProviderPaths {
  const root = join(resolve(dataDir), 'provider');
  const run = join(root, 'run');
  return { root, run, socket: join(run, 'provider.sock'), log: join(root, 'operations.jsonl'), ops: join(root, 'ops'), instance: join(root, 'instance-id'), requests: join(root, 'requests.log') };
}

export function assertSocketPath(path: string): void {
  const n = Buffer.byteLength(path);
  if (n > MAX_SOCKET_PATH_BYTES) {
    throw preconditionError(
      `the Provider's socket path is ${n} bytes, and a unix socket path can be at most ${MAX_SOCKET_PATH_BYTES}: ${path}`,
      'Use a shorter --data-dir. Node would bind a truncated path and report success, and Core would never find it.',
    );
  }
}










export function instanceIdAt(paths: ProviderPaths): string {


  try {
    return sharedInstanceIdAt(paths.instance, 'pi');
  } catch (err) {
    throw preconditionError(err instanceof Error ? err.message : String(err), 'Do not edit it; it names the log beside it.');
  }
}


export class Provider {
  private queue: Promise<unknown> = Promise.resolve();


















  private readonly inFlight = new Map<string, number>();

  constructor(
    private readonly log: OperationLog,
    private readonly args: ParsedArgs,
    private readonly paths: ProviderPaths,
    private readonly steps: typeof STEPS = STEPS,
    readonly instanceID: string = 'pi_' + '0'.repeat(32),
    private readonly workspace?: Workspace,
  ) {}





  execute(operationID: string, body: unknown): Promise<{ status: number; body: Answer | ProtocolError }> {
    if (!OperationID.safeParse(operationID).success) return Promise.resolve(bad('INVALID_OPERATION_ID', `not an operation_id: ${operationID}`));





    this.noteRequest('received', operationID);
    this.inFlight.set(operationID, (this.inFlight.get(operationID) ?? 0) + 1);

    const run = Promise.resolve(body).then((b) => {
      const r = this.queue.then(() => this.executeNow(operationID, b));
      this.queue = r.catch(() => undefined);
      return r;
    });
    return run.finally(() => {
      const n = (this.inFlight.get(operationID) ?? 1) - 1;
      if (n > 0) this.inFlight.set(operationID, n);
      else this.inFlight.delete(operationID);
    });
  }

  query(operationID: string): { status: number; body: Answer | NotFound | ReceivedNoResult | ProtocolError } {
    if (!OperationID.safeParse(operationID).success) return bad('INVALID_OPERATION_ID', `not an operation_id: ${operationID}`);
    const rec = this.log.lookup(operationID);


    if (rec.state === 'absent' && this.inFlight.has(operationID)) {
      return { status: 200, body: { status: 'received_no_result', operation_id: operationID, provider_instance_id: this.instanceID } };
    }
    if (rec.state === 'absent') {

      return { status: 200, body: { status: 'not_found', operation_id: operationID, provider_instance_id: this.instanceID } };
    }
    return { status: 200, body: this.fromLog(rec) };
  }













  environment(body: unknown): { status: number; body: { facts: GovernanceFact[] } | ProtocolError } {
    const req = EnvironmentFactsRequest.safeParse(body);
    if (!req.success) return bad('INVALID_REQUEST', req.error.issues.map((i) => `${i.path.join('.')}: ${i.message}`).join('; '));
    try {
      return { status: 200, body: { facts: environmentFacts(req.data, this.instanceID, () => new Date(), this.workspace, this.args.global.dataDir ?? defaultDataDir()) } };
    } catch (err) {
      return bad('UNRESOLVABLE', `the path could not be resolved on this side: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  private async executeNow(operationID: string, body: unknown): Promise<{ status: number; body: Answer | ProtocolError }> {
    const req = ExecuteRequest.safeParse(body);
    if (!req.success) return bad('INVALID_REQUEST', req.error.issues.map((i) => `${i.path.join('.')}: ${i.message}`).join('; '));
    const params = StepParams[req.data.step].safeParse(req.data.params);
    if (!params.success) return bad('INVALID_REQUEST', params.error.issues.map((i) => `params.${i.path.join('.')}: ${i.message}`).join('; '));

    const rec = this.log.lookup(operationID);
    if (rec.state !== 'absent') {
      if (rec.intent.step !== req.data.step) {


        return {
          status: 409,
          body: { code: 'OPERATION_ID_REUSED', message: `${operationID} was recorded as ${rec.intent.step}, not ${req.data.step}` },
        };
      }







      this.noteRequest('deduplicated', operationID, rec.state);
      return { status: 200, body: this.fromLog(rec) };
    }

    const nonce = req.data.nonce;
    this.log.recordIntent({ operation_id: operationID, step: req.data.step, nonce, at: now() });
    const ctx: StepContext = { args: this.args, log: this.log, operationID, opDir: join(this.paths.ops, operationID), workspace: this.workspace };
    const answer = (status: Answer['status'], evidence: Record<string, unknown>): Answer => ({
      operation_id: operationID, step: req.data.step, status, nonce, evidence, answered_from: 'execution',
      provider_instance_id: this.instanceID,
    });
    try {
      const evidence = await this.steps[req.data.step](ctx, params.data as Record<string, string>);
      this.log.recordResult({ operation_id: operationID, status: 'succeeded', nonce, evidence, at: now() });
      return { status: 200, body: answer('succeeded', evidence) };
    } catch (err) {
      if (err instanceof Refused || err instanceof Failed) {
        const evidence = { reason: err.message, ...err.evidence };
        this.log.recordResult({ operation_id: operationID, status: 'failed', nonce, evidence, at: now() });
        return { status: 200, body: answer('failed', evidence) };
      }


      return {
        status: 200,
        body: answer('unknown', { reason: 'the step stopped partway, and whether it changed anything is not known', error: err instanceof Error ? err.message : String(err) }),
      };
    }
  }
















  private noteRequest(kind: 'received' | 'deduplicated', operationID: string, detail?: string): void {
    mkdirSync(this.paths.root, { recursive: true, mode: 0o700 });
    const fd = openSync(this.paths.requests, 'a', 0o600);
    try {
      writeSync(fd, `${kind} ${operationID}${detail ? ' ' + detail : ''}\n`);
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
  }

  private fromLog(rec: Exclude<ReturnType<OperationLog['lookup']>, { state: 'absent' }>): Answer {
    if (rec.state === 'result') {
      return {
        operation_id: rec.intent.operation_id, step: rec.intent.step as StepName, status: rec.result.status,
        nonce: rec.result.nonce, evidence: rec.result.evidence, answered_from: 'log', provider_instance_id: this.instanceID,
      };
    }
    return {
      operation_id: rec.intent.operation_id, step: rec.intent.step as StepName, status: 'unknown', nonce: rec.intent.nonce,
      evidence: {
        reason: 'this Provider recorded starting this operation and no result: it was interrupted. ' +
          'Asking the world (label · application · generation) is reconciliation, which this build does not do yet',
      },
      answered_from: 'log',
      provider_instance_id: this.instanceID,
    };
  }
}

function bad(code: string, message: string): { status: number; body: ProtocolError } {
  return { status: 400, body: { code, message } };
}

function now(): string {
  return new Date().toISOString();
}

async function readJSON(req: IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const c of req) {
    size += (c as Buffer).length;
    if (size > 1 << 20) throw new Error('request body over 1 MiB');
    chunks.push(c as Buffer);
  }
  return JSON.parse(Buffer.concat(chunks).toString('utf8') || 'null');
}

function send(res: ServerResponse, status: number, body: unknown): void {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(body) + '\n');
}


function answering(path: string): Promise<boolean> {
  return new Promise((done) => {
    const c = connect(path);
    c.once('connect', () => { c.destroy(); done(true); });
    c.once('error', () => done(false));
  });
}


export function createProviderServer(provider: Provider): Server {
  return createServer((req, res) => {
    if ((req.url ?? '') === '/v1/environment/facts') {
      if (req.method !== 'POST') {
        res.setHeader('Allow', 'POST');
        return send(res, 405, { code: 'METHOD_NOT_ALLOWED', message: `${req.method} ${req.url}` });
      }
      readJSON(req)
        .then((b) => { const r = provider.environment(b); send(res, r.status, r.body); })
        .catch((err: unknown) => send(res, 400, { code: 'INVALID_REQUEST', message: err instanceof Error ? err.message : String(err) }));
      return;
    }
    const m = /^\/v1\/operations\/([^/]+)$/.exec(req.url ?? '');
    if (!m) return send(res, 404, { code: 'NO_SUCH_ROUTE', message: `${req.method} ${req.url}` });
    const id = decodeURIComponent(m[1]!);
    if (req.method === 'GET') {
      const r = provider.query(id);
      return send(res, r.status, r.body);
    }
    if (req.method === 'POST') {


      provider.execute(id, readJSON(req))
        .then((r) => send(res, r.status, r.body))
        .catch((err: unknown) => send(res, 400, { code: 'INVALID_REQUEST', message: err instanceof Error ? err.message : String(err) }));
      return;
    }
    res.setHeader('Allow', 'GET, POST');
    return send(res, 405, { code: 'METHOD_NOT_ALLOWED', message: `${req.method} ${req.url}` });
  });
}

export async function serve(args: ParsedArgs): Promise<number> {
  const paths = providerPaths(args.global.dataDir ?? defaultDataDir());
  assertSocketPath(paths.socket);

  mkdirSync(paths.root, { recursive: true, mode: 0o700 });
  chmodSync(paths.root, 0o700);
  mkdirSync(paths.run, { recursive: true, mode: 0o755 });
  chmodSync(paths.run, 0o755);
  mkdirSync(paths.ops, { recursive: true, mode: 0o700 });

  if (existsSync(paths.socket)) {
    if (await answering(paths.socket)) {
      throw preconditionError(
        `a Provider is already serving on ${paths.socket}`,
        'One Provider per data directory: its log has one writer. Stop the other one first.',
      );
    }
    unlinkSync(paths.socket);
  }

  const { log, tornTail } = OperationLog.open(paths.log);
  if (tornTail) process.stderr.write(`provider: ${paths.log} ended in a torn line (a crash mid-write); it was cut off\n`);
  const instanceID = instanceIdAt(paths);
  const wsDir = flagString(args, 'workspace-dir');
  let workspace: Workspace | undefined;
  if (wsDir) {
    const dir = resolve(wsDir);
    if (!existsSync(dir) || !statSync(dir).isDirectory()) {
      throw preconditionError(`--workspace-dir ${wsDir} is not a directory`, 'Point it at the workspace this Provider serves.');
    }
    workspace = { id: basename(dir), dir };
  }
  const provider = new Provider(log, args, paths, STEPS, instanceID, workspace);

  const server = createProviderServer(provider);

  await new Promise<void>((ok, fail) => {
    server.once('error', fail);
    server.listen(paths.socket, () => ok());
  });



  chmodSync(paths.socket, 0o666);
  process.stderr.write(`provider: serving on ${paths.socket}\n  log ${paths.log} (instance ${instanceID})\n` +
    (workspace ? `  workspace ${workspace.id} at ${workspace.dir}\n` : '') +
    `  mount ${paths.run} into Core's container, and nowhere else\n`);

  await new Promise<void>((done) => {
    const stop = () => server.close(() => { log.close(); done(); });
    process.once('SIGTERM', stop);
    process.once('SIGINT', stop);
  });
  return 0;
}
