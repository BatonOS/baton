// SPDX-License-Identifier: Apache-2.0

















import { request } from 'node:https';
import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

import { defaultAdminDir, defaultDataDir, type Client } from '../api/client.js';
import { BatonError, ExitCode, preconditionError } from '../errors.js';
import { execCapture, containerExists, readiness } from './engine.js';
import { resolveNetwork, fetchDescriptor, verifyDescriptor } from './resolver.js';

interface OfficeFile { name: string; node_id?: string; driver_network?: string | null; applied_to?: string | null; network_id?: string | null }


function memberOffices(dataDir: string): OfficeFile[] {
  const dir = join(dataDir, 'nodes');
  if (!existsSync(dir)) return [];
  const out: OfficeFile[] = [];
  for (const f of readdirSync(dir)) {
    if (!f.endsWith('.json')) continue;
    try {
      const o = JSON.parse(readFileSync(join(dir, f), 'utf8')) as OfficeFile;




      out.push(o);
    } catch {  }
  }
  return out;
}

interface MemberCert { cert: string; key: string; ca: string; office: string }







type CertResult =
  | { kind: 'ok'; cert: MemberCert }
  | { kind: 'stopped' }
  | { kind: 'not-member' };


function certOf(office: OfficeFile): CertResult {
  const container = `baton-agent-${office.name}`;



  const state = readiness(container);
  if (state !== 'running' && state !== 'healthy') {
    return containerExists(container) ? { kind: 'stopped' } : { kind: 'not-member' };
  }
  const read = (p: string) => execCapture(container, ['cat', p]);

  if (!execCapture(container, ['test', '-f', '/var/lib/baton/pki/identity.json']).ok) return { kind: 'not-member' };
  const cert = read('/var/lib/baton/pki/node.crt');
  const key = read('/var/lib/baton/pki/node.key');
  const ca = read('/var/lib/baton/pki/ca.crt');
  if (!cert.ok || !key.ok || !ca.ok || !cert.stdout || !key.stdout) return { kind: 'not-member' };
  return { kind: 'ok', cert: { cert: cert.stdout, key: key.stdout, ca: ca.stdout, office: office.name } };
}









function ownNetworkCA(args: { global: { adminDir?: string; dataDir?: string } }): string | null {
  try {
    return readFileSync(join(args.global.adminDir ?? defaultAdminDir(args.global.dataDir), 'ca.crt'), 'utf8').trim();
  } catch { return null; }
}










async function locate(args: { global: { master: string; dataDir?: string; adminDir?: string } }, ref: string, ownClient: Client): Promise<{ endpoint: string; member: MemberCert }> {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const offices = memberOffices(dataDir);


  let ownName = '', ownID = '';
  try {
    const self = await ownClient.get<{ display_name?: string; network_id?: string }>('/networks/self');
    ownName = self.display_name ?? ''; ownID = self.network_id ?? '';
  } catch {  }
  const isOwn = ref === ownName || ref === ownID || ref === args.global.master;














  if (!isOwn && /^net_[0-9a-f]{8,}$/i.test(ref)) {
    let stopped = '';
    for (const o of offices) {
      if (o.network_id !== ref) continue;
      const c = certOf(o);
      if (c.kind === 'stopped') { if (!stopped) stopped = o.name; continue; }
      if (c.kind !== 'ok') continue;


      const ep = masterURLOf(dataDir, o.name);
      if (!ep) continue;
      return { endpoint: ep, member: c.cert };
    }
    if (stopped) throw stoppedOffice(stopped, ref);
    throw new BatonError({
      code: 'NOT_A_MEMBER',
      message: `no office on this machine records membership of ${ref}`,
      remediation:
        'An office that joined before the register recorded network identities has none, ' +
        'and cannot be found by id. Read that network by its address instead, or re-join to record it.',
      exitCode: ExitCode.PRECONDITION,
    });
  }

  if (isOwn) {
    const ownCA = ownNetworkCA(args);
    let stopped = '', foreignOffice = false;
    for (const o of offices) {
      const c = certOf(o);
      if (c.kind === 'stopped') { if (!stopped) stopped = o.name; continue; }
      if (c.kind !== 'ok') continue;




      if (ownCA && c.cert.ca.trim() !== ownCA) { foreignOffice = true; continue; }
      return { endpoint: args.global.master, member: c.cert };
    }
    if (stopped) throw stoppedOffice(stopped, ref);
    throw new BatonError({
      code: 'NOT_A_MEMBER',
      message: `no office on this machine is a member of ${ref}`,
      remediation: foreignOffice
        ? `An office here belongs to a DIFFERENT network (its certificate is from another CA); it is not a member of ${ref}. Create or join an office in this network: baton agent create --name <n>.`
        : 'A network is read as your agent in it. Create and join one: baton agent create --name <n>; baton agent join --name <n> --network <ref>.',
      exitCode: ExitCode.PRECONDITION,
    });
  }


  const resolved = await resolveNetwork(ref);
  let endpoint: string | null = null;
  const refusals: string[] = [];
  for (const ep of resolved.candidates) {
    try {
      const d = await fetchDescriptor(ep);
      if (verifyDescriptor(d, resolved.pin).ok) { endpoint = ep; break; }
      refusals.push(`${ep}: descriptor did not verify`);
    } catch (err) { refusals.push(`${ep}: ${err instanceof Error ? err.message : String(err)}`); }
  }
  if (!endpoint) {
    throw new BatonError({
      code: 'NETWORK_UNREACHABLE',
      message: `could not reach ${ref}`,
      remediation: (refusals.join('\n  ') || 'No candidate endpoint answered.') + '\n  This says nothing about whether you are a member; it is about reaching the network.',
      exitCode: ExitCode.UNREACHABLE,
    });
  }
  const host = new URL(endpoint).host;
  let stopped = '';
  for (const o of offices) {

    try {
      const doc = readFileSync(join(dataDir, 'compose', `agent-${o.name}.yml`), 'utf8');
      if (doc.includes(host) || (o.applied_to && new URL(o.applied_to).host === host)) {
        const c = certOf(o);
        if (c.kind === 'ok') return { endpoint, member: c.cert };
        if (c.kind === 'stopped' && !stopped) stopped = o.name;
      }
    } catch {  }
  }
  if (stopped) throw stoppedOffice(stopped, ref);
  throw new BatonError({
    code: 'NOT_A_MEMBER',
    message: `no office on this machine is a member of ${ref}`,
    remediation: `Join it first: baton agent join --name <n> --network ${ref}, then have its master admit it.`,
    exitCode: ExitCode.PRECONDITION,
  });
}





function stoppedOffice(office: string, ref: string): BatonError {
  return new BatonError({
    code: 'OFFICE_STOPPED',
    message: `your office ${office} in ${ref} is not running, so its membership certificate cannot be read`,
    remediation: `A stopped office is still a member — this is not "you are not a member". Start it, then read again: baton start ${office}.`,
    exitCode: ExitCode.PRECONDITION,
  });
}






export async function readAsMember<T = unknown>(
  args: { global: { master: string; dataDir?: string; adminDir?: string } },
  ref: string,
  path: string,
  ownClient: Client,
): Promise<T> {
  const { endpoint, member } = await locate(args, ref, ownClient);
  const url = new URL(endpoint.replace(/\/+$/, '') + '/api/v1alpha1' + path);
  return new Promise<T>((resolve, reject) => {
    const req = request(
      {
        method: 'GET',
        hostname: url.hostname,
        port: url.port,
        path: url.pathname + url.search,
        cert: member.cert,
        key: member.key,
        ca: member.ca,



        rejectUnauthorized: false,
        ...(/^[0-9.]+$/.test(url.hostname) || url.hostname.includes(':') ? {} : { servername: url.hostname }),
      },
      (res) => {
        let body = '';
        res.on('data', (c) => (body += c));
        res.on('end', () => {
          if ((res.statusCode ?? 0) >= 400) {
            let doc: { message?: string; remediation?: string; code?: string } = {};
            try { doc = JSON.parse(body); } catch {  }
            reject(new BatonError({
              code: doc.code ?? 'NETWORK_READ_FAILED',
              message: doc.message ?? `${ref} answered ${res.statusCode} for ${path}`,
              remediation: doc.remediation ?? 'The network refused the read.',
              exitCode: res.statusCode === 403 ? ExitCode.AUTH : ExitCode.PRECONDITION,
            }));
            return;
          }
          try { resolve(JSON.parse(body) as T); } catch { reject(preconditionError(`${ref} did not answer with JSON`, 'The endpoint may not be a Baton master.')); }
        });
      },
    );
    req.on('error', (err) => {
      const e = err as NodeJS.ErrnoException;





      const tls = /alert|ssl|handshake|certificate|unknown ca/i.test(String(err.message ?? ''))
        || /CERT|SSL|TLS/.test(e.code ?? '');
      if (tls) {
        reject(new BatonError({
          code: 'MEMBERSHIP_MISMATCH',
          message: `${ref} refused this office's membership certificate`,
          remediation: `The office presenting the certificate is not a member of ${ref} (its certificate is from another CA). Read as an office that is actually a member of it.`,
          exitCode: ExitCode.PRECONDITION,
        }));
        return;
      }
      reject(new BatonError({
        code: 'NETWORK_UNREACHABLE',
        message: `could not reach ${ref}` + (e.code ? ` (${e.code})` : ''),
        remediation: 'Check the network is up and reachable from this machine.',
        exitCode: ExitCode.UNREACHABLE,
      }));
    });
    req.end();
  });
}


export { defaultAdminDir };










function masterURLOf(dataDir: string, office: string): string | null {
  try {
    const doc = readFileSync(join(dataDir, 'compose', `agent-${office}.yml`), 'utf8');
    const m = /BATON_MASTER_URL:\s*["']?(\S+?)["']?\s*$/m.exec(doc);
    return m?.[1] ?? null;
  } catch {
    return null;
  }
}
