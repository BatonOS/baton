// SPDX-License-Identifier: Apache-2.0

























import * as nodeFs from 'node:fs';
import { randomBytes } from 'node:crypto';
import { z } from 'zod';


export interface LogFs {
  existsSync(path: string): boolean;
  readFileSync(path: string, encoding: 'utf8'): string;
  openSync(path: string, flags: string, mode?: number): number;
  writeSync(fd: number, data: string): number;
  fsyncSync(fd: number): void;
  closeSync(fd: number): void;
  truncateSync(path: string, len: number): void;
}

const realFs: LogFs = {
  existsSync: nodeFs.existsSync,
  readFileSync: (p, e) => nodeFs.readFileSync(p, e),
  openSync: (p, f, m) => nodeFs.openSync(p, f, m),
  writeSync: (fd, d) => nodeFs.writeSync(fd, d),
  fsyncSync: nodeFs.fsyncSync,
  closeSync: nodeFs.closeSync,
  truncateSync: (p, l) => nodeFs.truncateSync(p, l),
};

const OPERATION_ID = z.string().regex(/^op_[0-9a-f]{32}$/);

export const IntentEntry = z.object({
  kind: z.literal('intent'),
  operation_id: OPERATION_ID,
  step: z.string().min(1),
  nonce: z.string().min(1),
  at: z.string(),
}).strict();

export const ResultEntry = z.object({
  kind: z.literal('result'),
  operation_id: OPERATION_ID,
  status: z.enum(['succeeded', 'failed']),
  nonce: z.string().min(1),
  evidence: z.record(z.unknown()),
  at: z.string(),
}).strict();

const Entry = z.discriminatedUnion('kind', [IntentEntry, ResultEntry]);

export type Intent = z.infer<typeof IntentEntry>;
export type Result = z.infer<typeof ResultEntry>;


export type Recorded =
  | { state: 'absent' }
  | { state: 'intent-only'; intent: Intent }
  | { state: 'result'; intent: Intent; result: Result };

export class LogCorrupt extends Error {}

export class OperationLog {
  private readonly intents = new Map<string, Intent>();
  private readonly results = new Map<string, Result>();
  private fd: number | undefined;

  private constructor(private readonly path: string, private readonly fs: LogFs) {}










  static open(path: string, fs: LogFs = realFs): { log: OperationLog; tornTail: boolean } {
    const log = new OperationLog(path, fs);
    let tornTail = false;
    if (fs.existsSync(path)) {
      const text = fs.readFileSync(path, 'utf8');


      const unterminated = text.length > 0 && !text.endsWith('\n');
      const lines = text.split('\n');
      const lastNonEmpty = lines.reduce((last, l, i) => (l.trim() ? i : last), -1);
      lines.forEach((line, i) => {
        if (!line.trim()) return;
        let parsed: z.SafeParseReturnType<unknown, z.infer<typeof Entry>>;
        try {
          parsed = Entry.safeParse(JSON.parse(line));
        } catch {
          parsed = { success: false } as typeof parsed;
        }
        if (!parsed.success) {
          if (i === lastNonEmpty && unterminated) {
            tornTail = true;
            return;
          }
          throw new LogCorrupt(`${path}: line ${i + 1} is not a log entry this Provider wrote`);
        }
        log.index(parsed.data);
      });



      if (tornTail) fs.truncateSync(path, Buffer.byteLength(text.slice(0, text.lastIndexOf('\n') + 1)));
    }
    log.fd = fs.openSync(path, 'a', 0o600);
    return { log, tornTail };
  }

  lookup(operationID: string): Recorded {
    const intent = this.intents.get(operationID);
    if (!intent) return { state: 'absent' };
    const result = this.results.get(operationID);
    return result ? { state: 'result', intent, result } : { state: 'intent-only', intent };
  }


  recordIntent(e: Omit<Intent, 'kind'>): Intent {
    const entry = IntentEntry.parse({ kind: 'intent', ...e });
    if (this.intents.has(entry.operation_id)) {
      throw new Error(`${entry.operation_id} already has an intent; an operation is attempted once`);
    }
    this.append(entry);
    this.index(entry);
    return entry;
  }


  recordResult(e: Omit<Result, 'kind'>): Result {
    const entry = ResultEntry.parse({ kind: 'result', ...e });
    if (!this.intents.has(entry.operation_id)) {
      throw new Error(`${entry.operation_id} has no intent; a result without one would say something ran that was never recorded as starting`);
    }
    if (this.results.has(entry.operation_id)) {
      throw new Error(`${entry.operation_id} already has a result`);
    }
    this.append(entry);
    this.index(entry);
    return entry;
  }

  close(): void {
    if (this.fd !== undefined) this.fs.closeSync(this.fd);
    this.fd = undefined;
  }

  private append(entry: Intent | Result): void {
    if (this.fd === undefined) throw new Error(`${this.path} is closed`);
    const line = JSON.stringify(entry) + '\n';
    const n = this.fs.writeSync(this.fd, line);
    if (n !== Buffer.byteLength(line)) {
      throw new Error(`${this.path}: short write (${n} of ${Buffer.byteLength(line)} bytes)`);
    }
    this.fs.fsyncSync(this.fd);
  }

  private index(entry: Intent | Result): void {
    if (entry.kind === 'intent') this.intents.set(entry.operation_id, entry);
    else this.results.set(entry.operation_id, entry);
  }
}














export function instanceIdAt(path: string, prefix: string): string {
  const shape = new RegExp(`^${prefix}_[0-9a-f]{32}$`);
  if (nodeFs.existsSync(path)) {
    const id = nodeFs.readFileSync(path, 'utf8').trim();
    if (!shape.test(id)) {
      throw new Error(`${path} is not an instance id this process wrote; do not edit it — it names the log beside it`);
    }
    return id;
  }
  const id = `${prefix}_` + randomBytes(16).toString('hex');
  const fd = nodeFs.openSync(path, 'wx', 0o600);
  try {
    nodeFs.writeSync(fd, id + '\n');
    nodeFs.fsyncSync(fd);
  } finally {
    nodeFs.closeSync(fd);
  }
  return id;
}
