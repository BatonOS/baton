// SPDX-License-Identifier: Apache-2.0


































import { cpus, totalmem } from 'node:os';
import { readFileSync } from 'node:fs';
import { parse } from 'yaml';

import { listManifests } from './manifests.js';

export interface HostResources {
  cpu_cores: number | null;
  memory_bytes: number | null;
  memory_available_bytes: number | null;
  committed_cpu: number | null;
  committed_memory_bytes: number | null;
}
















function memAvailableBytes(): number | null {
  try {
    const text = readFileSync('/proc/meminfo', 'utf8');
    const line = text.split('\n').find((l) => l.startsWith('MemAvailable:'));
    if (!line) return null;
    const kb = Number(line.replace(/^MemAvailable:\s*/, '').replace(/\s*kB\s*$/, ''));
    return Number.isFinite(kb) && kb >= 0 ? kb * 1024 : null;
  } catch {
    return null;
  }
}


function composeBytes(raw: unknown): number | undefined {
  if (typeof raw === 'number') return Number.isFinite(raw) ? raw : undefined;
  if (typeof raw !== 'string') return undefined;
  const m = /^(\d+(?:\.\d+)?)\s*([bkmg]?)b?$/i.exec(raw.trim());
  if (!m) return undefined;
  const n = Number(m[1]);
  if (!Number.isFinite(n)) return undefined;
  const factor = { '': 1, b: 1, k: 1024, m: 1024 ** 2, g: 1024 ** 3 }[(m[2] ?? '').toLowerCase()] ?? 1;
  return Math.round(n * factor);
}















function committed(dataDir: string | undefined): { cpu: number | null; memory: number | null } {
  let manifests;
  try {
    manifests = listManifests(dataDir);
  } catch {

    return { cpu: null, memory: null };
  }




  let cpu = 0;
  let memory = 0;
  let read = false;

  for (const m of manifests) {
    let doc: unknown;
    try {
      doc = parse(readFileSync(m.path, 'utf8'));
    } catch {


      return { cpu: null, memory: null };
    }
    read = true;
    const services = (doc as { services?: Record<string, Record<string, unknown>> })?.services ?? {};
    for (const svc of Object.values(services)) {
      const c = Number(svc?.cpus);
      if (Number.isFinite(c)) cpu += c;
      const b = composeBytes(svc?.mem_limit);
      if (b !== undefined) memory += b;
    }
  }

  return read || manifests.length === 0
    ? { cpu: Number(cpu.toFixed(3)), memory }
    : { cpu: null, memory: null };
}









export function hostResources(dataDir: string | undefined): HostResources {
  const cores = cpus()?.length;
  const total = totalmem();
  const c = committed(dataDir);
  return {
    cpu_cores: Number.isFinite(cores) && cores > 0 ? cores : null,
    memory_bytes: Number.isFinite(total) && total > 0 ? total : null,
    memory_available_bytes: memAvailableBytes(),
    committed_cpu: c.cpu,
    committed_memory_bytes: c.memory,
  };
}
