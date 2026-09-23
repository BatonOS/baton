// SPDX-License-Identifier: Apache-2.0










export const CONTAINER = 'container';


export function parseLabels(s: string | undefined | null): Record<string, string> {
  const out: Record<string, string> = {};
  for (const part of (s ?? '').split(',')) {
    const t = part.trim();
    if (!t) continue;
    const i = t.indexOf('=');
    if (i <= 0) continue;
    out[t.slice(0, i)] = t.slice(i + 1);
  }
  return out;
}
