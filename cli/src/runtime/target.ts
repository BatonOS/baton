// SPDX-License-Identifier: Apache-2.0


















import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { defaultDataDir } from '../api/client.js';
import { containerExists, containerNames } from './engine.js';
import { BatonError, ExitCode, usageError } from '../errors.js';














export type ComponentRole = 'master' | 'standby' | 'agent';


const PRIMARY_COMPONENT: Record<ComponentRole, string> = {
  master: 'control-api',
  standby: 'control-api',
  agent: 'agent-daemon',
};

export interface Target {
  role: ComponentRole;
  node: string;
  component: string;
  isPrimary: boolean;
  container: string;
  session: string;
}


const NODE_NAME = /^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/;

export function isValidNodeName(name: string): boolean {
  return NODE_NAME.test(name);
}

export function validateNodeName(name: string): void {
  if (!NODE_NAME.test(name)) {
    throw usageError(
      `${name} is not a usable node name`,
      'Use lowercase letters, digits, and interior hyphens, 1-40 characters. ' +
        'No dots or colons (tmux session names forbid both), and no leading or trailing hyphen.',
    );
  }
}

export function containerName(role: ComponentRole, node: string, component?: string): string {
  const base = `baton-${role}-${node}`;
  return !component || component === PRIMARY_COMPONENT[role] ? base : `${base}-${component}`;
}

export function sessionName(role: ComponentRole, node: string, component?: string): string {
  const base = `baton-${role}-${node}`;
  return !component || component === PRIMARY_COMPONENT[role] ? base : `${base}--${component}`;
}


























function resolveNodeIdentifier(given: string, dataDir?: string): string {
  const dir = join(dataDir ?? defaultDataDir(), 'nodes');
  if (!existsSync(dir)) return given;

  const wanted = given.startsWith('@') ? given.slice(1) : given;
  let byID: string | undefined;
  let byAgent: string | undefined;

  for (const f of readdirSync(dir)) {
    if (!f.endsWith('.json')) continue;
    try {
      const rec = JSON.parse(readFileSync(join(dir, f), 'utf8')) as {
        name?: string;
        node_id?: string;
        agent?: string;
      };
      if (!rec.name) continue;


      if (rec.name === given) return rec.name;

      if (rec.node_id && wanted.length >= 4 && rec.node_id.startsWith(wanted)) byID = rec.name;
      if (rec.agent && rec.agent === wanted) byAgent = rec.name;
    } catch {


    }
  }
  return byID ?? byAgent ?? given;
}

export function resolveTarget(
  node: string,
  opts: { role?: ComponentRole; component?: string; dataDir?: string } = {},
): Target {
  node = resolveNodeIdentifier(node, opts.dataDir);
  validateNodeName(node);

  const roles: ComponentRole[] = opts.role ? [opts.role] : ['agent', 'master', 'standby'];
  const matches: ComponentRole[] = [];

  for (const role of roles) {
    if (containerExists(containerName(role, node, opts.component))) matches.push(role);
  }

  if (matches.length === 0) {
    throw new BatonError({
      code: 'NOT_FOUND',
      message: `node ${node} is not running here`,
      remediation:
        'List what is running with `baton status`. If the node exists but is stopped, ' +
        'start it with `baton start`.',
      exitCode: ExitCode.PRECONDITION,
    });
  }

  if (matches.length > 1) {
    throw new BatonError({
      code: 'AMBIGUOUS',
      message: `${node} matches more than one role: ${matches.join(', ')}`,
      remediation: `Say which one: --role ${matches[0]}`,
      exitCode: ExitCode.CONFLICT,
      details: { matches },
    });
  }

  const role = matches[0]!;
  const component = opts.component ?? PRIMARY_COMPONENT[role];

  return {
    role,
    node,
    component,
    isPrimary: component === PRIMARY_COMPONENT[role],
    container: containerName(role, node, opts.component),
    session: sessionName(role, node, opts.component),
  };
}


export function componentsOf(role: ComponentRole, node: string): string[] {
  const prefix = `baton-${role}-${node}`;
  return containerNames(prefix).map((name) =>
    name === prefix ? PRIMARY_COMPONENT[role] : name.slice(prefix.length + 1),
  );
}

export { PRIMARY_COMPONENT };
