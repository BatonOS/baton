// SPDX-License-Identifier: Apache-2.0












import type { ParsedArgs } from '../args.js';
import type { Client } from '../api/client.js';
import { ExitCode, usageError } from '../errors.js';
import { json } from '../output.js';

export async function contextVerb(args: ParsedArgs, client: Client): Promise<number> {
  if (args.positionals.length > 1) {
    throw usageError('baton context takes no arguments', 'baton context — the caller is who the answer is about');
  }
  const res = await client.get<unknown>('/core/context');
  process.stdout.write(json(res) + '\n');
  return ExitCode.OK;
}
