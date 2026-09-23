// SPDX-License-Identifier: Apache-2.0










import type { ParsedArgs } from '../args.js';
import { usageError } from '../errors.js';
import { serve } from '../provider/serve.js';

export async function providerVerb(args: ParsedArgs): Promise<number> {
  const sub = args.positionals[1];
  if (sub === 'serve') return serve(args);
  throw usageError(
    sub ? `provider has no subcommand ${sub}` : 'provider needs a subcommand',
    'baton provider serve — start the local Provider on <data-dir>/provider/run/provider.sock',
  );
}
