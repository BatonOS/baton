// SPDX-License-Identifier: Apache-2.0












import type { ParsedArgs } from '../args.js';
import type { Client } from '../api/client.js';
import { ExitCode, usageError } from '../errors.js';
import { flagString } from '../args.js';
import { json } from '../output.js';
import { readFileSync } from 'node:fs';


export interface CoreStatus {
  execution_chain: string;
  contract_version: string;
  build: { version: string; revision: string; source_state: string };
}

export async function coreVerb(args: ParsedArgs, client: Client): Promise<number> {
  const sub = args.positionals[1];
  switch (sub) {
    case 'status':
      return status(args, client);
    case 'propose':
      return propose(args, client);
    case 'approve':
    case 'reject':
      return decide(args, client, sub);
    default:
      throw usageError(
        sub ? `baton core has no subcommand "${sub}"` : 'baton core needs a subcommand',
        'baton core status — which execution chain, contract and build answered\n' +
          'baton core propose < proposal.json — carry a Proposal to the chain; the body is read from stdin\n' +
          'baton core approve|reject <transaction-id> --plan-digest <digest> [--note <text>] — a verdict on a frozen plan',
      );
  }
}



























async function propose(args: ParsedArgs, client: Client): Promise<number> {
  let text: string;
  try {
    text = readFileSync(0, 'utf8');
  } catch (err) {
    throw usageError(
      `could not read the proposal from stdin: ${err instanceof Error ? err.message : String(err)}`,
      'baton core propose < proposal.json',
    );
  }
  if (!text.trim()) {
    throw usageError(
      'baton core propose read an empty proposal from stdin',
      'Pipe the Proposal in: baton core propose < proposal.json — an empty body is not a proposal, ' +
        'and sending it would make the chain answer about a document nobody wrote.',
    );
  }
  let body: unknown;
  try {
    body = JSON.parse(text);
  } catch (err) {
    throw usageError(
      `the proposal on stdin is not valid JSON: ${err instanceof Error ? err.message : String(err)}`,
      'A Proposal is one JSON object. Check it with: jq . < proposal.json',
    );
  }





  const res = await client.post<{ transaction: string; status: string }>('/core/proposals', body);
  process.stdout.write(args.global.output === 'json' ? json(res) + '\n' : `${res.transaction} ${res.status}\n`);
  return ExitCode.OK;
}

async function status(args: ParsedArgs, client: Client): Promise<number> {
  const res = await client.get<CoreStatus>('/core/status');
  process.stdout.write(args.global.output === 'json' ? json(res) + '\n' : render(res));
  return ExitCode.OK;
}


export function render(s: CoreStatus): string {
  return (
    `execution chain   ${s.execution_chain}\n` +
    `contract version  ${s.contract_version}\n` +
    `build             ${s.build.version} · ${s.build.revision} · ${s.build.source_state}\n` +
    (s.build.source_state === 'clean'
      ? ''
      : `  ⚠ source ${s.build.source_state}: this build does not correspond to any one commit, ` +
        'so "built from the tree being judged" cannot be established.\n')
  );
}

















async function decide(args: ParsedArgs, client: Client, verb: 'approve' | 'reject'): Promise<number> {
  const id = args.positionals[2];
  const digest = flagString(args, 'plan-digest');
  if (!id || !digest) {
    throw usageError(
      `baton core ${verb} needs a transaction id and --plan-digest`,
      `baton core ${verb} <transaction-id> --plan-digest <digest> [--note <text>] — ` +
        'the digest is the one shown with the plan, in the same Outcome.',
    );
  }
  const note = flagString(args, 'note');
  const res = await client.post<{ status: string; transaction: string }>(
    `/core/transactions/${encodeURIComponent(id)}/${verb}`,
    { plan_digest: digest, ...(note ? { note } : {}) },
  );
  process.stdout.write(args.global.output === 'json' ? json(res) + '\n' : `${res.transaction} ${res.status}\n`);
  return ExitCode.OK;
}
