// SPDX-License-Identifier: Apache-2.0






















import { randomBytes, scryptSync, timingSafeEqual } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';

const DEFAULT_USER = 'admin';
const DEFAULT_PASSWORD = 'admin';









const TOKEN_TTL_MS = 12 * 60 * 60 * 1000;

interface Stored {
  salt: string;
  hash: string;
}









function credentialPath(dataDir: string): string {
  return join(dataDir, 'panel', 'passwd');
}

function hash(password: string, salt: string): string {
  return scryptSync(password, salt, 32).toString('hex');
}


export function passwordIsDefault(dataDir: string): boolean {
  const stored = readStored(dataDir);
  return !stored || verify(DEFAULT_PASSWORD, stored);
}

function readStored(dataDir: string): Stored | undefined {
  const path = credentialPath(dataDir);
  if (!existsSync(path)) return undefined;
  try {
    return JSON.parse(readFileSync(path, 'utf8')) as Stored;
  } catch {
    return undefined;
  }
}

function verify(password: string, stored: Stored): boolean {
  const got = Buffer.from(hash(password, stored.salt), 'hex');
  const want = Buffer.from(stored.hash, 'hex');



  return got.length === want.length && timingSafeEqual(got, want);
}



















export function resetToDefault(dataDir: string): void {
  const path = credentialPath(dataDir);
  if (existsSync(path)) rmSync(path);
}

export function setPassword(dataDir: string, password: string): void {
  const salt = randomBytes(16).toString('hex');
  const path = credentialPath(dataDir);
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 });
  writeFileSync(path, JSON.stringify({ salt, hash: hash(password, salt) }), { mode: 0o600 });
}








export function checkCredentials(dataDir: string, user: string, password: string): boolean {
  if (user !== DEFAULT_USER) return false;
  const stored = readStored(dataDir);
  if (!stored) return password === DEFAULT_PASSWORD;
  return verify(password, stored);
}
















interface Session {
  expiry: number;
  sessionId: string;
}

const sessions = new Map<string, Session>();

export function issueToken(): { token: string; expiresAt: string } {
  const token = randomBytes(32).toString('base64url');
  const expiry = Date.now() + TOKEN_TTL_MS;
  sessions.set(token, { expiry, sessionId: 'sess-' + randomBytes(6).toString('hex') });
  return { token, expiresAt: new Date(expiry).toISOString() };
}







export function sessionIdFor(token: string): string | undefined {
  const s = sessions.get(token);
  if (!s || Date.now() > s.expiry) return undefined;
  return s.sessionId;
}

export function revokeToken(token: string): void {
  sessions.delete(token);
}










export function revokeAllTokens(): void {
  sessions.clear();
}

export function tokenIsValid(token: string): boolean {
  const s = sessions.get(token);
  if (s === undefined) return false;
  if (Date.now() > s.expiry) {



    sessions.delete(token);
    return false;
  }
  return true;
}

export function bearerFrom(header: string | undefined): string {
  if (!header) return '';
  return header.startsWith('Bearer ') ? header.slice(7) : '';
}

export const AUTH_DEFAULTS = { user: DEFAULT_USER, password: DEFAULT_PASSWORD, ttlMs: TOKEN_TTL_MS };
