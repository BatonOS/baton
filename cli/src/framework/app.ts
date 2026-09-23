// SPDX-License-Identifier: Apache-2.0



















import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { z } from 'zod';

import { OperationLog, type Recorded } from '../provider/log.js';



export { OperationLog, instanceIdAt } from '../provider/log.js';
import { OperationID } from '../provider/protocol.js';



export { Failed, Refused } from '../provider/steps.js';
import { Failed, Refused } from '../provider/steps.js';














export interface StepContext {





  readonly operationID: string;

  readonly subject: string;

  readonly input: Record<string, unknown>;
}









export type Execute = (ctx: StepContext) => Promise<Record<string, unknown>>;







































export type Status = 'succeeded' | 'failed' | 'unknown';



























export interface Answer {
  operation_id: string;
  step: string;
  status: Status;
  nonce: string;
  evidence: Record<string, unknown>;
  answered_from: 'execution' | 'log';
  provider_instance_id: string;
}

export interface NotFound {
  status: 'not_found';
  operation_id: string;
  provider_instance_id: string;
}

export interface ReceivedNoResult {
  status: 'received_no_result';
  operation_id: string;
  provider_instance_id: string;
}

export interface ProtocolError {
  code: string;
  message: string;
}









const ExecuteRequest = z.object({
  step: z.string().min(1),
  nonce: z.string().min(1),






  subject: z.string(),
  params: z.record(z.unknown()),
}).strict();

function bad(code: string, message: string): { status: number; body: ProtocolError } {
  return { status: 400, body: { code, message } };
}





export class AppExecutor {














  private readonly inFlight = new Map<string, number>();
  private queue: Promise<unknown> = Promise.resolve();

  constructor(
    private readonly log: OperationLog,
    private readonly handlers: Record<string, Execute>,
    readonly instanceID: string,
  ) {}









  stepNames(): { status: number; body: { steps: string[] } } {
    return { status: 200, body: { steps: Object.keys(this.handlers).sort() } };
  }

  execute(operationID: string, body: unknown): Promise<{ status: number; body: Answer | ProtocolError }> {
    if (!OperationID.safeParse(operationID).success) {
      return Promise.resolve(bad('INVALID_OPERATION_ID', `not an operation_id: ${operationID}`));
    }
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
    if (!OperationID.safeParse(operationID).success) {
      return bad('INVALID_OPERATION_ID', `not an operation_id: ${operationID}`);
    }
    const rec = this.log.lookup(operationID);
    if (rec.state === 'absent' && this.inFlight.has(operationID)) {
      return { status: 200, body: { status: 'received_no_result', operation_id: operationID, provider_instance_id: this.instanceID } };
    }
    if (rec.state === 'absent') {



      return { status: 200, body: { status: 'not_found', operation_id: operationID, provider_instance_id: this.instanceID } };
    }
    return { status: 200, body: this.fromLog(rec) };
  }

  private async executeNow(operationID: string, body: unknown): Promise<{ status: number; body: Answer | ProtocolError }> {
    const req = ExecuteRequest.safeParse(body);
    if (!req.success) {
      return bad('INVALID_REQUEST', req.error.issues.map((i) => `${i.path.join('.')}: ${i.message}`).join('; '));
    }
    const handler = this.handlers[req.data.step];
    if (!handler) {


      return { status: 404, body: { code: 'NO_SUCH_STEP', message: `this App handles ${Object.keys(this.handlers).sort().join(', ') || '(none)'}, ⊘ ${req.data.step}` } };
    }



    const prior = this.log.lookup(operationID);
    if (prior.state !== 'absent') {
      if (prior.intent.step !== req.data.step) {
        return { status: 409, body: { code: 'OPERATION_ID_REUSED', message: `${operationID} was recorded as ${prior.intent.step}, ⊘ ${req.data.step}` } };
      }



      return { status: 200, body: this.fromLog(prior) };
    }






    let intent;
    try {
      intent = this.log.recordIntent({ operation_id: operationID, step: req.data.step, nonce: req.data.nonce, at: new Date().toISOString() });
    } catch (err) {





      return { status: 500, body: { code: 'LOG_REFUSED_INTENT', message: err instanceof Error ? err.message : String(err) } };
    }

    const ctx: StepContext = { operationID, subject: req.data.subject, input: req.data.params };
    const now = () => new Date().toISOString();
    try {
      const evidence = await handler(ctx);
      const result = this.log.recordResult({ operation_id: operationID, status: 'succeeded', nonce: req.data.nonce, evidence, at: now() });
      return { status: 200, body: this.answer(intent, result, 'execution') };
    } catch (err) {
      if (err instanceof Refused || err instanceof Failed) {


        const result = this.log.recordResult({
          operation_id: operationID, status: 'failed', nonce: req.data.nonce,
          evidence: { ...err.evidence, reason: err.message, touched_the_world: err instanceof Failed }, at: now(),
        });
        return { status: 200, body: this.answer(intent, result, 'execution') };
      }













      return {
        status: 200,
        body: {
          operation_id: operationID, step: req.data.step, status: 'unknown', nonce: req.data.nonce,
          evidence: { reason: err instanceof Error ? err.message : String(err) },
          answered_from: 'execution', provider_instance_id: this.instanceID,
        },
      };
    }
  }

  private fromLog(rec: Recorded): Answer {
    if (rec.state === 'intent-only') {


      return {
        operation_id: rec.intent.operation_id, step: rec.intent.step, status: 'unknown',
        nonce: rec.intent.nonce, evidence: { reason: 'this App recorded the intent and no result; it did not finish' },
        answered_from: 'log', provider_instance_id: this.instanceID,
      };
    }
    if (rec.state === 'absent') throw new Error('framework: fromLog called with nothing recorded');
    return this.answer(rec.intent, rec.result, 'log');
  }

  private answer(intent: { operation_id: string; step: string; nonce: string }, result: { status: 'succeeded' | 'failed'; evidence: Record<string, unknown> }, from: 'execution' | 'log'): Answer {
    return {
      operation_id: intent.operation_id, step: intent.step,
      status: result.status,



      nonce: intent.nonce,
      evidence: result.evidence, answered_from: from, provider_instance_id: this.instanceID,
    };
  }
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


export function createAppServer(app: AppExecutor): Server {
  return createServer((req, res) => {
    if ((req.url ?? '') === '/v1/steps') {
      if (req.method !== 'GET') {
        res.setHeader('Allow', 'GET');
        return send(res, 405, { code: 'METHOD_NOT_ALLOWED', message: `${req.method} ${req.url}` });
      }
      const r = app.stepNames();
      return send(res, r.status, r.body);
    }
    const m = /^\/v1\/operations\/([^/]+)$/.exec(req.url ?? '');
    if (!m) return send(res, 404, { code: 'NO_SUCH_ROUTE', message: `${req.method} ${req.url}` });
    const id = decodeURIComponent(m[1]!);
    if (req.method === 'GET') {
      const r = app.query(id);
      return send(res, r.status, r.body);
    }
    if (req.method === 'POST') {
      app.execute(id, readJSON(req))
        .then((r) => send(res, r.status, r.body))
        .catch((err: unknown) => send(res, 400, { code: 'INVALID_REQUEST', message: err instanceof Error ? err.message : String(err) }));
      return;
    }
    res.setHeader('Allow', 'GET, POST');
    return send(res, 405, { code: 'METHOD_NOT_ALLOWED', message: `${req.method} ${req.url}` });
  });
}
