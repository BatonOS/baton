// SPDX-License-Identifier: Apache-2.0














import { readFileSync, writeFileSync } from 'node:fs';
import type { Client } from '../api/client.js';
import { type Attestation } from './cloud.js';
import { flagBool, flagString, parseDuration, type ParsedArgs } from '../args.js';
import { ExitCode, preconditionError, usageError } from '../errors.js';
import { json } from '../output.js';
import { publishEndpoint } from './network.js';

export async function transfer(args: ParsedArgs, client: Client): Promise<number> {
  const what = args.positionals[1];
  if (what !== 'master') {
    throw usageError(
      `transfer ${what ?? ''} is not a subcommand`,
      'Today: baton transfer master --to <standby> | baton transfer master accept --file <offer>',
    );
  }
  if (args.positionals[2] === 'accept') return acceptTransfer(args, client);
  if (args.positionals[2] === 'demote') return demoteSelf(args, client);
  return offerTransfer(args, client);
}


async function demoteSelf(args: ParsedArgs, client: Client): Promise<number> {


  const newMaster = flagString(args, 'to');




  const receiptFile = flagString(args, 'receipt');
  const force = flagBool(args, 'force');
  const body: Record<string, unknown> = {};
  if (newMaster) body.new_master = newMaster;
  if (receiptFile) {



















    const onDisk = JSON.parse(readFileSync(receiptFile, 'utf8')) as Record<string, unknown>;
    const { payload, signature } = onDisk;
    if (typeof payload !== 'string' || typeof signature !== 'string') {
      throw preconditionError(
        `${receiptFile} is not a transfer receipt`,
        'It must carry `payload` and `signature` — the file `transfer master accept --receipt-out` writes.',
      );
    }
    body.receipt = { payload, signature };
  }
  if (force) body.force = true;
  const res = await client.post<{ role: string; read_only: boolean }>('/system/demote', body);
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    'demoted — this control plane is now a read-only mirror; it refuses writes.\n' +
      '  Point its nodes at the new master; promotion raised the leader epoch, so a returning old primary presents a stale one and is refused.\n',
  );
  return ExitCode.OK;
}


async function offerTransfer(args: ParsedArgs, client: Client): Promise<number> {
  const to = flagString(args, 'to');
  if (!to) {
    throw usageError(
      'transfer master needs --to <standby>',
      'Name the standby that will take over: baton transfer master --to standby01 --out-file ./offer.json',
    );
  }
  const ttlMs = parseDuration(flagString(args, 'ttl') ?? '1h');
  const expires = new Date(Date.now() + ttlMs).toISOString();
  if (args.global.dryRun) {
    process.stdout.write(`would offer the master role to ${to} (expires ${expires}). Nothing was minted.\n`);
    return ExitCode.OK;
  }

  const att = await client.post<Attestation>('/networks/self/attestations', {
    purpose: 'master-transfer-offer',
    gaining: to,
    expires_at: expires,
  });
  const offer = {
    network_id: att.network_id, public_key: att.public_key,
    payload: att.payload, signature: att.signature,
    nonce: att.nonce, timestamp: att.timestamp,
    gaining: to, expires_at: expires,
  };
  const outFile = flagString(args, 'out-file');
  const text = json(offer);
  if (args.global.output === 'json' && !outFile) {
    process.stdout.write(text + '\n');
    return ExitCode.OK;
  }
  if (!outFile) {
    process.stdout.write(text + '\n');
    return ExitCode.OK;
  }
  writeFileSync(outFile, text, { mode: 0o600 });
  process.stdout.write(
    `transfer offer for ${to} written to ${outFile} (expires ${expires}).\n` +
      `  On ${to}'s host, point --master at ITS OWN endpoint and accept:\n` +
      `    baton transfer master accept --file ${outFile} --master https://<${to} endpoint>\n`,
  );
  return ExitCode.OK;
}


async function acceptTransfer(args: ParsedArgs, client: Client): Promise<number> {
  const file = flagString(args, 'file');
  if (!file) {
    throw usageError(
      'transfer master accept needs --file <offer>',
      'The offer is the file `baton transfer master --to` wrote on the master.',
    );
  }
  const offer = JSON.parse(readFileSync(file, 'utf8')) as { public_key: string; payload: string; signature: string };
  const res = await client.post<{
    role: string; leader_epoch: number; network_id: string;
    receipt: { payload: string; signature: string; public_key: string; nonce: string; timestamp: string };
  }>(
    '/system/transfer/accept',
    { public_key: offer.public_key, payload: offer.payload, signature: offer.signature },
  );




  const receiptOut = flagString(args, 'receipt-out');
  if (receiptOut) {
    writeFileSync(receiptOut, json(res.receipt) + '\n', { mode: 0o600 });
  }







  const endpoint = flagString(args, 'endpoint');
  let endpointResult: { ok: boolean; note?: string } | null = null;
  if (endpoint) {
    if (!/^https:\/\//.test(endpoint)) {
      throw usageError(
        'transfer master accept --endpoint must be https://host:port',
        'For example: --endpoint https://baton.example.com:8443 — where this network now answers.',
      );
    }
    const { cloudSync } = await publishEndpoint(client, endpoint, undefined, true);
    endpointResult = cloudSync;
  }

  if (args.global.output === 'json') {
    process.stdout.write(json({ ...res, endpoint: endpoint ?? null, cloud_endpoint: endpointResult }) + '\n');
    return ExitCode.OK;
  }
  const endpointLine = endpoint
    ? (endpointResult?.ok
        ? `  Published ${endpoint} as this network's sole endpoint; the cloud registry now points here.\n`
        : `  Published ${endpoint} locally; cloud registry NOT updated${endpointResult?.note ? ` (${endpointResult.note})` : ''} — run \`baton network publish --endpoint ${endpoint}\` once reachable.\n`)
    : `  Next: \`baton network publish --endpoint https://<this host>:8443\` so the cloud registry and nodes find this host.\n`;
  process.stdout.write(
    `accepted — this node is now the master (epoch ${res.leader_epoch}).\n` +
      endpointLine +
      (receiptOut
        ? `  Receipt written to ${receiptOut}. Carry it to the old master and demote:\n` +
          `    baton transfer master demote --to <this endpoint> --receipt ${receiptOut}\n`
        : `  This command's --output json carries a "receipt"; the old master demotes with it:\n` +
          `    baton transfer master demote --receipt <receipt.json>\n`) +










      `  Nodes that have seen epoch ${res.leader_epoch} will refuse the old master;\n` +
      `  the old master itself keeps answering until you demote it (above).\n`,
  );
  return ExitCode.OK;
}
