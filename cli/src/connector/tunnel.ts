// SPDX-License-Identifier: Apache-2.0


























import type { Duplex } from 'node:stream';

const LINE_CAP = 4096;










class LineChannel {
  private buf = Buffer.alloc(0);
  private waiting: Array<{ resolve: (l: string) => void; reject: (e: Error) => void }> = [];
  private failed: Error | undefined;
  private readonly onData = (c: Buffer) => {
    this.buf = Buffer.concat([this.buf, c]);
    this.pump();
  };
  private readonly onEnd = () => this.fail(new Error('connection closed'));

  constructor(private readonly sock: Duplex) {
    sock.on('data', this.onData);
    sock.on('error', this.onEnd);
    sock.on('close', this.onEnd);
  }

  private fail(e: Error): void {
    this.failed ??= e;
    this.pump();
  }

  private pump(): void {
    while (this.waiting.length && !this.failed) {
      const nl = this.buf.indexOf(0x0a);
      if (nl === -1) {
        if (this.buf.length > LINE_CAP) this.fail(new Error('line too long'));
        break;
      }
      const line = this.buf.subarray(0, nl).toString('utf8');
      this.buf = this.buf.subarray(nl + 1);
      this.waiting.shift()!.resolve(line);
    }
    if (this.failed) for (const w of this.waiting.splice(0)) w.reject(this.failed);
  }

  nextLine(): Promise<string> {
    return new Promise((resolve, reject) => {
      this.waiting.push({ resolve, reject });
      this.pump();
    });
  }


  detach(): void {
    this.sock.off('data', this.onData);
    this.sock.off('error', this.onEnd);
    this.sock.off('close', this.onEnd);
    if (this.buf.length) (this.sock as Duplex & { unshift(c: Buffer): void }).unshift(this.buf);
    this.buf = Buffer.alloc(0);
  }
}

export interface TunnelOptions {

  ws: string;

  bearer: string;

  dial: () => Promise<Duplex>;

  onData: (conn: Duplex, client?: string) => void;
  log: (line: string) => void;

  backoffMs?: (attempt: number) => number;

  pingIntervalMs?: number;
}

async function expectOk(ch: LineChannel, after: string): Promise<void> {
  const line = await ch.nextLine();
  if (line !== 'OK') throw new Error(`broker answered ${after} with: ${line}`);
}

export function startTunnel(opts: TunnelOptions): { stop: () => void } {
  let stopped = false;
  let attempt = 0;
  let current: Duplex | undefined;
  let pinger: NodeJS.Timeout | undefined;
  const backoff = opts.backoffMs ?? ((n: number) => Math.min(30_000, 500 * 2 ** Math.min(n, 6)));

  async function openData(token: string, client?: string): Promise<void> {
    const conn = await opts.dial();
    const ch = new LineChannel(conn);
    try {
      conn.write(`AUTH ${opts.bearer}\n`);
      await expectOk(ch, 'AUTH');
      conn.write(`DATA ${token}\n`);
      await expectOk(ch, 'DATA');
    } catch (e) {
      conn.destroy();
      opts.log(`data dial refused: ${(e as Error).message}`);
      return;
    }
    ch.detach();
    opts.onData(conn, client);
  }

  async function runControl(): Promise<void> {
    while (!stopped) {
      let conn: Duplex | undefined;
      try {
        conn = await opts.dial();
        current = conn;
        const ch = new LineChannel(conn);
        conn.write(`AUTH ${opts.bearer}\n`);
        await expectOk(ch, 'AUTH');
        conn.write(`REGISTER ${opts.ws}\n`);
        await expectOk(ch, 'REGISTER');
        attempt = 0;
        opts.log(`registered ${opts.ws} with the rendezvous broker`);
        if (opts.pingIntervalMs !== 0) {
          pinger = setInterval(() => conn?.write('PING\n'), opts.pingIntervalMs ?? 30_000);
        }
        for (;;) {
          const line = await ch.nextLine();
          const parts = line.split(' ');
          if (parts[0] === 'PING') {
            conn.write('PONG\n');
          } else if (parts[0] === 'PONG') {

          } else if (parts[0] === 'OPEN' && parts[1] === opts.ws && parts[2]) {
            const client = parts[3]?.startsWith('client=') ? parts[3].slice('client='.length) : undefined;
            void openData(parts[2], client);
          } else {



            opts.log(`unexpected control frame: ${line}`);
          }
        }
      } catch (e) {
        if (!stopped) opts.log(`control connection lost: ${(e as Error).message}`);
      } finally {
        if (pinger) clearInterval(pinger);
        pinger = undefined;
        conn?.destroy();
        current = undefined;
      }
      if (stopped) return;
      attempt += 1;
      await new Promise((r) => setTimeout(r, backoff(attempt)));
    }
  }

  void runControl();

  return {
    stop: () => {
      stopped = true;
      if (pinger) clearInterval(pinger);
      current?.write('BYE\n');
      current?.destroy();
    },
  };
}
