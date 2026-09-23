import { defaultDataDir } from '../api/client.js';
// SPDX-License-Identifier: Apache-2.0


















import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { parse } from 'yaml';
import { join } from 'node:path';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { containerName, isValidNodeName, type ComponentRole } from './target.js';

const ROLES: ComponentRole[] = ['master', 'standby', 'agent'];

export interface Manifest {
  role: ComponentRole;
  node: string;

  path: string;
  container: string;
}

export function composeDir(dataDir: string | undefined): string {
  return join(dataDir ?? defaultDataDir(), 'compose');
}










export function listManifests(dataDir: string | undefined): Manifest[] {
  const dir = composeDir(dataDir);
  if (!existsSync(dir)) return [];

  const out: Manifest[] = [];
  for (const file of readdirSync(dir)) {
    const parsed = parseManifestName(file);
    if (!parsed) continue;
    out.push({
      ...parsed,
      path: join(dir, file),
      container: containerName(parsed.role, parsed.node),
    });
  }
  return out.sort((a, b) => rank(a.role) - rank(b.role) || a.node.localeCompare(b.node));
}

function rank(role: ComponentRole): number {
  return role === 'master' ? 0 : role === 'standby' ? 1 : 2;
}















export function manifestVolumes(path: string): string[] | undefined {
  let doc: unknown;
  try {
    doc = parse(readFileSync(path, 'utf8'));
  } catch {
    return undefined;
  }
  if (!doc || typeof doc !== 'object') return undefined;
  const volumes = (doc as { volumes?: unknown }).volumes;



  if (volumes === undefined || volumes === null) return [];
  if (typeof volumes !== 'object' || Array.isArray(volumes)) return undefined;
  return Object.keys(volumes);
}













export function manifestHostPort(path: string): string | null {
  let doc: unknown;
  try {
    doc = parse(readFileSync(path, 'utf8'));
  } catch {
    return null;
  }
  const services = (doc as { services?: Record<string, { ports?: unknown }> } | null)?.services;
  if (!services || typeof services !== 'object') return null;
  for (const svc of Object.values(services)) {
    const ports = (svc as { ports?: unknown } | null)?.ports;
    if (!Array.isArray(ports)) continue;
    for (const p of ports) {
      if (typeof p !== 'string') continue;
      const parts = p.split(':');
      if (parts.length === 3) return parts[1] || null;
      if (parts.length === 2) return parts[0] || null;
    }
  }
  return null;
}








function parseManifestName(file: string): { role: ComponentRole; node: string } | undefined {
  if (!file.endsWith('.yml')) return undefined;
  const stem = file.slice(0, -'.yml'.length);
  for (const role of ROLES) {
    const prefix = `${role}-`;
    if (!stem.startsWith(prefix)) continue;
    const node = stem.slice(prefix.length);
    return isValidNodeName(node) ? { role, node } : undefined;
  }
  return undefined;
}








export function resolveManifest(
  dataDir: string | undefined,
  node: string,
  role?: ComponentRole,
): Manifest {
  const all = listManifests(dataDir);
  const matches = all.filter((m) => m.node === node && (!role || m.role === role));

  if (matches.length === 1) return matches[0]!;

  if (matches.length === 0) {
    const known = all.map((m) => `${m.node} (${m.role})`);
    throw new BatonError({
      code: 'NOT_FOUND',
      message: `no node named ${node} was created in ${composeDir(dataDir)}`,
      remediation:
        known.length > 0
          ? `Created nodes: ${known.join(', ')}.`
          : 'Nothing has been created yet. Start with `baton init`.',
      exitCode: ExitCode.PRECONDITION,
      details: { known },
    });
  }

  throw new BatonError({
    code: 'AMBIGUOUS',
    message: `${node} exists under more than one role: ${matches.map((m) => m.role).join(', ')}`,
    remediation: `Say which one: --role ${matches[0]!.role}`,
    exitCode: ExitCode.CONFLICT,
    details: { matches: matches.map((m) => m.role) },
  });
}


export function selectManifests(
  dataDir: string | undefined,
  node: string | undefined,
  role?: ComponentRole,
): Manifest[] {
  if (node) return [resolveManifest(dataDir, node, role)];

  const all = listManifests(dataDir);
  if (all.length === 0) {
    throw preconditionError(
      `no nodes have been created in ${composeDir(dataDir)}`,




      'Create one with `baton agent create --name <n>`. If your nodes live ' +
        'elsewhere, point at them with --data-dir.',
    );
  }
  return role ? all.filter((m) => m.role === role) : all;
}


export function parseRole(raw: string | undefined): ComponentRole | undefined {
  if (!raw) return undefined;
  if ((ROLES as string[]).includes(raw)) return raw as ComponentRole;
  throw usageError(`--role ${raw} is not a role`, `Use one of: ${ROLES.join(', ')}.`);
}
