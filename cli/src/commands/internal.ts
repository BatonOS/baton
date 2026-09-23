// SPDX-License-Identifier: Apache-2.0





import type { ParsedArgs } from '../args.js';
import { usageError } from '../errors.js';
import { agentCreate } from './agent.js';













export async function internalVerb(args: ParsedArgs): Promise<number> {
  const noun = args.positionals[1] ?? '';
  const sub = args.positionals[2] ?? '';
  if (noun === 'agent' && sub === 'provision') {
    return agentCreate({ ...args, positionals: ['agent', 'create', ...args.positionals.slice(3)] });
  }
  throw usageError(
    `internal ${[noun, sub].filter(Boolean).join(' ') || '(nothing)'} is not a subcommand`,
    'Available: baton internal agent provision --name <n> [--node-id <id>] [--no-network] [--template <t>]',
  );
}
