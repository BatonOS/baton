// SPDX-License-Identifier: Apache-2.0













import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { parse as parseYAML } from 'yaml';

import { execCapture } from './engine.js';

interface OfficeFile {
  node_id: string;
  name: string;
  driver_network: string | null;
  applied_to?: string | null;
  driver?: { driver: string; node_ref: string; mounts: { class: string; hostPath: string; guestPath: string }[] };
  [k: string]: unknown;
}


export function reconcileApplied(dataDir: string, name: string): boolean {
  const path = join(dataDir, 'nodes', `${name}.json`);
  if (!existsSync(path)) return false;
  let office: OfficeFile;
  try {
    office = JSON.parse(readFileSync(path, 'utf8')) as OfficeFile;
  } catch {
    return false;
  }
  if (!office.applied_to || office.driver_network) return false;


  if (office.driver) {
    const batonMount = office.driver.mounts.find((m) => m.class === 'baton');
    if (!batonMount) return false;
    const idFile = join(batonMount.hostPath, 'pki', 'identity.json');
    if (existsSync(idFile)) {
      try {
        const id = JSON.parse(readFileSync(idFile, 'utf8')) as { node_id?: string };
        if (!id.node_id) return false;
        office.node_id = id.node_id;
        office.driver_network = office.applied_to;
        office.applied_to = null;
        writeFileSync(path, JSON.stringify(office, null, 2) + '\n', { mode: 0o600 });
        return true;
      } catch { return false; }
    }




    return false;
  }
  const container = `baton-agent-${name}`;
  let id: { node_id?: string } = {};
  try {
    id = JSON.parse(execCapture(container, ['cat', '/var/lib/baton/pki/identity.json']).stdout) as { node_id?: string };
  } catch {




    const probe = execCapture(container, ['sh', '-c', 'test -f /var/lib/baton/pki/application.json && echo yes || echo no']);
    if (probe.ok && probe.stdout.trim() === 'no') {
      office.applied_to = null;
      writeFileSync(path, JSON.stringify(office, null, 2) + '\n', { mode: 0o600 });
      return true;
    }
    return false;
  }
  if (!id.node_id) return false;
  let net: string | null = null;
  try {
    const doc = parseYAML(readFileSync(join(dataDir, 'compose', `agent-${name}.yml`), 'utf8')) as {
      networks?: Record<string, { name?: string }>;
    };
    net = doc.networks?.default?.name ?? null;
  } catch {  }
  office.node_id = id.node_id;
  office.driver_network = net ?? office.applied_to;
  office.applied_to = null;
  writeFileSync(path, JSON.stringify(office, null, 2) + '\n', { mode: 0o600 });
  return true;
}














export function markLeftNetwork(dataDir: string, name: string): void {
  const path = join(dataDir, 'nodes', `${name}.json`);
  if (!existsSync(path)) return;
  const office = JSON.parse(readFileSync(path, 'utf8')) as OfficeFile;
  office.driver_network = null;
  office.applied_to = null;
  writeFileSync(path, JSON.stringify(office, null, 2) + '\n', { mode: 0o600 });
}
