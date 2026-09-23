// SPDX-License-Identifier: Apache-2.0

















import { createHash, randomBytes } from 'node:crypto';
import { request as httpsRequest } from 'node:https';
import type { IncomingMessage } from 'node:http';
import type { Duplex } from 'node:stream';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const WS_GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';

function acceptKey(key: string): string {
  return createHash('sha1').update(key + WS_GUID).digest('base64');
}


interface WsFrame { fin: boolean; opcode: number; payload: Buffer }


class FrameParser {
  private buf = Buffer.alloc(0);
  push(chunk: Buffer, emit: (f: WsFrame) => void): void {
    this.buf = Buffer.concat([this.buf, chunk]);
    for (;;) {
      if (this.buf.length < 2) return;
      const b0 = this.buf[0]!, b1 = this.buf[1]!;
      const fin = (b0 & 0x80) !== 0;
      const opcode = b0 & 0x0f;
      const masked = (b1 & 0x80) !== 0;
      let len = b1 & 0x7f;
      let off = 2;
      if (len === 126) { if (this.buf.length < 4) return; len = this.buf.readUInt16BE(2); off = 4; }
      else if (len === 127) { if (this.buf.length < 10) return; len = Number(this.buf.readBigUInt64BE(2)); off = 10; }
      const maskLen = masked ? 4 : 0;
      if (this.buf.length < off + maskLen + len) return;
      let payload: Buffer;
      if (masked) {
        const mask = this.buf.subarray(off, off + 4);
        payload = Buffer.alloc(len);
        for (let i = 0; i < len; i++) payload[i] = this.buf[off + 4 + i]! ^ mask[i % 4]!;
      } else {
        payload = this.buf.subarray(off + maskLen, off + maskLen + len);
      }
      this.buf = this.buf.subarray(off + maskLen + len);
      emit({ fin, opcode, payload });
    }
  }
}


function buildFrame(opcode: number, payload: Buffer, mask: boolean): Buffer {
  const len = payload.length;
  const head: number[] = [0x80 | opcode];
  let lenBytes: Buffer;
  if (len < 126) { head.push((mask ? 0x80 : 0) | len); lenBytes = Buffer.alloc(0); }
  else if (len < 65536) { head.push((mask ? 0x80 : 0) | 126); lenBytes = Buffer.alloc(2); lenBytes.writeUInt16BE(len); }
  else { head.push((mask ? 0x80 : 0) | 127); lenBytes = Buffer.alloc(8); lenBytes.writeBigUInt64BE(BigInt(len)); }
  if (!mask) return Buffer.concat([Buffer.from(head), lenBytes, payload]);
  const m = randomBytes(4);
  const masked = Buffer.alloc(len);
  for (let i = 0; i < len; i++) masked[i] = payload[i]! ^ m[i % 4]!;
  return Buffer.concat([Buffer.from(head), lenBytes, m, masked]);
}






export function handlePtyUpgrade(
  req: IncomingMessage,
  socket: Duplex,
  opts: { master: URL; adminDir: string; isValid: (t: string) => boolean },
): void {
  const url = new URL(req.url ?? '/', 'http://x');

  const protos = (req.headers['sec-websocket-protocol'] ?? '').split(',').map((s) => s.trim());
  const bearer = protos.find((p) => p.startsWith('bearer.'))?.slice('bearer.'.length) ?? '';
  if (!protos.includes('baton.pty.v1') || !opts.isValid(bearer)) {
    socket.write('HTTP/1.1 401 Unauthorized\r\n\r\n');
    socket.destroy();
    return;
  }
  const wsKey = req.headers['sec-websocket-key'] ?? '';
  const agent = url.searchParams.get('agent') ?? '';
  const mode = url.searchParams.get('mode') === 'takeover' ? 'takeover' : 'read';














  const masterKey = randomBytes(16).toString('base64');
  const mreq = httpsRequest({
    hostname: opts.master.hostname,
    port: opts.master.port,
    path: `/api/v1alpha1/pty?agent=${encodeURIComponent(agent)}&mode=${mode}`,
    method: 'GET',
    headers: {
      Connection: 'Upgrade',
      Upgrade: 'websocket',
      'Sec-WebSocket-Version': '13',
      'Sec-WebSocket-Key': masterKey,
      'Sec-WebSocket-Protocol': 'baton.pty.v1',
    },
    cert: readFileSync(join(opts.adminDir, 'admin.crt')),
    key: readFileSync(join(opts.adminDir, 'admin.key')),
    ca: readFileSync(join(opts.adminDir, 'ca.crt')),
    rejectUnauthorized: false,
  });

  mreq.on('upgrade', (_res, mSocket) => {

    socket.write(
      'HTTP/1.1 101 Switching Protocols\r\n' +
        'Upgrade: websocket\r\n' +
        'Connection: Upgrade\r\n' +
        `Sec-WebSocket-Accept: ${acceptKey(wsKey)}\r\n` +
        'Sec-WebSocket-Protocol: baton.pty.v1\r\n\r\n',
    );

    const fromBrowser = new FrameParser();
    const fromMaster = new FrameParser();
    const close = () => { try { socket.destroy(); } catch {  } try { mSocket.destroy(); } catch {  } };

    socket.on('data', (c: Buffer) => fromBrowser.push(c, (f) => {
      if (f.opcode === 0x8) { close(); return; }

      mSocket.write(buildFrame(f.opcode, f.payload, true));
    }));
    mSocket.on('data', (c: Buffer) => fromMaster.push(c, (f) => {
      if (f.opcode === 0x8) { close(); return; }

      socket.write(buildFrame(f.opcode, f.payload, false));
    }));
    socket.on('close', close);
    mSocket.on('close', close);
    socket.on('error', close);
    mSocket.on('error', close);
  });





















  mreq.on('response', (mres) => {
    const status = `HTTP/1.1 ${mres.statusCode ?? 502} ${mres.statusMessage ?? ''}\r\n`;
    const type = mres.headers['content-type'];
    socket.write(status + (type ? `content-type: ${type}\r\n` : '') + 'connection: close\r\n\r\n');
    mres.on('data', (chunk: Buffer) => socket.write(chunk));
    mres.on('end', () => socket.end());
    mres.on('error', () => socket.destroy());
  });
  mreq.on('error', () => {
    socket.write('HTTP/1.1 502 Bad Gateway\r\n\r\n');
    socket.destroy();
  });
  mreq.end();
}
