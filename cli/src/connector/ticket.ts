// SPDX-License-Identifier: Apache-2.0










































import { createPublicKey, verify as edVerify, type KeyObject } from 'node:crypto';
import { z } from 'zod';

export const TICKET_PURPOSE = 'workspace-panel-entry';


const payloadSchema = z
  .object({
    purpose: z.string(),
    ws: z.string().min(1),
    exp: z.number().int(),
    nonce: z.string().min(22),
  })
  .strict();

export type TicketVerdict =
  | { ok: true; ws: string }
  | { ok: false; code: 'invalid_ticket' | 'not_owned' | 'expired' | 'already_used' };





export class NonceSet {
  private burned = new Map<string, number>();
  constructor(private readonly cap = 10_000) {}


  burn(nonce: string, exp: number, now: number): boolean {
    for (const [n, e] of this.burned) if (e <= now) this.burned.delete(n);
    if (this.burned.has(nonce)) return false;


    if (this.burned.size >= this.cap) return false;
    this.burned.set(nonce, exp);
    return true;
  }
}

const B64URL = /^[A-Za-z0-9_-]+$/;

function b64urlDecode(s: string): Buffer | null {
  if (!B64URL.test(s)) return null;
  return Buffer.from(s, 'base64url');
}


export function loadIssuerKey(pem: string): KeyObject {
  const key = createPublicKey(pem);
  if (key.asymmetricKeyType !== 'ed25519') {
    throw new Error(`ticket issuer key must be ed25519, got ${key.asymmetricKeyType}`);
  }
  return key;
}









export function verifyTicket(
  ticket: string,
  opts: { issuerKey: KeyObject; ws: string; nonces: NonceSet; now: number },
): TicketVerdict {
  const dot = ticket.indexOf('.');
  if (dot <= 0 || ticket.indexOf('.', dot + 1) !== -1) return { ok: false, code: 'invalid_ticket' };
  const encodedPayload = ticket.slice(0, dot);
  const sig = b64urlDecode(ticket.slice(dot + 1));
  const payloadBytes = b64urlDecode(encodedPayload);
  if (!sig || !payloadBytes) return { ok: false, code: 'invalid_ticket' };


  if (!edVerify(null, Buffer.from(encodedPayload, 'ascii'), opts.issuerKey, sig)) {
    return { ok: false, code: 'invalid_ticket' };
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(payloadBytes.toString('utf8'));
  } catch {
    return { ok: false, code: 'invalid_ticket' };
  }
  const claims = payloadSchema.safeParse(parsed);
  if (!claims.success) return { ok: false, code: 'invalid_ticket' };
  const { purpose, ws, exp, nonce } = claims.data;



  if (purpose !== TICKET_PURPOSE) return { ok: false, code: 'invalid_ticket' };
  if (ws !== opts.ws) return { ok: false, code: 'not_owned' };
  if (exp <= opts.now) return { ok: false, code: 'expired' };
  if (!opts.nonces.burn(nonce, exp, opts.now)) return { ok: false, code: 'already_used' };
  return { ok: true, ws };
}
