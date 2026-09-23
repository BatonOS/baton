// SPDX-License-Identifier: Apache-2.0

















import { readFileSync, writeFileSync } from 'node:fs';
import { Agent as HttpsAgent, request as httpsRequest } from 'node:https';
import type { Client } from '../api/client.js';
import { BatonError } from '../errors.js';
import { flagString, type ParsedArgs } from '../args.js';
import { ExitCode, usageError } from '../errors.js';
import { json } from '../output.js';

interface GrantView {
  grant_id: string;
  issuer_network_id: string;
  subject_network_id: string;
  scope: string;
  issued_at: string;
  expiry_at: string;
  revoked_at: string | null;
  payload?: string;
  signature?: string;
  public_key?: string;
}

export async function access(args: ParsedArgs, client: Client): Promise<number> {
  const noun = args.positionals[1];
  if (noun === 'grant') return grant(args, client);
  if (noun === 'token') return tokenVerb(args, client);
  if (noun === 'received') return receivedVerb(args, client);
  if (noun === 'fetch') return fetchResource(args);
  throw usageError(
    `access ${noun ?? ''} is not a subcommand`,
    'baton access grant issue|list|revoke | received import|list|rm (A/B management) | token get | fetch <id> (B side)',
  );
}

async function grant(args: ParsedArgs, client: Client): Promise<number> {
  const verb = args.positionals[2];
  switch (verb) {
    case 'issue':  return grantIssue(args, client);
    case 'list':   return grantList(args, client);
    case 'revoke': return grantRevoke(args, client);
    default:
      throw usageError(
        `access grant ${verb ?? ''} is not a subcommand`,
        'baton access grant issue --subject <net_id> --key <b64> --scope <s> --expiry <RFC3339> | list | revoke <grant_id>',
      );
  }
}









async function grantIssue(args: ParsedArgs, client: Client): Promise<number> {
  const subject = flagString(args, 'subject');
  const scope = flagString(args, 'scope');
  const expiry = flagString(args, 'expiry');
  const key = readKey(args);
  if (!subject || !scope || !expiry) {
    throw usageError(
      'access grant issue needs --subject, --scope and --expiry',
      "Endorse a network's key: baton access grant issue --subject net_b17c… --key <B's base64 pubkey> " +
        '--scope read:skill/* --expiry 2026-09-21T12:00:00Z',
    );
  }
  if (Number.isNaN(Date.parse(expiry))) {
    throw usageError(
      '--expiry must be an RFC3339 timestamp',
      'A grant is a dated authorization; give it an explicit end: --expiry 2026-09-21T12:00:00Z',
    );
  }
  if (args.global.dryRun) {
    process.stdout.write(`would endorse ${subject} for ${scope} until ${expiry}. Nothing was signed.\n`);
    return ExitCode.OK;
  }
  const g = await client.post<GrantView>('/networks/self/access-grants', {
    subject_network_id: subject, subject_key: key, scope, expiry_at: expiry,
  });

  const outFile = flagString(args, 'out-file');
  if (outFile) {
    writeFileSync(outFile, json(g) + '\n', { mode: 0o600 });
    process.stdout.write(
      `grant ${g.grant_id} for ${subject} written to ${outFile} (scope ${scope}, until ${expiry}).\n` +
        `  Hand it to ${subject}; revoke any time with: baton access grant revoke ${g.grant_id}\n`,
    );
    return ExitCode.OK;
  }
  process.stdout.write(json(g) + '\n');
  return ExitCode.OK;
}


async function grantList(args: ParsedArgs, client: Client): Promise<number> {
  const res = await client.get<{ grants: GrantView[] }>('/networks/self/access-grants');
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  if (res.grants.length === 0) {
    process.stdout.write('no grants issued.\n');
    return ExitCode.OK;
  }
  for (const g of res.grants) {
    const state = g.revoked_at ? `revoked ${g.revoked_at}` : `active until ${g.expiry_at}`;
    process.stdout.write(`${g.grant_id}  ${g.subject_network_id}  ${g.scope}  (${state})\n`);
  }
  return ExitCode.OK;
}


async function grantRevoke(args: ParsedArgs, client: Client): Promise<number> {
  const grantID = args.positionals[3];
  if (!grantID) {
    throw usageError('access grant revoke needs a grant_id', 'baton access grant revoke grant-3f2c…');
  }
  const res = await client.delete<{ grant_id: string; revoked: boolean; note: string }>(
    `/networks/self/access-grants/${encodeURIComponent(grantID)}`,
  );
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(`revoked ${res.grant_id} — ${res.note}\n`);
  return ExitCode.OK;
}


function readKey(args: ParsedArgs): string {
  const keyFile = flagString(args, 'key-file');
  if (keyFile) return readFileSync(keyFile, 'utf8').trim();
  const key = flagString(args, 'key');
  if (!key) {
    throw usageError(
      "access grant issue needs --key — B's public key",
      "Paste B's network public key (single-line base64), or read it from a file with --key-file <path>.",
    );
  }
  if (key.startsWith('@')) return readFileSync(key.slice(1), 'utf8').trim();
  return key.trim();
}



interface Attestation { payload: string; signature: string }
interface TokenView { token_id: string; payload: string; signature: string; issued_at: string; expiry_at: string; scope: string }

async function tokenVerb(args: ParsedArgs, client: Client): Promise<number> {
  if (args.positionals[2] !== 'get') {
    throw usageError(
      `access token ${args.positionals[2] ?? ''} is not a subcommand`,
      'baton access token get --grant <grant-file> --to <A-endpoint> --ca <A-ca.pem> [--scope <s>] [--out-file <f>]',
    );
  }
  return tokenGet(args, client);
}









async function tokenGet(args: ParsedArgs, client: Client): Promise<number> {
  const grantFile = flagString(args, 'grant');
  if (!grantFile) {
    throw usageError('access token get needs --grant <file>', 'The grant is the file A issued you with `access grant issue --out-file`.');
  }
  const grant = JSON.parse(readFileSync(grantFile, 'utf8')) as GrantView;
  const to = flagString(args, 'to');
  if (!to) throw usageError('access token get needs --to <A-endpoint>', 'Where A answers, e.g. --to https://a.example.com:9443');
  const scope = flagString(args, 'scope') ?? grant.scope;

  if (args.global.dryRun) {
    process.stdout.write(`would ask ${to} for a token on grant ${grant.grant_id} (scope ${scope}). Nothing was signed.\n`);
    return ExitCode.OK;
  }



  const req = await client.post<Attestation>('/networks/self/attestations', {
    purpose: 'access-token-request', grant_id: grant.grant_id, scope,
  });

  const token = await remoteRequest<TokenView>('POST', to, '/networks/self/access-tokens', {
    ca: flagString(args, 'ca'),
    body: {
      grant: { payload: grant.payload, signature: grant.signature },
      request: { payload: req.payload, signature: req.signature },
    },
  });

  const outFile = flagString(args, 'out-file');
  if (outFile) {
    writeFileSync(outFile, json(token) + '\n', { mode: 0o600 });
    process.stdout.write(
      `token ${token.token_id} written to ${outFile} (scope ${token.scope}, expires ${token.expiry_at}).\n` +
        `  Fetch with it: baton access fetch <resource_id> --token ${outFile} --to ${to} --ca <A-ca.pem>\n`,
    );
    return ExitCode.OK;
  }
  process.stdout.write(json(token) + '\n');
  return ExitCode.OK;
}








async function fetchResource(args: ParsedArgs): Promise<number> {
  const id = args.positionals[2];
  if (!id) throw usageError('access fetch needs a resource id', 'baton access fetch <resource_id> --token <file> --to <A-endpoint> --ca <A-ca.pem>');
  const tokenFile = flagString(args, 'token');
  if (!tokenFile) throw usageError('access fetch needs --token <file>', 'The token is what `access token get --out-file` wrote.');
  const to = flagString(args, 'to');
  if (!to) throw usageError('access fetch needs --to <A-endpoint>', 'Where A answers, e.g. --to https://a.example.com:9443');
  const token = JSON.parse(readFileSync(tokenFile, 'utf8')) as TokenView;
  const type = flagString(args, 'type');
  const q = type ? `?type=${encodeURIComponent(type)}` : '';

  const res = await remoteRequest<unknown>('GET', to, `/networks/self/access-resources/${encodeURIComponent(id)}${q}`, {
    ca: flagString(args, 'ca'),
    headers: { 'X-Baton-Access-Token': token.payload, 'X-Baton-Access-Signature': token.signature },
  });
  process.stdout.write(json(res) + '\n');
  return ExitCode.OK;
}

interface ReceivedGrantView {
  grant_id: string; issuer_network_id: string; scope: string;
  expiry_at: string; endpoint: string; ca_fingerprint: string;
}



async function receivedVerb(args: ParsedArgs, client: Client): Promise<number> {
  switch (args.positionals[2]) {
    case 'import': return receivedImport(args, client);
    case 'list':   return receivedList(args, client);
    case 'rm':     return receivedRm(args, client);
    default:
      throw usageError(
        `access received ${args.positionals[2] ?? ''} is not a subcommand`,
        'baton access received import --grant <file> [--endpoint <A>] [--ca <A-ca.pem>] [--resolver dns|cloud-registry] [--domain <d>] | list | rm <grant_id>',
      );
  }
}






















async function receivedImport(args: ParsedArgs, client: Client): Promise<number> {
  const grantFile = flagString(args, 'grant');
  if (!grantFile) {
    throw usageError('access received import needs --grant <file>', 'The grant is the file A issued you (`access grant issue --out-file`).');
  }
  const grant = JSON.parse(readFileSync(grantFile, 'utf8')) as GrantView;
  const caFile = flagString(args, 'ca');




  const resolver = flagString(args, 'resolver');
  if (resolver && resolver !== 'dns' && resolver !== 'cloud-registry') {
    throw usageError(
      `--resolver ${resolver} is not one of dns, cloud-registry`,
      'It records which resolver the peer declared for itself; it is not a list to try in order: each network declares one resolver, and recovery re-asks it and never tries another.',
    );
  }
  const body = {
    grant: { payload: grant.payload, signature: grant.signature, public_key: grant.public_key },
    connection: {
      endpoint: flagString(args, 'endpoint') ?? '',
      ca: caFile ? readFileSync(caFile, 'utf8') : '',
      ...(resolver ? { resolver } : {}),
      ...(flagString(args, 'domain') ? { domain: flagString(args, 'domain') } : {}),
    },
  };
  const res = await client.post<ReceivedGrantView>('/networks/self/received-grants', body);
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    `imported ${res.grant_id} from ${res.issuer_network_id} (scope ${res.scope}).\n` +
      (res.ca_fingerprint ? `  trust ${res.endpoint} at CA ${res.ca_fingerprint}\n` : '') +
      `  list: baton access received list\n`,
  );
  return ExitCode.OK;
}

async function receivedList(args: ParsedArgs, client: Client): Promise<number> {
  const res = await client.get<{ received_grants: ReceivedGrantView[] }>('/networks/self/received-grants');
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  if (res.received_grants.length === 0) {
    process.stdout.write('no received grants.\n');
    return ExitCode.OK;
  }
  for (const g of res.received_grants) {
    process.stdout.write(`${g.grant_id}  from ${g.issuer_network_id}  ${g.scope}  (until ${g.expiry_at})  ${g.endpoint} CA ${g.ca_fingerprint}\n`);
  }
  return ExitCode.OK;
}

async function receivedRm(args: ParsedArgs, client: Client): Promise<number> {
  const grantID = args.positionals[3];
  if (!grantID) throw usageError('access received rm needs a grant_id', 'baton access received rm grant-3f2c…');
  const res = await client.delete<{ grant_id: string; removed: boolean }>(`/networks/self/received-grants/${encodeURIComponent(grantID)}`);
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(`removed ${res.grant_id} from this network's copy.\n`);
  return ExitCode.OK;
}








function remoteRequest<T>(
  method: string,
  endpoint: string,
  path: string,
  opts: { ca?: string; body?: unknown; headers?: Record<string, string> },
): Promise<T> {
  const url = new URL(endpoint.replace(/\/+$/, '') + '/api/v1alpha1' + path);
  const payload = opts.body === undefined ? undefined : JSON.stringify(opts.body);
  const agent = new HttpsAgent(opts.ca ? { ca: readFileSync(opts.ca) } : {});
  return new Promise<T>((resolve, reject) => {
    const req = httpsRequest(
      {
        method, hostname: url.hostname, port: url.port || 443,
        path: url.pathname + url.search, agent,
        headers: {
          ...(payload ? { 'content-type': 'application/json', 'content-length': Buffer.byteLength(payload) } : {}),
          ...(opts.headers ?? {}),
        },
      },
      (res) => {
        const chunks: Buffer[] = [];
        res.on('data', (c: Buffer) => chunks.push(c));
        res.on('end', () => {
          const raw = Buffer.concat(chunks).toString('utf8');
          const status = res.statusCode ?? 0;
          let parsed: unknown;
          try { parsed = raw.length ? JSON.parse(raw) : undefined; }
          catch {
            reject(new BatonError({ code: 'BAD_RESPONSE', message: `A returned ${status} with a body that is not JSON`,
              remediation: 'Check --to points at a BATON control plane, and --ca at its CA.', exitCode: ExitCode.INTERNAL,
              details: { body: raw.slice(0, 200) } }));
            return;
          }
          if (status >= 400) {
            const b = (parsed ?? {}) as { code?: string; message?: string; remediation?: string };
            reject(new BatonError({ code: b.code ?? 'ACCESS_DENIED', message: b.message ?? `A refused with ${status}`,
              remediation: b.remediation ?? 'See A operator: the grant may be revoked, expired, or out of scope.',
              exitCode: ExitCode.UNREACHABLE }));
            return;
          }
          resolve(parsed as T);
        });
      },
    );
    req.on('error', (err) => {




      const code = (err as NodeJS.ErrnoException).code ?? '';
      if (/CERT|SIGNATURE|SELF_SIGNED|CHAIN|VERIFY|DEPTH_ZERO/i.test(code)) {
        reject(new BatonError({ code: 'CA_PIN_MISMATCH',
          message: `A's certificate did not validate against the pinned CA (${code})`,
          remediation: "Do NOT retry. The endpoint is not presenting a certificate this CA signed — confirm the CA and endpoint with A's operator out of band before trusting it.",
          exitCode: ExitCode.UNREACHABLE }));
        return;
      }
      reject(new BatonError({ code: 'UNREACHABLE',
        message: `could not reach A at ${endpoint}: ${(err as Error).message}`,
        remediation: 'Check A is reachable and the endpoint is right.',
        exitCode: ExitCode.UNREACHABLE }));
    });
    if (payload) req.write(payload);
    req.end();
  });
}
