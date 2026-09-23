// SPDX-License-Identifier: Apache-2.0































import { request as httpRequest, type IncomingMessage, type ServerResponse } from 'node:http';
import type { Duplex } from 'node:stream';
import type { KeyObject } from 'node:crypto';
import { NonceSet, verifyTicket } from './ticket.js';
import { SessionStore } from './session.js';

export interface FrontOptions {

  ws: string;

  issuerKey: KeyObject;

  panelRemoteFacePort: number;

  now?: () => number;
}

function refuse(res: ServerResponse, status: number, code: string, message: string, remediation: string): void {
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8' });
  res.end(JSON.stringify({ code, message, remediation }));
}


const MAX_ENTRY_BODY = 8 * 1024;


const POST_FORWARD = new Set(['/run', '/auth/login', '/auth/logout']);
const READ_METHODS = new Set(['GET', 'HEAD', 'OPTIONS']);

export function createFront(opts: FrontOptions): {
  listener: (req: IncomingMessage, res: ServerResponse) => void;
  onUpgrade: (req: IncomingMessage, socket: Duplex) => void;
} {
  const nonces = new NonceSet();
  const sessions = new SessionStore();
  const now = opts.now ?? (() => Math.floor(Date.now() / 1000));

  function entry(req: IncomingMessage, res: ServerResponse): void {
    if (req.method !== 'POST') {
      refuse(res, 405, 'METHOD_NOT_ALLOWED', `${req.method} is not used here`, 'Present a ticket with POST.');
      return;
    }
    let raw = '';
    req.on('data', (c: Buffer) => {
      raw += c;
      if (raw.length > MAX_ENTRY_BODY) req.destroy();
    });
    req.on('end', () => {


      let ticket = '';
      const ct = req.headers['content-type'] ?? '';
      if (ct.includes('application/json')) {
        try {
          ticket = String((JSON.parse(raw || '{}') as { ticket?: unknown }).ticket ?? '');
        } catch {

        }
      } else {
        ticket = new URLSearchParams(raw).get('ticket') ?? '';
      }
      const verdict = verifyTicket(ticket, { issuerKey: opts.issuerKey, ws: opts.ws, nonces, now: now() });
      if (!verdict.ok) {
        const why: Record<string, string> = {
          invalid_ticket: 'the ticket is not a currently valid one from this platform',
          not_owned: 'the ticket is for a different workspace',
          expired: 'the ticket has expired',
          already_used: 'the ticket was already used once',
        };
        refuse(res, 403, verdict.code, why[verdict.code] ?? verdict.code,
          'Ask the dashboard for a fresh ticket — each one opens a single entry within about a minute.');
        return;
      }
      res.writeHead(303, { 'set-cookie': sessions.mint(now()), location: '/' });
      res.end();
    });
  }

  function forward(req: IncomingMessage, res: ServerResponse): void {
    const path = (req.url ?? '/').split('?')[0] ?? '/';
    const method = req.method ?? 'GET';
    if (!READ_METHODS.has(method) && !(method === 'POST' && POST_FORWARD.has(path))) {
      refuse(res, 403, 'REMOTE_READ_ONLY', `${method} ${path} does not travel through the remote face`,
        'Writes go through `baton` with your operator certificate.');
      return;
    }
    const headers = { ...req.headers };
    delete headers.cookie;
    delete headers.origin;
    delete headers.referer;
    delete headers.host;
    const up = httpRequest(
      { host: '127.0.0.1', port: opts.panelRemoteFacePort, path: req.url ?? '/', method, headers },
      (upRes) => {
        res.writeHead(upRes.statusCode ?? 502, upRes.headers);
        upRes.pipe(res);
      },
    );
    up.on('error', () => {
      refuse(res, 502, 'PANEL_UNREACHABLE', 'the panel behind this workspace did not answer',
        'The workspace may be starting or stopping. Try again shortly.');
    });
    req.pipe(up);
  }

  return {
    listener: (req, res) => {
      const path = (req.url ?? '/').split('?')[0] ?? '/';
      if (path === '/entry') {
        entry(req, res);
        return;
      }
      if (!sessions.check(req.headers.cookie, now())) {
        refuse(res, 401, 'ENTRY_REQUIRED', 'this connection has not presented a ticket',
          'Open the workspace from the cloud dashboard, which posts a fresh ticket to /entry.');
        return;
      }
      forward(req, res);
    },


    onUpgrade: (_req, socket) => {
      socket.destroy();
    },
  };
}
