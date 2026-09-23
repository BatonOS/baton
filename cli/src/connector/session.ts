// SPDX-License-Identifier: Apache-2.0




















import { randomBytes, timingSafeEqual } from 'node:crypto';

export const SESSION_COOKIE = '__Host-baton_entry';


const SESSION_TTL_S = 12 * 60 * 60;

export class SessionStore {
  private sessions = new Map<string, number>();


  mint(now: number): string {
    for (const [t, e] of this.sessions) if (e <= now) this.sessions.delete(t);
    const token = randomBytes(32).toString('base64url');
    this.sessions.set(token, now + SESSION_TTL_S);
    return `${SESSION_COOKIE}=${token}; Path=/; Secure; HttpOnly; SameSite=Lax; Max-Age=${SESSION_TTL_S}`;
  }


  check(cookieHeader: string | undefined, now: number): boolean {
    if (!cookieHeader) return false;
    for (const part of cookieHeader.split(';')) {
      const eq = part.indexOf('=');
      if (eq < 0) continue;
      if (part.slice(0, eq).trim() !== SESSION_COOKIE) continue;
      const presented = part.slice(eq + 1).trim();
      for (const [token, exp] of this.sessions) {
        if (exp <= now) continue;
        const a = Buffer.from(presented);
        const b = Buffer.from(token);
        if (a.length === b.length && timingSafeEqual(a, b)) return true;
      }
    }
    return false;
  }
}
