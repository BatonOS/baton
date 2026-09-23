// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs';

import type { Client } from '../api/client.js';
import type { ParsedArgs } from '../args.js';
import { ExitCode, usageError } from '../errors.js';
import { json, table } from '../output.js';
import { flagBool, flagString } from '../args.js';









interface IntegrationView {
  integration_id: string;
  provider: string;
  public_key: string;
  fingerprint: string;
  scopes: string[];
  target_network: string;
  enabled: boolean;
  created_at: string;
  confirmed_at?: string;
}

export async function integrations(args: ParsedArgs, client: Client): Promise<number> {
  const sub = args.positionals[1] ?? 'list';

  if (sub === 'add') {











    const fromStdin = flagBool(args, 'stdin');
    const file = flagString(args, 'passkey-file');
    if (fromStdin && file) {
      throw usageError(
        'integrations add takes --stdin or --passkey-file, not both',
        'Two sources for one key means one of them is ignored, silently. Pick one.',
      );
    }
    if (!fromStdin && !file) {
      throw usageError(
        'integrations add needs a key',
        'Give one with --passkey-file <path> (or pipe it with --stdin). The value is the ' +
          'PUBLIC half the provider published — never a private key.',
      );
    }
    const publicKey = fromStdin ? readFileSync(0, 'utf8') : readFileSync(file!, 'utf8');
    if (!publicKey.trim()) {
      throw usageError(
        'the key is empty',
        fromStdin ? 'Nothing arrived on stdin.' : `${file} is empty.`,
      );
    }
    const provider = flagString(args, 'provider') ?? 'baton-cloud';
    const scopes = (flagString(args, 'scopes') ?? 'skill.install')
      .split(',')
      .map((s: string) => s.trim())
      .filter(Boolean);

    const created = await client.post<IntegrationView>('/integrations', {
      provider,
      public_key: publicKey,
      scopes,
    });

    process.stdout.write(
      args.global.output === 'json'
        ? json(created) + '\n'
        : `added ${created.integration_id}  ${created.provider}\n` +
            `  ${created.fingerprint}\n` +
            `  scopes: ${created.scopes.join(', ') || '(none)'}\n\n` +




            `Compare that fingerprint with the one ${created.provider} shows you.\n` +
            `It is disabled until you enable it: baton integrations enable ${created.integration_id}\n`,
    );
    return ExitCode.OK;
  }

  if (sub === 'enable' || sub === 'disable' || sub === 'revoke') {
    const id = args.positionals[2];
    if (!id) {
      throw usageError(`integrations ${sub} needs an id`, 'Find it with: baton integrations list');
    }
    if (sub === 'revoke') {
      await client.delete(`/integrations/${encodeURIComponent(id)}`);


      process.stdout.write(`revoked ${id}\n  Its key is gone; signatures stop being accepted now.\n`);
      return ExitCode.OK;
    }
    const got = await client.post<IntegrationView>(`/integrations/${encodeURIComponent(id)}/${sub}`, {});
    process.stdout.write(`${got.integration_id} is now ${got.enabled ? 'enabled' : 'disabled'}\n`);
    return ExitCode.OK;
  }

  if (sub !== 'list') {
    throw usageError(
      `integrations ${sub} is not a subcommand`,
      'Available: baton integrations [list] | add --passkey-file <f> | enable <id> | disable <id> | revoke <id>',
    );
  }

  const res = await client.get<{ items: IntegrationView[] }>('/integrations');
  const items = res.items ?? [];

  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }

  process.stdout.write(
    table(
      items,
      [
        { header: 'id', get: (i) => i.integration_id },
        { header: 'provider', get: (i) => i.provider },

        { header: 'fingerprint', get: (i) => i.fingerprint },



        { header: 'may', get: (i) => i.scopes.join(', ') || '—' },
        { header: 'state', get: (i) => (i.enabled ? 'enabled' : 'disabled') },
      ],
      'No provider is authorised to act on this network.',
    ) + '\n',
  );
  return ExitCode.OK;
}
