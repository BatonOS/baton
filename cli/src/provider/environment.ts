// SPDX-License-Identifier: Apache-2.0

















import { basename, dirname, join, resolve } from 'node:path';
import { realpathSync } from 'node:fs';
import { z } from 'zod';

import type { GovernanceFact } from './protocol.js';
import { FACT_DESTINATION_RESOLVED, FACT_HARNESSES_AVAILABLE, FACT_WORKSPACE_BOUNDARY } from './protocol.js';
import { listTemplates } from '../runtime/template.js';
import type { Workspace } from './steps.js';

















export function canonicalise(p: string): string {
  let head = resolve(p);
  const tail: string[] = [];
  for (;;) {
    try {
      head = realpathSync.native(head);
      break;
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'ENOENT') throw err;
      const parent = dirname(head);


      if (parent === head) break;
      tail.unshift(basename(head));
      head = parent;
    }
  }
  if (tail.length === 0) return head;
  return join(head, ...tail);
}


export const EnvironmentFactsRequest = z
  .object({ destination: z.string().min(1).optional() })
  .strict();



















export function harnessesAvailable(dataDir: string): string[] {
  const counts = new Map<string, number>();
  for (const t of listTemplates(dataDir)) {
    if (t.error === null) counts.set(t.name, (counts.get(t.name) ?? 0) + 1);
  }
  return [...counts].filter(([, n]) => n === 1).map(([name]) => name).sort();
}

export function environmentFacts(
  req: { destination?: string },
  instanceID: string,
  now: () => Date,
  workspace?: Workspace,
  dataDir?: string,
): GovernanceFact[] {
  const facts: GovernanceFact[] = [];
  const at = now().toISOString();



  if (dataDir !== undefined) {
    let names: string[] | undefined;
    try {
      names = harnessesAvailable(dataDir);
    } catch {
      names = undefined;
    }
    if (names !== undefined) {
      facts.push({
        name: FACT_HARNESSES_AVAILABLE,
        value: JSON.stringify(names),
        source: `provider:local/${instanceID} templates`,
        resolved_at: at,
      });
    }
  }
  if (workspace) {
    facts.push({
      name: FACT_WORKSPACE_BOUNDARY,
      value: canonicalise(workspace.dir),
      source: `provider:local/${instanceID} --workspace-dir`,
      resolved_at: at,
    });
  }
  if (req.destination !== undefined) {
    facts.push({
      name: FACT_DESTINATION_RESOLVED,
      value: canonicalise(req.destination),
      source: `provider:local/${instanceID} realpath`,
      resolved_at: at,
    });
  }
  return facts;
}
