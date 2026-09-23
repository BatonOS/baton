// SPDX-License-Identifier: Apache-2.0










import type { RunState } from './engine.js';











export const LEVELS = ['crash-consistent', 'filesystem-consistent', 'application-consistent'] as const;

export type Level = (typeof LEVELS)[number];


export type Consistency = Level | 'unknown';


export const METADATA_MEMBER = 'snapshot.json';











export function consistencyOf(state: RunState): Consistency {
  return state === 'running' ? 'crash-consistent' : 'unknown';
}








export function describeConsistency(c: Consistency): string {
  switch (c) {
    case 'crash-consistent':
      return 'crash-consistent — as if the power had been cut: the filesystem journal recovers what it can, nothing else';
    case 'filesystem-consistent':
      return 'filesystem-consistent — the filesystem was frozen, but the application was not asked';
    case 'application-consistent':
      return 'application-consistent — the runtime flushed and stopped writing before this was read';
    case 'unknown':







      return 'unknown — no level can be claimed';
  }
}










export function consistencyFrom(doc: unknown): Consistency {
  if (!doc || typeof doc !== 'object' || Array.isArray(doc)) return 'unknown';
  const v = (doc as { consistency?: unknown }).consistency;
  return typeof v === 'string' && (LEVELS as readonly string[]).includes(v) ? (v as Level) : 'unknown';
}


export function metadataDocument(consistency: Consistency, takenAt: string): Record<string, unknown> {
  return { consistency, taken_at: takenAt };
}
