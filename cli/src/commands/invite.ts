// SPDX-License-Identifier: Apache-2.0

import { readFileSync, writeFileSync } from 'node:fs';

import type { Client } from '../api/client.js';
import type { ParsedArgs } from '../args.js';
import { flagString } from '../args.js';
import { ExitCode, usageError } from '../errors.js';
import { json } from '../output.js';










interface Invitation {
  invitee: string;
  from_network: string;
  to_network: string;
  to_network_fingerprint: string;
  token: string;
  expires_at: string;
}

export async function invite(args: ParsedArgs, client: Client): Promise<number> {
  const agent = args.positionals[2];
  const from = flagString(args, 'from');
  if (!agent || !from) {
    throw usageError(
      'network invite needs an agent and the network it is in',
      'For example: baton network invite b --from net_7f93a2b1c4d5e6f7',
    );
  }




  const ttl = flagString(args, 'ttl') ?? '1h';
  if (args.global.dryRun) {
    const out = flagString(args, 'out-file');
    process.stdout.write(
      args.global.output === 'json'
        ? json({ would_invite: agent, from_network: from, ttl, out_file: out ?? null }) + '\n'
        : `would invite ${agent} from ${from} (ttl ${ttl})` +
            (out ? `, writing the token to ${out}` : '') +
            '\n  nothing was issued.\n',
    );
    return ExitCode.OK;
  }

  const inv = await client.post<Invitation>('/networks/self/invitations', {
    invitee: agent,
    from_network: from,
    ttl,
  });

  const out = flagString(args, 'out-file');
  if (out) {



    writeFileSync(out, JSON.stringify(inv, null, 2), { mode: 0o600 });
  }

  if (args.global.output === 'json') {
    process.stdout.write(json(inv) + '\n');
    return ExitCode.OK;
  }

  process.stdout.write(
    `invited ${inv.invitee}\n` +
      `  from       ${inv.from_network}\n` +
      `  to         ${inv.to_network}  ${inv.to_network_fingerprint}\n` +
      `  expires    ${inv.expires_at}\n` +
      (out ? `  written to ${out}\n` : '') +
      '\n' +



      `  Send this to the master of ${inv.from_network}, and only to them.\n` +
      '  Whoever holds it can redeem it as ' + inv.invitee + ': the token is the credential,\n' +
      '  and the channel you send it through is what protects it.\n',
  );
  return ExitCode.OK;
}

export async function approveTransfer(args: ParsedArgs, client: Client): Promise<number> {
  const file = flagString(args, 'file');
  if (!file) {
    throw usageError(
      'network approve-transfer needs the invitation',
      'Give it with --file <path>. It is the JSON the inviting network produced.',
    );
  }
  const inv = JSON.parse(readFileSync(file, 'utf8')) as Invitation;

  const approval = await client.post<{
    invitee: string;
    to_network: string;
    from_network_fingerprint: string;
  }>('/networks/self/transfers', inv);

  if (args.global.output === 'json') {
    process.stdout.write(json(approval) + '\n');
    return ExitCode.OK;
  }

  process.stdout.write(
    `released ${approval.invitee} to ${approval.to_network}\n` +
      `  this network  ${approval.from_network_fingerprint}\n\n` +



      `  Its certificates here are revoked — it can no longer reach this network.\n` +
      `  If the transfer does not complete on the other side, take it back with a\n` +
      `  fresh enrollment token; no coordination with ${approval.to_network} is needed.\n`,
  );
  return ExitCode.OK;
}
