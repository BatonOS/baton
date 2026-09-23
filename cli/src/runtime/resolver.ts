// SPDX-License-Identifier: Apache-2.0























import { promises as dns } from 'node:dns';
import { hostname } from 'node:os';
import { createPublicKey, verify as cryptoVerify } from 'node:crypto';
import { request as httpsRequest } from 'node:https';

import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { cloudURL } from '../commands/cloud.js';








export const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1', '::1', hostname()]);

export function isLocalHost(address: string): boolean {
  return LOCAL_HOSTS.has(address);
}

export type Channel = 'entry-point' | 'dns' | 'hosted';

export interface Resolution {
  network: string;
  channel: Channel;

  candidates: string[];

  pin: string | null;

  notes: string[];
}

export interface Descriptor {
  network_id: string;
  display_name?: string;
  identity: { public_key: string; fingerprint: string };
  endpoints: { address: string; port: number; protocol: string; priority?: number }[];
  record: { version: number; issued_at?: string; expires_at: string; payload: string; signature: string };
}


export function channelOf(ref: string): Channel {










  if (/^[A-Za-z][A-Za-z0-9+.-]*:\/\//.test(ref)) return 'entry-point';
  if (ref.includes('@')) return 'hosted';
  return 'dns';
}















export function networkKeySource(pinned: boolean, channel: Channel): 'dns' | 'registry' | 'first-contact' {
  if (!pinned) return 'first-contact';
  return channel === 'hosted' ? 'registry' : 'dns';
}













export function networkKeyAuthority(pinned: boolean, channel: Channel, ref: string): string | null {
  if (!pinned) return null;
  if (channel === 'hosted') return ref.split('@')[1] ?? null;
  if (channel === 'dns') return ref;
  return null;
}

const TXT_LABEL = '_baton';


export function parseTXT(txt: string): { v: string; k: string; d?: string; ep?: string[] } {
  const tags = new Map<string, string>();
  for (const part of txt.split(';')) {
    const [key, ...rest] = part.split('=');
    if (!key || rest.length === 0) continue;
    tags.set(key.trim(), rest.join('=').trim());
  }
  const v = tags.get('v') ?? '';
  const k = tags.get('k') ?? '';
  const d = tags.get('d');
  const ep = tags.get('ep')?.split(',').map((s) => s.trim()).filter(Boolean);
  return { v, k, d, ep };
}


export async function resolveNetwork(ref: string): Promise<Resolution> {
  const channel = channelOf(ref);
  if (channel === 'entry-point') {
    return { network: ref, channel, candidates: [ref.replace(/\/+$/, '')], pin: null, notes: ['channel 0: no key to pin; the entry point\'s key is learned on first contact'] };
  }
  if (channel === 'dns') {
    const name = `${TXT_LABEL}.${ref}`;
    let records: string[][];
    try {
      records = await dns.resolveTxt(name);
    } catch (err) {






      throw new BatonError({
        code: 'NETWORK_NOT_NAMED',
        message: `${ref} does not name a network: no ${name} TXT record (${(err as { code?: string }).code ?? String(err)})`,
        remediation: `The domain's owner publishes one: ${name} IN TXT "v=baton1; k=<fingerprint>; ep=https://host:8443" ` +
          '(or d=<descriptor URL> in place of ep=). Recovery re-asks DNS; a hosted name is a different channel, not a fallback.',
        exitCode: ExitCode.PRECONDITION,
      });
    }
    const joined = records.map((chunks) => chunks.join(''));
    const ours = joined.filter((t) => /(^|;)\s*v=baton1(\s*;|$)/.test(t));
    if (ours.length === 0) {
      throw new BatonError({
        code: 'NETWORK_NOT_NAMED',
        message: `${name} has TXT records, but none says v=baton1`,
        remediation: 'Publish it as a TXT record at _baton.<domain>: v=baton1; k=<network key fingerprint>; d=<descriptor URL> or ep=<entry points>. Records under this label that are not ours are ignored, not guessed at.',
        exitCode: ExitCode.PRECONDITION,
      });
    }
    if (ours.length > 1) {


      throw new BatonError({
        code: 'AMBIGUOUS_NETWORK',
        message: `${name} has ${ours.length} v=baton1 records; one domain names one network`,
        remediation: 'Remove all but one.',
        exitCode: ExitCode.CONFLICT,
      });
    }














    const unusable = (message: string, remediation: string) =>
      new BatonError({ code: 'NETWORK_RECORD_UNUSABLE', message, remediation, exitCode: ExitCode.PRECONDITION });
    const rec = parseTXT(ours[0]!);
    if (!/^SHA256:[A-Za-z0-9+/]+$/.test(rec.k)) {
      throw unusable(
        `${name} says v=baton1 but has no usable k= fingerprint`,
        'k= is the network\'s key fingerprint, SHA256:… as `baton network show` prints it. The record names a network; it does not name its key.',
      );
    }
    const candidates: string[] = [];
    const notes: string[] = [`read ${name}`];
    if (rec.ep && rec.ep.length > 0) candidates.push(...rec.ep.map((e) => e.replace(/\/+$/, '')));
    if (rec.d) notes.push(`descriptor document at ${rec.d}`);
    if (candidates.length === 0 && !rec.d) {
      throw unusable(
        `${name} says v=baton1 but names neither ep= nor d=`,
        'One of them is required: where the network answers, or where its descriptor is. The record exists and says nothing about where to go.',
      );
    }
    if (candidates.length === 0 && rec.d) {

      const doc = await fetchDescriptorAt(rec.d);
      candidates.push(...endpointsOf(doc));
      notes.push('entry points taken from the descriptor document');
    }
    return { network: ref, channel, candidates, pin: rec.k, notes };
  }

  const [name, registry] = ref.split('@');
  if (!name || !registry) throw usageError(`${ref} is not a hosted name`, 'Hosted names look like team@<registry domain>.');
  const base = cloudURL();
  const cloudHost = new URL(base).hostname;



  if (!cloudHost.endsWith(registry) && !registry.endsWith(cloudHost.replace(/^api\./, ''))) {
    throw preconditionError(
      `${ref} is under ${registry}, but this build's hosted registry is ${cloudHost} (BATON_CLOUD_URL)`,
      'Point BATON_CLOUD_URL at that registry, or use the network\'s domain / entry point.',
    );
  }
  let res: Response;
  try {
    res = await fetch(`${base}/api/resolve/${encodeURIComponent(name)}`, { signal: AbortSignal.timeout(15000) });
  } catch (err) {
    throw new BatonError({ code: 'CLOUD_UNREACHABLE', message: `${base} did not answer (${(err as Error).message})`, remediation: 'Recovery re-asks the registry; a domain is a different channel, not a fallback.', exitCode: ExitCode.UNREACHABLE });
  }
  if (!res.ok) {















    let doc: { code?: string; message?: string; remediation?: string; error?: { code?: string; message?: string; remediation?: string } } = {};
    try { doc = (await res.json()) as typeof doc; } catch {  }



    const said = doc.error ?? doc;
    if (said.code) {
      throw new BatonError({
        code: said.code,
        message: said.message ?? `${base} refused: ${said.code}`,
        remediation: said.remediation ?? 'The registry named this refusal; it is about the request or the host, not necessarily about the network.',
        exitCode: ExitCode.PRECONDITION,
      });
    }
    if (res.status === 404) {
      throw preconditionError(
        `${base} has no network named ${name}`,
        'Check the name, or ask its operator which registry it is on. ' +
          'This is what THAT host answered — a host that serves a different surface can answer the same way.',
      );
    }
    throw new BatonError({ code: `HTTP_${res.status}`, message: `the registry answered ${res.status} for ${name}`, remediation: 'Retry, or check the registry.', exitCode: ExitCode.UNREACHABLE });
  }
  const body = (await res.json()) as { identity?: { fingerprint?: string }; endpoints?: { address: string; port: number; protocol?: string }[] };
  const pin = body.identity?.fingerprint ?? null;
  const candidates = (body.endpoints ?? []).map((e) => `https://${e.address}:${e.port}`);
  const notes = [`registry ${base} answered`];
  if (candidates.length === 0) notes.push('the registry lists no endpoints for this network yet — it is registered but has not published where it answers');
  return { network: ref, channel, candidates, pin, notes };
}

function endpointsOf(d: Descriptor): string[] {
  return [...d.endpoints].sort((a, b) => (a.priority ?? 0) - (b.priority ?? 0)).map((e) => `https://${e.address}:${e.port}`);
}

async function fetchDescriptorAt(url: string): Promise<Descriptor> {
  let res: Response;
  try {
    res = await fetch(url, { signal: AbortSignal.timeout(15000) });
  } catch (err) {
    throw new BatonError({ code: 'DESCRIPTOR_UNREACHABLE', message: `${url} did not answer (${(err as Error).message})`, remediation: 'The document the network\'s TXT record points at is not reachable from here.', exitCode: ExitCode.UNREACHABLE });
  }
  if (!res.ok) throw new BatonError({ code: 'DESCRIPTOR_UNREACHABLE', message: `${url} answered ${res.status}`, remediation: 'The descriptor document is not being served.', exitCode: ExitCode.UNREACHABLE });
  return (await res.json()) as Descriptor;
}


export function fetchDescriptor(entryPoint: string): Promise<Descriptor> {




  const url = new URL(`${entryPoint.replace(/\/+$/, '')}/api/v1alpha1/network`);
  return new Promise<Descriptor>((resolve, reject) => {
    const req = httpsRequest(url, { method: 'GET', rejectUnauthorized: false, timeout: 15000 }, (res) => {
      const chunks: Buffer[] = [];
      res.on('data', (c: Buffer) => chunks.push(c));
      res.on('end', () => {
        const body = Buffer.concat(chunks).toString('utf8');
        if ((res.statusCode ?? 0) !== 200) {
          let code = `HTTP_${res.statusCode}`; let msg = `${url} answered ${res.statusCode}`;
          try { const e = JSON.parse(body) as { code?: string; message?: string }; if (e.code) { code = e.code; msg = e.message ?? msg; } } catch {  }
          reject(new BatonError({ code, message: msg, remediation: code === 'NETWORK_NOT_PUBLISHED' ? 'The network\'s operator publishes with `baton network publish --endpoint …` first.' : 'The entry point is not serving a descriptor.', exitCode: ExitCode.PRECONDITION }));
          return;
        }
        try { resolve(JSON.parse(body) as Descriptor); } catch { reject(new BatonError({ code: 'DESCRIPTOR_INVALID', message: `${url} did not answer with JSON`, remediation: 'Is that a BATON control plane?', exitCode: ExitCode.PRECONDITION })); }
      });
    });
    req.on('timeout', () => { req.destroy(new Error('timeout')); });
    req.on('error', (err) => reject(new BatonError({ code: 'DESCRIPTOR_UNREACHABLE', message: `${url} did not answer (${err.message})`, remediation: 'Is the entry point reachable from this machine?', exitCode: ExitCode.UNREACHABLE })));
    req.end();
  });
}


export function verifyDescriptor(d: Descriptor, pin: string | null): { ok: boolean; reason?: string } {
  if (!d?.record?.payload || !d.record.signature || !d.identity?.public_key) return { ok: false, reason: 'the descriptor is missing its record or key' };
  let payload: Buffer;
  try {
    payload = Buffer.from(d.record.payload, 'base64');
    const key = createPublicKey(d.identity.public_key);
    const good = cryptoVerify(null, payload, key, Buffer.from(d.record.signature, 'base64'));
    if (!good) return { ok: false, reason: 'record.signature does not verify with identity.public_key' };
  } catch (err) {
    return { ok: false, reason: `could not verify: ${(err as Error).message}` };
  }
  const lines = payload.toString('utf8').split('\n');
  if (lines[0] !== 'network-descriptor' || lines[1] !== d.network_id || lines[2] !== d.identity.fingerprint) {
    return { ok: false, reason: 'the signed bytes do not describe this descriptor (id or fingerprint differ)' };
  }
  if (new Date(d.record.expires_at).getTime() < Date.now()) return { ok: false, reason: `the descriptor expired at ${d.record.expires_at}` };
  if (pin && d.identity.fingerprint !== pin) return { ok: false, reason: `the entry point's key is ${d.identity.fingerprint}, but the network was named with ${pin} — not the same network` };
  return { ok: true };
}
