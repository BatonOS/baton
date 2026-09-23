// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs';



















export function packageVersion(from: URL = new URL('../package.json', import.meta.url)): string {
  try {
    const v = (JSON.parse(readFileSync(from, 'utf8')) as { version?: unknown }).version;
    return typeof v === 'string' && v.length > 0 ? v : 'unknown';
  } catch {
    return 'unknown';
  }
}
