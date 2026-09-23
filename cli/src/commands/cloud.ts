// SPDX-License-Identifier: Apache-2.0

import type { Client } from '../api/client.js';
import { defaultDataDir } from '../api/client.js';
import { listTemplates, templatesDir } from '../runtime/template.js';
import type { ParsedArgs } from '../args.js';
import { flagBool, flagString } from '../args.js';
import { createInterface } from 'node:readline';
import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { z } from 'zod';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { json, table } from '../output.js';
import { isLocalHost } from '../runtime/resolver.js';











export function cloudURL(): string {
  return (process.env.BATON_CLOUD_URL ?? 'https://api.batoncloud.org').replace(/\/+$/, '');
}


























export interface Attestation {
  network_id: string;
  public_key: string;
  payload: string;
  signature: string;
  nonce: string;
  timestamp: string;
}

export async function cloudFetch(path: string, init: RequestInit = {}): Promise<{ status: number; body: any }> {
  let res: Response;
  try {
    res = await fetch(cloudURL() + path, {
      ...init,
      headers: { 'content-type': 'application/json', ...(init.headers ?? {}) },
    });
  } catch (err) {



















    const configured = !!process.env.BATON_CLOUD_URL;
    throw new BatonError({
      code: 'CLOUD_UNREACHABLE',
      message: `${cloudURL()} did not answer (${(err as Error).message})`,
      remediation: 'This says nothing about whether this network is registered. ' +
        (configured
          ? 'Check the address you set in BATON_CLOUD_URL and the network path, then retry.'
          : 'BATON_CLOUD_URL is not set, so this used the built-in default — which does not resolve today. ' +
            'The hosted registry is one pluggable resolver among several, and its address is yours to choose: set BATON_CLOUD_URL to the one you use. ' +
            'Everything else works without it — the hosted registry is one of three resolution channels, not a prerequisite.'),
      exitCode: ExitCode.UNREACHABLE,
    });
  }
  const body = await res.json().catch(() => ({}));
  return { status: res.status, body };
}



function promptLine(question: string): Promise<string> {
  return new Promise((resolve) => {
    const rl = createInterface({ input: process.stdin, output: process.stderr });









    let settled = false;
    const done = (v: string) => { if (!settled) { settled = true; rl.close(); resolve(v); } };
    rl.on('close', () => done(''));
    rl.question(question, (answer) => done(answer.trim()));
  });
}

export async function cloud(args: ParsedArgs, client: Client): Promise<number> {
  const sub = args.positionals[1] ?? 'status';

  if (sub === 'status') return status(args, client);
  if (sub === 'connect') return connect(args, client);
  if (sub === 'phone-code') return phoneCode(args);
  if (sub === 'phone-connect') return phoneConnect(args, client);
  if (sub === 'token-check') return tokenCheck(args);
  if (sub === 'disconnect') return disconnect(args, client);
  if (sub === 'templates') return cloudTemplates(args, client);
  if (sub === 'diff') return cloudDiff(args, client);
  if (sub === 'skills' || sub === 'networks') {
    return discovery(args, sub);
  }
  throw usageError(
    `cloud ${sub} is not a subcommand`,
    'Available: baton cloud status | connect --phone <n> | phone-code | phone-connect | token-check | disconnect | skills | templates | diff | networks',
  );
}
















export async function cloudNetworkView(
  client: Client,
  networkId: string,
): Promise<{ bound: string; named: string; address_published: string; visibility: string; name: string | null; address: string }> {
  const path = `/api/networks/${networkId}/status`;
  const bodySha256 = createHash('sha256').update('').digest('hex');
  const sig = await client.post<{ signature: string; timestamp: string; nonce: string }>(
    '/networks/self/cloud-sign', { method: 'GET', path, body_sha256: bodySha256 });
  const res = await cloudFetch(path, {
    method: 'GET',
    headers: { 'x-baton-timestamp': sig.timestamp, 'x-baton-nonce': sig.nonce, 'x-baton-signature': sig.signature },
  });
  if (res.status !== 200) cloudRefused('cloud status', res);
  const c = (res.body?.cloud ?? {}) as Record<string, unknown>;
  return {
    bound: String(c.bound ?? 'unknown'),
    named: String(c.named ?? 'unknown'),














    address_published: String(c.address_published ?? 'unknown'),



    visibility: String(c.visibility ?? 'unknown'),
    name: (c.name ?? null) as string | null,
    address: String(c.address ?? ''),
  };
}


















export interface CloudStatusJSON {
  status: string;
  endpoint: string;
  network_id: string;
  fingerprint: string;
  bound: string;
  named: string;
  address_published: string;

















  reachable: string;
  visibility: string;



  address: string | null;
}
























export function cloudStatusJSON(f: Omit<CloudStatusJSON, 'reachable'>): CloudStatusJSON {
  return { ...f, reachable: f.address_published };
}










export async function syncEndpointsToCloud(client: Client): Promise<{ ok: boolean; note?: string }> {
  const self = await client.get<{ network_id?: string; endpoints?: { address: string; port: number; protocol: string }[] }>('/networks/self');
  if (!self.network_id) return { ok: false, note: 'no network identity' };
  const eps = (self.endpoints ?? [])
    .filter((e) => !isLocalHost(e.address))
    .map((e) => ({ address: e.address, port: e.port, protocol: e.protocol }));
  if (!eps.length) return { ok: false, note: 'no reachable endpoint to publish (loopback does not count) — publish a reachable --endpoint first' };
  const body = Buffer.from(JSON.stringify({ endpoints: eps }), 'utf8');
  const bodySha256 = createHash('sha256').update(body).digest('hex');
  const path = `/api/networks/${self.network_id}/endpoints`;













  const sig = await client.post<{ signature: string; timestamp: string; nonce: string }>('/networks/self/cloud-sign', { method: 'PUT', path, body_sha256: bodySha256 });
  const res = await cloudFetch(path, {
    method: 'PUT',
    headers: {
      'content-type': 'application/json',
      'x-baton-timestamp': sig.timestamp,
      'x-baton-nonce': sig.nonce,
      'x-baton-signature': sig.signature,
    },
    body,
  });
  if (res.status >= 200 && res.status < 300) return { ok: true };
  return { ok: false, note: cloudRefusalNote(cloudURL(), res.status, res.body) };
}





















export function cloudRefusalNote(
  url: string,
  status: number,
  body: { code?: string; message?: string; remediation?: string } | undefined,
): string {
  const code = body?.code ? ` (${body.code})` : '';



  const said = [body?.message, body?.remediation].filter(Boolean).join(' — ');
  return said ? `${url} answered ${status}${code}: ${said}` : `${url} answered ${status}${code}`;
}

async function status(args: ParsedArgs, client: Client): Promise<number> {
  const net = await client.get<{ network_id: string; fingerprint: string }>('/networks/self');





  let locallyEnabled = false;
  try {
    const integs = await client.get<{ items?: { provider: string; enabled: boolean }[] }>('/integrations');
    locallyEnabled = (integs.items ?? []).some((i) => i.provider === 'baton-cloud' && i.enabled);
  } catch {  }




  let bound = 'unknown', named = 'unknown', address_published = 'unknown', visibility = 'unknown', address = '';
  let cloudReached = false;
  if (locallyEnabled) {
    try {
      const view = await cloudNetworkView(client, net.network_id);
      ({ bound, named, address_published, visibility, address } = view);
      cloudReached = true;
    } catch {  }
  }

  const state = !locallyEnabled ? 'not_registered' : !cloudReached ? 'unreachable' : 'connected';


  const out: CloudStatusJSON = cloudStatusJSON({
    status: state,
    endpoint: cloudURL(),
    network_id: net.network_id,
    fingerprint: net.fingerprint,



    bound, named, address_published,



    visibility,
    address: address || null,
  });

  if (args.global.output === 'json') {
    process.stdout.write(json(out) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(`cloud    ${out.endpoint}\nstatus   ${state}\n`);
  if (state === 'not_registered') {
    process.stdout.write('  Not linked to Baton Cloud. Connect with: baton cloud connect --phone <number>\n');
  } else if (state === 'unreachable') {
    process.stdout.write('  Baton Cloud did not answer — bound/named/address_published are unknown, not assumed.\n');
  } else {
    process.stdout.write(`  bound ${bound} · named ${named} · address published ${address_published} · visibility ${visibility}\n`);
    if (address) process.stdout.write(`  address ${address}\n`);
    if (named === 'no') process.stdout.write('  No formal name yet. Claim one: baton network register <name>\n');


    if (address_published === 'no') process.stdout.write('  No public address published — publish one: baton network publish --endpoint https://<host>:<port>\n');
  }
  return ExitCode.OK;
}




















async function connect(args: ParsedArgs, client: Client): Promise<number> {













  const phone = flagString(args, 'phone');
  if (!phone) {
    throw usageError(
      'baton cloud connect needs --phone <number>',
      'A phone verification code authenticates the account (a terminal cannot do the browser sign-in). ' +
        'For example: baton cloud connect --phone 5551234567 --country-code 1 [--sign-up]',
    );
  }
  const countryCode = flagString(args, 'country-code') ?? '1';
  const signUp = flagBool(args, 'sign-up');











  const given = flagString(args, 'code');
  if (!given) {
    const sc = await cloudFetch('/api/auth/od/phone/send-code', {
      method: 'POST',
      body: JSON.stringify({ country_code: countryCode, phone, purpose: signUp ? 'register' : 'login' }),
    });
    if (sc.status !== 200 || !sc.body?.sent) cloudRefused('send-code', sc);


    process.stdout.write(
      `\n  Code sent to +${countryCode} ${phone}.` +
        (sc.body.debug_code ? `  (dev bypass code: ${sc.body.debug_code})` : '') + '\n',
    );
  } else {


    process.stdout.write(`\n  Using the code you passed; no new one was sent (a new one would have replaced it).\n`);
  }




  const code = given ?? (await promptLine('  Enter the code: '));




  if (!code) {
    throw usageError(
      'no verification code entered (stdin gave nothing)',
      'Interactively: run it again and type the code. Scripted: get one with `baton cloud phone-code` and pass `--code <code>`.',
    );
  }

  const lg = await cloudFetch(signUp ? '/api/auth/od/phone/register' : '/api/auth/od/phone/code-login', {
    method: 'POST',
    body: JSON.stringify({ country_code: countryCode, phone, code }),
  });
  if (lg.status !== 200 || !lg.body?.access_token) cloudRefused(signUp ? 'register' : 'code-login', lg);
  const token = lg.body.access_token as string;















  let bindClient = client;
  if (!existsSync(join(dataDir(args), 'compose')) ||
      !readdirSync(join(dataDir(args), 'compose')).some((f) => f.startsWith('master-') && f.endsWith('.yml'))) {




















    const mine = await accountNetworks(token);
    if (mine !== null && mine.length > 0 && !flagBool(args, 'found-new')) {
      throw preconditionError(
        `this account already has ${mine.length} network(s), and this machine has none — connect would found a second one`,
        `Its networks: ${mine.map((n) => n.network_id + (n.network_name ? ` (${n.network_name})` : '')).join(', ')}\n` +
          '  To put THIS machine into one of them, it applies to join — connecting cannot do that:\n' +
          '    baton agent join --name <agent> --network <domain | name@registry | https://host:8443>\n' +
          '  To deliberately found a second network under this account, say so:\n' +
          '    baton cloud connect … --found-new',
      );
    }
    const offices = officesHere(args);
    if (offices.length === 0) {



      throw preconditionError(
        'this machine has no agent yet, so there is nothing to found a network around',
        'Open one first — you choose the name, and it founds the network:\n' +
          '    baton agent create --name <name>\n' +
          '  Then run connect again. Your account is registered; nothing is lost.',
      );
    }
    if (offices.length > 1) {
      throw preconditionError(
        `this machine has ${offices.length} agents and no control plane, so which one founds the network is ambiguous`,
        `Found it explicitly, then connect:\n    baton setup master <${offices.join('|')}>`,
      );
    }
    const { foundMaster } = await import('./create.js');
    const { code: rcMaster, entry } = await foundMaster(args, offices[0]!);
    if (rcMaster !== ExitCode.OK) return rcMaster;



    const { Client: C } = await import('../api/client.js');
    bindClient = new C({
      baseURL: entry,
      adminDir: args.global.adminDir ?? join(dataDir(args), 'admin'),
      timeoutMs: args.global.timeoutMs,
    });
  }

  const bound = await bindAccount(bindClient, token);
  process.stdout.write(`\n  Cloud's passkey stored — fingerprint ${bound.fingerprint}\n`);
  process.stdout.write(
    `\n  connected ${bound.network_id}\n` +
      `  ${bound.public_key_line}  ${bound.identity_fingerprint}\n` +
      `  binding    ${bound.binding_id} (bound — this network and Baton Cloud can each verify the other)\n` +
      (bound.address
        ? `  address    ${bound.address}${bound.named ? '' : '  (a default id address)'}\n`
        : '') +
      (bound.endpoints_advertised
        ? '  reachable endpoints advertised to the registry\n'
        : '  no reachable endpoint advertised yet — publish one so the address resolves:\n    baton network publish --endpoint https://<reachable host>:<port>\n') +
      (bound.named ? '' : '\n  Claim a formal name with: baton network register <name>\n'),
  );
  return ExitCode.OK;
}


interface BoundAccount {
  network_id: string;
  binding_id: string;
  integration_id: string;
  fingerprint: string;

  state: string;
  address: string;
  named: boolean;
  endpoints_advertised: boolean;
  public_key_line: string;
  identity_fingerprint: string;
}










async function bindAccount(client: Client, token: string): Promise<BoundAccount> {






  const att = await client.post<Attestation>('/networks/self/attestations', {
    purpose: 'cloud-register',
    network_name: '',
    endpoints: [],
  });

  const reg = await cloudFetch('/api/networks/register', {
    method: 'POST',
    headers: { authorization: `Bearer ${token}` },
    body: JSON.stringify({
      network_id: att.network_id,
      public_key: att.public_key,


      network_name: '',
      signed_record: att.signature,
      payload: att.payload,
    }),
  });
  if (reg.status !== 201) cloudRefused('register the network', reg);
















  const canonical = (reg.body.cloud_address ?? '') as string;
  if (canonical) {
    await client.post('/networks/self/addresses', { address: canonical, source: 'cloud-registry' });
  }









  const scopes = ['skill.install', 'resource.install', 'resource.update', 'resource.read'];
  const mint = await cloudFetch('/api/bindings', {
    method: 'POST',
    headers: { authorization: `Bearer ${token}` },
    body: JSON.stringify({ provider: 'baton-cloud', target_network: att.network_id, scopes, label: 'Baton Cloud' }),
  });
  if (mint.status !== 200 && mint.status !== 201) cloudRefused('mint the binding', mint);
  const bindingID = mint.body.binding_id as string;
  const passkey = mint.body.passkey as string;
  const fingerprint = mint.body.fingerprint as string;



  const alreadyBound = mint.body.state === 'bound';




  let integID: string;
  const existing = await client.get<{ items?: { integration_id: string; provider: string; public_key: string }[] }>('/integrations');
  const already = (existing.items ?? []).find((i) => i.provider === 'baton-cloud' && i.public_key.trim() === passkey.trim());
  if (already) {
    integID = already.integration_id;
  } else {
    const integ = await client.post<{ integration_id: string }>('/integrations', { provider: 'baton-cloud', public_key: passkey, scopes });
    integID = integ.integration_id;
  }










  if (!alreadyBound) {
    const confirmPath = `/api/bindings/${bindingID}/confirm`;
    const confirmBody = JSON.stringify({ network_id: att.network_id, provider_key_fingerprint: fingerprint });
    const bodySha256 = createHash('sha256').update(confirmBody).digest('hex');
    const sig = await client.post<{ signature: string; timestamp: string; nonce: string }>(
      '/networks/self/cloud-sign', { method: 'POST', path: confirmPath, body_sha256: bodySha256 });
    const conf = await cloudFetch(confirmPath, {
      method: 'POST',
      headers: {
        'x-baton-timestamp': sig.timestamp,
        'x-baton-nonce': sig.nonce,
        'x-baton-signature': sig.signature,
      },
      body: confirmBody,
    });
    if (conf.status !== 200) cloudRefused('confirm the binding', conf);
  }







  await client.post(`/integrations/${integID}/enable`, {});







  try {
    const existing = await client.get<{ items?: { integration_id: string; provider: string }[] }>('/integrations');
    for (const i of existing.items ?? []) {
      if (i.provider === 'baton-cloud' && i.integration_id !== integID) {
        await client.delete(`/integrations/${i.integration_id}`);
      }
    }
  } catch {  }






  const adv = await syncEndpointsToCloud(client).catch((e) => ({ ok: false, note: e instanceof Error ? e.message : String(e) }));
  const cloudBlk = (reg.body.cloud ?? {}) as { address?: string; named?: string };
  return {
    network_id: att.network_id,
    binding_id: bindingID,
    integration_id: integID,
    fingerprint,
    state: String(mint.body.state ?? (alreadyBound ? 'bound' : 'confirmed')),
    address: String(cloudBlk.address ?? reg.body.cloud_address ?? ''),
    named: cloudBlk.named === 'yes',
    endpoints_advertised: adv.ok,
    public_key_line: `${att.public_key.trim().split('\n')[1]?.slice(0, 24)}…`,
    identity_fingerprint: String(reg.body.identity?.fingerprint ?? ''),
  };
}





















































const PhoneCodeInput = z.object({
  country_code: z.string().regex(/^\d{1,4}$/),
  phone: z.string().regex(/^\d{4,15}$/),
  purpose: z.enum(['auth', 'login', 'register']),
});

const PhoneConnectInput = PhoneCodeInput.extend({
  code: z.string().regex(/^\d{4,8}$/),
});

const TokenCheckInput = z.object({
  access_token: z.string().min(1),
});

function readStdinJSON<T>(schema: z.ZodType<T>, what: string): T {
  let raw: string;
  try {
    raw = readFileSync(0, 'utf8');
  } catch {
    throw usageError(`${what} reads its input from stdin`, 'Pipe one JSON object on stdin; nothing goes on the command line.');
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw usageError(`${what}: stdin is not JSON`, 'Pipe one JSON object on stdin.');
  }
  const r = schema.safeParse(parsed);
  if (!r.success) {
    throw usageError(`${what}: ${r.error.issues.map((i) => `${i.path.join('.') || '(root)'}: ${i.message}`).join('; ')}`,
      'Fields: country_code (digits), phone (digits), purpose (auth|login|register)' + (what.endsWith('connect') ? ', code (digits)' : '') + '.');
  }
  return r.data;
}


























































function cloudRefused(step: string, res: { status: number; body: any }): never {
  const code = typeof res.body?.code === 'string' ? res.body.code : String(res.status);
  const message = typeof res.body?.message === 'string' ? res.body.message : '';
  throw new BatonError({
    code,
    message: `${step}: ${code}${message ? ` — ${message}` : ''}`,
    remediation: typeof res.body?.remediation === 'string' ? res.body.remediation : '',
    exitCode: ExitCode.PRECONDITION,
    details: { step, status: res.status, upstream: res.body ?? null },
  });
}

async function phoneCode(args: ParsedArgs): Promise<number> {
  const input = readStdinJSON(PhoneCodeInput, 'baton cloud phone-code');
  const sc = await cloudFetch('/api/auth/od/phone/send-code', { method: 'POST', body: JSON.stringify(input) });
  if (sc.status !== 200 || !sc.body?.sent) cloudRefused('send-code', sc);
  const out: Record<string, unknown> = { sent: true, expires_in: sc.body.expires_in ?? null, purpose: input.purpose };
  if (sc.body.debug_code) out.debug_code = sc.body.debug_code;
  if (sc.body.real_sms_bypassed !== undefined) out.real_sms_bypassed = sc.body.real_sms_bypassed;
  process.stdout.write((args.global.output === 'json' ? JSON.stringify(out) : `code sent (expires in ${out.expires_in ?? '?'} s)`) + '\n');
  return ExitCode.OK;
}




















async function tokenCheck(args: ParsedArgs): Promise<number> {
  const input = readStdinJSON(TokenCheckInput, 'baton cloud token-check');
  const me = await cloudFetch('/api/auth/me', { headers: { authorization: `Bearer ${input.access_token}` } });
  if (me.status === 401 || me.status === 403) {
    process.stdout.write((args.global.output === 'json' ? JSON.stringify({ valid: false, status: me.status, upstream: me.body ?? null }) : `not valid (${me.status})`) + '\n');
    return ExitCode.OK;
  }
  if (me.status !== 200 || typeof me.body?.account_id !== 'string') cloudRefused('token-check', me);
  const out: Record<string, unknown> = { valid: true, account_id: me.body.account_id as string };


  if (typeof me.body.expires_in === 'number') out.expires_in = me.body.expires_in;
  process.stdout.write((args.global.output === 'json' ? JSON.stringify(out) : `valid — ${out.account_id}`) + '\n');
  return ExitCode.OK;
}

async function phoneConnect(args: ParsedArgs, client: Client): Promise<number> {
  const input = readStdinJSON(PhoneConnectInput, 'baton cloud phone-connect');
  const path = input.purpose === 'auth'
    ? '/api/auth/od/phone/code-auth'
    : input.purpose === 'register' ? '/api/auth/od/phone/register' : '/api/auth/od/phone/code-login';
  const lg = await cloudFetch(path, {
    method: 'POST',
    body: JSON.stringify({ country_code: input.country_code, phone: input.phone, code: input.code }),
  });
  if (lg.status !== 200 || typeof lg.body?.access_token !== 'string') cloudRefused(input.purpose, lg);
  const token = lg.body.access_token as string;
  const me = await cloudFetch('/api/auth/me', { headers: { authorization: `Bearer ${token}` } });
  if (me.status !== 200 || typeof me.body?.account_id !== 'string') cloudRefused('me', me);
  const bound = await bindAccount(client, token);
  const out = {
















    ...(typeof me.body.phone === 'string' && me.body.phone ? { phone: me.body.phone as string } : {}),
    ...(typeof me.body.email === 'string' && me.body.email ? { email: me.body.email as string } : {}),



    ...(typeof lg.body.access_token === 'string' ? { access_token: lg.body.access_token as string } : {}),


    ...(lg.body.created === undefined ? {} : { created: lg.body.created as boolean }),
    account_created: lg.body.account_created as boolean | undefined,
    account_id: me.body.account_id as string,
    network_id: bound.network_id,
    binding_id: bound.binding_id,
    integration_id: bound.integration_id,
    fingerprint: bound.fingerprint,
    state: bound.state,
    address: bound.address || null,
    named: bound.named,
    endpoints_advertised: bound.endpoints_advertised,
  };
  process.stdout.write((args.global.output === 'json' ? JSON.stringify(out) : `connected ${out.network_id} — binding ${out.binding_id} (${out.state}), passkey ${out.fingerprint}`) + '\n');
  return ExitCode.OK;
}













async function disconnect(args: ParsedArgs, client: Client): Promise<number> {
  const integs = await client.get<{ items?: { integration_id: string; provider: string; enabled: boolean }[] }>('/integrations');
  const live = (integs.items ?? []).filter((i) => i.provider === 'baton-cloud' && i.enabled);
  if (live.length === 0) {
    process.stdout.write('  not connected to Baton Cloud — nothing to disconnect.\n');
    return ExitCode.OK;
  }
  for (const i of live) {
    await client.post(`/integrations/${i.integration_id}/disable`, {});
  }
  process.stdout.write(
    '  disconnected from Baton Cloud.\n' +
      '  The passkey is kept (disabled); reconnect with `baton cloud connect` — the key does not change.\n',
  );
  return ExitCode.OK;
}

















function dataDir(args: ParsedArgs): string {
  return args.global.dataDir ?? defaultDataDir();
}











async function accountNetworks(token: string): Promise<{ network_id: string; network_name?: string }[] | null> {
  try {
    const res = await cloudFetch('/api/account/overview', { headers: { authorization: `Bearer ${token}` } });
    if (res.status !== 200) return null;
    const items = (res.body as { networks?: unknown })?.networks;
    if (!Array.isArray(items)) return null;
    return items
      .filter((n): n is { network_id: string; network_name?: string } =>
        !!n && typeof (n as { network_id?: unknown }).network_id === 'string')
      .map((n) => ({ network_id: n.network_id, network_name: typeof n.network_name === 'string' ? n.network_name : undefined }));
  } catch {
    return null;
  }
}


function officesHere(args: ParsedArgs): string[] {
  const dir = join(dataDir(args), 'nodes');
  if (!existsSync(dir)) return [];
  return readdirSync(dir).filter((f) => f.endsWith('.json')).map((f) => f.replace(/\.json$/, '')).sort();
}













async function directoryTemplates(client: Client): Promise<any[]> {


  let connected = false;
  try {
    const [net, integs] = await Promise.all([
      client.get<{ addresses: { source: string }[] }>('/networks/self'),
      client.get<{ items?: { provider: string; enabled: boolean }[] }>('/integrations'),
    ]);
    const hosted = net.addresses.some((a) => a.source === 'cloud-registry');
    const bound = (integs.items ?? []).some((i) => i.provider === 'baton-cloud' && i.enabled);
    connected = hosted || bound;
  } catch (err) {











    if (!(err instanceof BatonError)) throw err;
  }
  if (!connected) {
    throw new BatonError({
      code: 'NOT_CONNECTED',
      message: 'not connected to Baton Cloud',
      remediation: 'Connect first: baton cloud connect',
      exitCode: ExitCode.AUTH,
    });
  }
  const res = await cloudFetch('/api/discovery/templates');
  if (res.status === 403) {
    throw new BatonError({
      code: 'AUTHORIZE_REQUIRED',
      message: 'this network is not authorised to read the directory',
      remediation: 'Connect it with: baton cloud connect',
      exitCode: ExitCode.AUTH,
    });
  }
  if (res.status !== 200) {
    throw new BatonError({
      code: String(res.body?.code ?? `HTTP_${res.status}`),
      message: 'could not read the Cloud template directory',
      remediation: `The directory answered ${res.status}. Retry, or check ${cloudURL()}.`,
      exitCode: ExitCode.UNREACHABLE,
    });
  }
  return Array.isArray(res.body?.items) ? res.body.items : [];
}

















export interface OfficialSide { template_id: string | null; sha: string | null }










export function diffOfficial(
  here: Map<string, OfficialSide>,
  there: Map<string, OfficialSide>,
): { name: string; where: string; id_match: string | null; bytes_match: string | null; here: OfficialSide | null; directory: OfficialSide | null }[] {
  const names = [...new Set([...here.keys(), ...there.keys()])].sort();
  return names.map((name) => {
    const h = here.get(name);
    const d = there.get(name);
    const where = h && d ? 'both' : h ? 'only here' : 'only in the directory';








    const idMatch = !h || !d ? null : h.template_id === null ? 'not bound here' : h.template_id === d.template_id ? 'same' : 'different';
    const bytesMatch = !h || !d ? null : h.sha === null || d.sha === null ? 'unknown' : h.sha === d.sha ? 'same' : 'different';
    return { name, where, id_match: idMatch, bytes_match: bytesMatch, here: h ?? null, directory: d ?? null };
  });
}






















async function cloudDiff(args: ParsedArgs, client: Client): Promise<number> {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const raw = await directoryTemplates(client);

  const there = new Map<string, OfficialSide>();






  if (raw.length > 0 && !raw.some((t) => typeof (t as any)?.official === 'boolean')) {
    throw new BatonError({
      code: 'DIRECTORY_SHAPE',
      message: `the directory returned ${raw.length} template(s), none carrying an \`official\` field`,
      remediation: 'This CLI decides officialness from that field. The directory changed shape, or this build is older than it — compare against the api-contract before trusting any diff.',
      exitCode: ExitCode.UNSUPPORTED,
    });
  }
  for (const t of raw) {
    if ((t as any)?.official !== true) continue;
    const name = typeof (t as any)?.name === 'string' ? (t as any).name : null;
    if (!name) continue;
    there.set(name, {
      template_id: typeof (t as any)?.template_id === 'string' ? (t as any).template_id : null,
      sha: typeof (t as any)?.spec_sha256 === 'string' && (t as any).spec_sha256 ? (t as any).spec_sha256 : null,
    });
  }

  const here = new Map<string, OfficialSide>();
  for (const row of listTemplates(dataDir)) {
    if (!row.official) continue;
    let sha: string | null = null;
    try {
      sha = 'sha256:' + createHash('sha256').update(readFileSync(row.path)).digest('hex');
    } catch {  }
    here.set(row.name, { template_id: row.template_id, sha });
  }

  const rows = diffOfficial(here, there);

  if (args.global.output === 'json') {
    process.stdout.write(json({ kind: 'CloudTemplateDiff', endpoint: cloudURL(), total: rows.length, items: rows }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(table(
    rows,
    [
      { header: 'name', get: (r) => r.name },
      { header: 'where', get: (r) => r.where },
      { header: 'id', get: (r) => r.id_match ?? '-' },
      { header: 'bytes', get: (r) => r.bytes_match ?? '-' },
    ],
    `The directory lists no official templates, and neither does ${templatesDir(dataDir)}.`,
  ) + '\n' +
    '\n  Nothing was written. Replacing a local file is its owner\'s decision;\n' +
    '  a template whose bytes differ is re-registered by publishing it, not by editing a hash.\n');
  return ExitCode.OK;
}











export function projectDirectoryTemplate(t: any) {






























  const declared = t?.declared_secrets;
  const secretsKnown = Array.isArray(declared);
  const secrets = (secretsKnown ? declared : []).map((s: any) =>
    typeof s === 'string'
      ? { name: s, label: null, provider: null, help_url: null }
      : {
          name: s?.name ?? null, label: s?.label ?? null,
          provider: s?.provider ?? null, help_url: s?.help_url ?? null,
          ...(s?.required === undefined ? {} : { required: s.required as boolean }),
          ...(s?.plugin === undefined ? {} : { plugin: s.plugin as string }),
        },
  );
  const rq = t?.resources ?? {};
  return {













    template_id: t?.template_id ?? null,
    name: t?.name ?? null,
    version: t?.version ?? null,
    publisher: t?.publisher ?? null,
    summary: t?.summary ?? null,
    image: t?.image ?? null,
    resources: { cpu: rq.cpu ?? null, memory: rq.memory ?? null },




    node: { vcpu: t?.node?.vcpu ?? null, memory: t?.node?.memory ?? null, volumeSize: t?.node?.disk ?? null },

    ...(secretsKnown ? { secrets } : {}),


    ...(typeof t?.secrets_error === 'string' && t.secrets_error ? { secrets_error: t.secrets_error as string } : {}),
    spec_sha256: t?.spec_sha256 ?? null,
    published_at: t?.published_at ?? null,




















    fetch: { via: 'cloud-template', ref: t?.template_id ?? null },
    error: null,
  };
}

async function cloudTemplates(args: ParsedArgs, client: Client): Promise<number> {




  const raw: any[] = await directoryTemplates(client);
  const items = raw.map(projectDirectoryTemplate);
  const envelope = { kind: 'CloudTemplateList', endpoint: cloudURL(), total: items.length, items };

  if (args.global.output === 'json') {
    process.stdout.write(json(envelope) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    table(
      items,
      [
        { header: 'name', get: (i) => i.name ?? '—' },
        { header: 'version', get: (i) => i.version ?? '—' },
        { header: 'publisher', get: (i) => i.publisher ?? '—' },
        { header: 'cpu', get: (i) => i.resources.cpu ?? '—' },
        { header: 'memory', get: (i) => i.resources.memory ?? '—' },




        { header: 'secrets', get: (i) => (i.secrets === undefined ? 'unknown' : i.secrets.length ? i.secrets.map((s: { name: string | null }) => s.name).join(',') : '—') },
        { header: 'summary', get: (i) => i.summary ?? '' },
      ],
      'The Cloud recommends no templates yet.',
    ) + '\n',
  );
  return ExitCode.OK;
}

async function discovery(args: ParsedArgs, kind: string): Promise<number> {
  const res = await cloudFetch(`/api/discovery/${kind}`);



  if (res.status === 403) {
    throw new BatonError({
      code: 'AUTHORIZE_REQUIRED',
      message: 'this network is not authorised to read the directory',
      remediation: 'Connect it with: baton cloud connect',
      exitCode: ExitCode.AUTH,
    });
  }
  if (res.status !== 200) {
    throw new BatonError({
      code: String(res.body?.code ?? `HTTP_${res.status}`),
      message: 'could not read the directory',
      remediation: `The directory answered ${res.status}. Retry, or check ${cloudURL()}.`,
      exitCode: ExitCode.UNREACHABLE,
    });
  }
  if (args.global.output === 'json') {
    process.stdout.write(json(res.body) + '\n');
    return ExitCode.OK;
  }
  const items: any[] = res.body.items ?? [];
  process.stdout.write(
    table(
      items,
      [
        { header: 'name', get: (i) => i.name },
        { header: 'version', get: (i) => i.version ?? '—' },
        { header: 'publisher', get: (i) => i.publisher ?? '—' },
        { header: 'summary', get: (i) => i.summary ?? '' },
      ],
      `The directory has no ${kind}.`,
    ) + '\n',
  );
  return ExitCode.OK;
}
