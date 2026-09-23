// SPDX-License-Identifier: Apache-2.0









































import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { request as httpsRequest } from 'node:https';
import { join } from 'node:path';

import type { Client } from '../api/client.js';
import { API_BASE, defaultAdminDir } from '../api/client.js';
import { generateKeyAndCsr } from '../api/csr.js';
import { flagBool, flagString, type ParsedArgs } from '../args.js';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { json } from '../output.js';




const BASE = '';

interface InviteResponse {
  name: string;
  code: string;
  expires_at: string;
  token_id: string;
  ca_fingerprint: string;
}

interface EnrolResponse {
  name: string;
  certificate_pem: string;
  ca_bundle_pem: string;
  cert_not_after: string;
  serial: string;
}

interface OperatorView {
  serial: string;
  name: string;
  fingerprint_sha256: string;
  issued_at: string;
  not_after: string;
  issued_by: string;
  from_ip: string;
  revoked_at: string | null;
  revoke_reason?: string;
}

export async function operator(args: ParsedArgs, client?: Client): Promise<number> {
  const sub = args.positionals[1] ?? '';
  switch (sub) {
    case 'invite': return invite(args, need(client, sub));
    case 'enrol':
    case 'enroll': return enrol(args);
    case 'list':   return list(args, need(client, sub));
    case 'revoke': return revoke(args, need(client, sub));
    default:
      throw usageError(
        `baton operator ${sub || '<subcommand>'} is not a subcommand`,
        'invite | enrol | list | revoke. `invite` and `revoke` run on the master; ' +
          '`enrol` runs on the machine that wants credentials.',
      );
  }
}




function need(client: Client | undefined, sub: string): Client {
  if (!client) {
    throw preconditionError(
      `baton operator ${sub} needs operator credentials`,
      'Run it on the master, or point at credentials with --admin-dir.',
    );
  }
  return client;
}

async function invite(args: ParsedArgs, client: Client): Promise<number> {
  const name = flagString(args, 'name') ?? args.positionals[2];
  if (!name) {
    throw usageError('baton operator invite needs a name',
      '`baton operator invite --name alice`. The name is the person: it is what the ' +
        'audit log shows for everything they do.');
  }
  const ttl = flagString(args, 'ttl');
  const res = await client.post<InviteResponse>(`${BASE}/operators/invitations`,
    ttl ? { name, ttl } : { name });

  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }


  process.stdout.write(
    `invitation for ${res.name}, expires ${res.expires_at}\n\n` +
    `  baton operator enrol --master <this master's url> \\\n` +
    `    --name ${res.name} \\\n` +
    `    --code ${res.code} \\\n` +
    `    --ca-fingerprint ${res.ca_fingerprint}\n\n` +
    `The code is single-use and shown once. Deliver it over a channel you trust; ` +
    `the fingerprint is what lets the other side tell this master from an impostor.\n`,
  );
  return ExitCode.OK;
}

async function enrol(args: ParsedArgs): Promise<number> {
  const name = flagString(args, 'name');
  const code = flagString(args, 'code') ?? readCodeFile(flagString(args, 'code-file'));
  const pin = flagString(args, 'ca-fingerprint');
  const insecure = flagBool(args, 'insecure');
  const master = args.global.master;

  if (!name) {
    throw usageError('baton operator enrol needs --name',
      'The name the invitation was minted for.');
  }
  if (!code) {
    throw usageError('baton operator enrol needs --code or --code-file',
      '`--code-file` reads it from a file, which keeps the code out of your shell history.');
  }
  if (!pin && !insecure) {
    throw usageError(
      'baton operator enrol needs --ca-fingerprint',
      'It is printed by `baton operator invite` on the master, alongside the code. ' +
        'Without it this machine cannot tell the master from anything else answering ' +
        'that address. `--insecure` skips the check and accepts that.',
    );
  }

  const dir = args.global.adminDir ?? defaultAdminDir(args.global.dataDir);



  const certPath = join(dir, 'admin.crt');
  if (existsSync(certPath) && !flagBool(args, 'force')) {
    throw preconditionError(
      `${dir} already holds operator credentials`,
      'Enrolling would overwrite them. Use --admin-dir for a second set, or --force ' +
        'to replace these. Your invitation code has not been spent.',
    );
  }

  const { privateKeyPem, csrPem } = generateKeyAndCsr(name);

  const res = await postUnverified<EnrolResponse>(master, `${API_BASE}/operators/enroll`, {
    name, code, csr_pem: csrPem, client_ts: new Date().toISOString(),
  });

  const got = rootFingerprint(res.ca_bundle_pem);
  if (pin) {
    if (!got) {
      throw preconditionError(
        'the master returned a CA bundle with no self-signed root in it',
        'Nothing here can be pinned, so nothing here is trusted. Check the address.',
      );
    }
    if (!equalFingerprints(got, pin)) {














      throw new BatonError({
        code: 'CA_FINGERPRINT_MISMATCH',
        message: `the master at ${master} presented CA ${got}, not ${pin}`,
        remediation:
          'Nothing was written here, but your invitation code has been redeemed — ' +
          'checking the fingerprint is what required sending it. Treat the credential as ' +
          'issued to someone else: on the master, run `baton operator revoke <name>` now, ' +
          'then check the address before enrolling again.',
        exitCode: ExitCode.PRECONDITION,
      });
    }
  }

  mkdirSync(dir, { recursive: true, mode: 0o700 });




  writeFileSync(join(dir, 'admin.key'), privateKeyPem, { mode: 0o600 });
  writeFileSync(certPath, res.certificate_pem, { mode: 0o600 });
  writeFileSync(join(dir, 'ca.crt'), res.ca_bundle_pem, { mode: 0o644 });

  if (args.global.output === 'json') {
    process.stdout.write(json({
      name: res.name, serial: res.serial, not_after: res.cert_not_after,
      admin_dir: dir, ca_fingerprint: got,


      ca_pinned: Boolean(pin),
    }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    `enrolled as ${res.name}\n` +
    `  credentials  ${dir}\n` +
    `  serial       ${res.serial}\n` +
    `  expires      ${res.cert_not_after}\n` +
    `  master CA    ${got}${pin ? ' (matched the pin)' : ' (NOT CHECKED — --insecure)'}\n\n` +
    `Every command from here needs --master ${master}, or set it in your config.\n`,
  );
  return ExitCode.OK;
}

async function list(args: ParsedArgs, client: Client): Promise<number> {
  const res = await client.get<{ operators: OperatorView[] }>(`${BASE}/operators`);
  const rows = res.operators ?? [];
  if (args.global.output === 'json') {
    process.stdout.write(json(rows) + '\n');
    return ExitCode.OK;
  }
  if (rows.length === 0) {


    process.stdout.write('no operator certificates have been issued by this master\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    'These are certificates issued, not people currently able to connect —\n' +
    'a certificate cannot say how many copies of its key exist.\n\n');
  for (const o of rows) {
    const state = o.revoked_at ? `revoked ${o.revoked_at}` : `until ${o.not_after}`;
    process.stdout.write(
      `${o.name}  ${o.serial}  ${state}  issued ${o.issued_at} by ${o.issued_by || '-'} from ${o.from_ip || '-'}\n`);
  }
  return ExitCode.OK;
}

async function revoke(args: ParsedArgs, client: Client): Promise<number> {
  const name = flagString(args, 'name') ?? args.positionals[2];
  if (!name) {
    throw usageError('baton operator revoke needs a name',
      '`baton operator revoke alice`. It revokes every certificate that name holds.');
  }
  const reason = flagString(args, 'reason');
  const res = await client.post<{ name: string; revoked: number; serials: string[] }>(
    `${BASE}/operators/${encodeURIComponent(name)}/revoke`, reason ? { reason } : {});

  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }


  process.stdout.write(
    res.revoked === 0
      ? `${name} held no live certificates; nothing was revoked\n`
      : `revoked ${res.revoked} certificate(s) for ${name}: ${res.serials.join(' ')}\n` +
        `They stop working now, not at the next restart.\n`,
  );
  return ExitCode.OK;
}

function readCodeFile(path: string | undefined): string | undefined {
  if (!path) return undefined;


  return readFileSync(path, 'utf8').trim();
}












export function rootFingerprint(bundlePem: string): string | null {







  const blocks = bundlePem.match(
    /-----BEGIN CERTIFICATE-----[\s\S]*?-----END CERTIFICATE-----/g) ?? [];
  const ders = blocks
    .map((block) => block
      .replace(/-----(BEGIN|END) CERTIFICATE-----/g, '')
      .replace(/[^A-Za-z0-9+/=]/g, ''))
    .filter((b64) => b64.length > 0)
    .map((b64) => Buffer.from(b64, 'base64'))
    .filter((der) => der.length > 0);

  const root = ders.at(-1);
  if (!root) return null;
  const hex = createHash('sha256').update(root).digest('hex').toUpperCase();
  return 'SHA256:' + (hex.match(/../g) ?? []).join(':');
}

export function equalFingerprints(a: string, b: string): boolean {
  const norm = (s: string) => s.trim().toUpperCase().replace(/^SHA256:/, '').replace(/:/g, '');
  return norm(a) === norm(b);
}








function postUnverified<T>(master: string, path: string, body: unknown): Promise<T> {
  const url = new URL(master.replace(/\/+$/, '') + path);
  const payload = JSON.stringify(body);
  return new Promise<T>((resolve, reject) => {
    const req = httpsRequest(
      {
        method: 'POST',
        hostname: url.hostname,
        port: url.port || 443,
        path: url.pathname,
        headers: { 'content-type': 'application/json', 'content-length': Buffer.byteLength(payload) },
        rejectUnauthorized: false,
        timeout: 30_000,
        ...(/^[0-9.]+$/.test(url.hostname) || url.hostname.includes(':')
          ? {} : { servername: url.hostname }),
      },
      (res) => {
        let raw = '';
        res.on('data', (c) => (raw += c));
        res.on('end', () => {
          if ((res.statusCode ?? 0) >= 400) {
            let doc: { message?: string; remediation?: string; code?: string } = {};
            try { doc = JSON.parse(raw); } catch {  }
            reject(new BatonError({
              code: doc.code ?? 'ENROL_REFUSED',
              message: doc.message ?? `${master} answered ${res.statusCode}`,
              remediation: doc.remediation ?? 'The master refused the enrolment.',
              exitCode: res.statusCode === 401 || res.statusCode === 403 ? ExitCode.AUTH : ExitCode.PRECONDITION,
            }));
            return;
          }
          try { resolve(JSON.parse(raw) as T); } catch {
            reject(preconditionError(
              `${master} did not answer with JSON`,
              'The address may not be a Baton master, or something else is answering it.'));
          }
        });
      },
    );
    req.on('error', (err) => reject(preconditionError(
      `cannot reach ${master}: ${(err as Error).message}`,
      'Check the address and that the master publishes a port reachable from here ' +
        '(`baton setup master --bind 0.0.0.0 --advertise-url https://<host>:8443`).')));
    req.on('timeout', () => { req.destroy(); });
    req.write(payload);
    req.end();
  });
}
