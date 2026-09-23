// SPDX-License-Identifier: Apache-2.0





























import { readFileSync, writeFileSync } from 'node:fs';
import type { Client } from '../api/client.js';
import { flagString, type ParsedArgs } from '../args.js';
import { ExitCode } from '../errors.js';
import { usageError } from '../errors.js';
import { json } from '../output.js';

interface DeliveryGrantView {
  grant_id: string;
  subject_network_id?: string;
  scope?: string;
  expiry_at?: string;
  revoked_at?: string | null;
  payload?: string;
  signature?: string;
}

export async function deliveryVerb(args: ParsedArgs, client: Client): Promise<number> {
  if (args.positionals[1] !== 'grant') {
    throw usageError(
      `delivery ${args.positionals[1] ?? ''} is not a subcommand`,
      'baton delivery grant issue --subject <net_id> --key <b64> --scope <recipient> --expiry <RFC3339> | list | revoke <grant_id>',
    );
  }
  switch (args.positionals[2]) {
    case 'issue':  return grantIssue(args, client);
    case 'list':   return grantList(args, client);
    case 'revoke': return grantRevoke(args, client);
    default:
      throw usageError(
        `delivery grant ${args.positionals[2] ?? ''} is not a subcommand`,
        'baton delivery grant issue --subject <net_id> --key <b64> --scope <recipient> --expiry <RFC3339> | list | revoke <grant_id>',
      );
  }
}






function readKey(args: ParsedArgs): string {
  const keyFile = flagString(args, 'key-file');
  if (keyFile) return readFileSync(keyFile, 'utf8').trim();
  const key = flagString(args, 'key');
  if (!key) {
    throw usageError(
      "delivery grant issue needs --key — the sender network's public key",
      "Paste it (single-line base64), or read it from a file with --key-file <path>. " +
        'The sender reads its own with `baton network show`.',
    );
  }
  if (key.startsWith('@')) return readFileSync(key.slice(1), 'utf8').trim();
  return key.trim();
}

async function grantIssue(args: ParsedArgs, client: Client): Promise<number> {
  const subject = flagString(args, 'subject');
  const scope = flagString(args, 'scope');
  const expiry = flagString(args, 'expiry');
  const key = readKey(args);
  if (!subject || !scope || !expiry) {
    throw usageError(
      'delivery grant issue needs --subject, --scope and --expiry',
      "Endorse a network to deliver here: baton delivery grant issue --subject net_b17c… " +
        "--key <sender's base64 pubkey> --scope @coder --expiry 2026-09-21T12:00:00Z",
    );
  }



  if (Number.isNaN(Date.parse(expiry))) {
    throw usageError(
      '--expiry must be an RFC3339 timestamp',
      'A grant is a dated authorization; give it an explicit end: --expiry 2026-09-21T12:00:00Z',
    );
  }
  if (args.global.dryRun) {
    process.stdout.write(
      `would endorse ${subject} to deliver to ${scope} until ${expiry}. Nothing was signed.\n`,
    );
    return ExitCode.OK;
  }
  const g = await client.post<DeliveryGrantView>('/networks/self/delivery-grants', {
    subject_network_id: subject, subject_key: key, scope, expiry_at: expiry,
  });
  const outFile = flagString(args, 'out-file');
  if (outFile) {

    writeFileSync(outFile, json(g) + '\n', { mode: 0o600 });
    process.stdout.write(
      `delivery grant ${g.grant_id} for ${subject} written to ${outFile} (scope ${scope}, until ${expiry}).\n` +
        `  Hand it to ${subject}; revoke any time with: baton delivery grant revoke ${g.grant_id}\n`,
    );
    return ExitCode.OK;
  }
  process.stdout.write(json(g) + '\n');
  return ExitCode.OK;
}

async function grantList(args: ParsedArgs, client: Client): Promise<number> {
  const res = await client.get<{ grants?: DeliveryGrantView[] }>('/networks/self/delivery-grants');
  const grants = res.grants ?? [];
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  if (grants.length === 0) {
    process.stdout.write('no delivery grants issued.\n');
    return ExitCode.OK;
  }
  for (const g of grants) {

    const state = g.revoked_at ? `revoked ${g.revoked_at}` : `active until ${g.expiry_at ?? '<no expiry>'}`;
    process.stdout.write(`${g.grant_id}  ${g.subject_network_id ?? '<no subject>'}  ${g.scope ?? '<no scope>'}  ${state}\n`);
  }
  return ExitCode.OK;
}

async function grantRevoke(args: ParsedArgs, client: Client): Promise<number> {
  const id = args.positionals[3];
  if (!id) {
    throw usageError(
      'delivery grant revoke needs a grant_id',
      'baton delivery grant revoke grant-3f2c… — find it with `baton delivery grant list`.',
    );
  }
  if (args.global.dryRun) {
    process.stdout.write(`would revoke delivery grant ${id}. Nothing was changed.\n`);
    return ExitCode.OK;
  }
  await client.delete(`/networks/self/delivery-grants/${encodeURIComponent(id)}`);



  process.stdout.write(
    `revoked ${id} — no new delivery token will be issued on it.\n` +
      `  Tokens already issued stay valid until they expire; that window is the grant's real close time.\n`,
  );
  return ExitCode.OK;
}
