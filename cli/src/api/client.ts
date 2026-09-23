// SPDX-License-Identifier: Apache-2.0










import { readFileSync, existsSync } from 'node:fs';
import { Agent, request } from 'node:https';
import { join } from 'node:path';
import { homedir } from 'node:os';
import { BatonError, ExitCode, preconditionError } from '../errors.js';

export interface ClientOptions {

  baseURL: string;

  adminDir: string;
  timeoutMs?: number;
}

export interface ApiErrorBody {
  code: string;
  message: string;
  request_id?: string;
  details?: Record<string, unknown>;
  remediation?: string;
}











export const API_BASE = '/api/v1alpha1';































export function defaultAdminDir(dataDir?: string): string {
  if (dataDir) {
    const local = join(dataDir, 'admin');




    if (existsSync(join(local, 'ca.crt'))) return local;
  }
  const xdg = process.env.XDG_CONFIG_HOME;
  const base = xdg && xdg.length > 0 ? xdg : join(homedir(), '.config');
  return join(base, 'baton', 'admin');
}

















export function defaultDataDir(): string {
  const xdg = process.env.XDG_CONFIG_HOME;
  const base = xdg && xdg.length > 0 ? xdg : join(homedir(), '.config');
  return join(base, 'baton');
}


function isIPAddress(host: string): boolean {
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(host) || host.includes(':');
}

export class Client {
  private readonly baseURL: string;


  private readonly adminDir: string;
  private readonly agent: Agent;
  private readonly timeoutMs: number;

  constructor(opts: ClientOptions) {
    this.baseURL = opts.baseURL.replace(/\/+$/, '');
    this.adminDir = opts.adminDir;
    this.timeoutMs = opts.timeoutMs ?? 30_000;

    const cert = join(opts.adminDir, 'admin.crt');
    const key = join(opts.adminDir, 'admin.key');
    const ca = join(opts.adminDir, 'ca.crt');

    for (const [label, path] of [['certificate', cert], ['key', key], ['CA bundle', ca]] as const) {
      if (!existsSync(path)) {
        throw preconditionError(
          `admin ${label} not found at ${path}`,




          'Create an agent with `baton agent create --name <n>`, which founds a ' +
            'network here if there is none and issues operator credentials, or ' +
            'point at an existing set with --admin-dir.',
        );
      }
    }

    this.agent = new Agent({
      cert: readFileSync(cert),
      key: readFileSync(key),
      ca: readFileSync(ca),
      keepAlive: false,
      minVersion: 'TLSv1.2',
    });
  }

  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const url = new URL(this.baseURL + API_BASE + path);
    const payload = body === undefined ? undefined : JSON.stringify(body);

    return new Promise<T>((resolve, reject) => {
      const req = request(
        {
          method,
          hostname: url.hostname,
          port: url.port || 443,
          path: url.pathname + url.search,
          agent: this.agent,




          ...(isIPAddress(url.hostname) ? {} : { servername: url.hostname }),
          headers: {
            ...(payload
              ? { 'content-type': 'application/json', 'content-length': Buffer.byteLength(payload) }
              : {}),






            ...(process.env.BATON_ACTING_FOR
              ? { 'x-baton-acting-for': process.env.BATON_ACTING_FOR }
              : {}),
          },
          timeout: this.timeoutMs,
        },
        (res) => {
          const chunks: Buffer[] = [];
          res.on('data', (c: Buffer) => chunks.push(c));
          res.on('end', () => {
            const raw = Buffer.concat(chunks).toString('utf8');
            const status = res.statusCode ?? 0;

            if (status === 204 || raw.length === 0) {
              resolve(undefined as T);
              return;
            }

            let parsed: unknown;
            try {
              parsed = JSON.parse(raw);
            } catch {
              reject(
                new BatonError({
                  code: 'BAD_RESPONSE',
                  message: `the control plane returned ${status} with a body that is not JSON`,
                  remediation: 'Check that --master points at a BATON control plane.',
                  exitCode: ExitCode.INTERNAL,
                  details: { body: raw.slice(0, 200) },
                }),
              );
              return;
            }

            if (status >= 400) {
              reject(apiError(status, parsed as ApiErrorBody));
              return;
            }
            resolve(parsed as T);
          });
        },
      );

      req.on('timeout', () => {
        req.destroy();
        reject(
          new BatonError({
            code: 'TIMEOUT',
            message: `the control plane did not respond within ${this.timeoutMs}ms`,
            remediation: 'Check that it is running with `baton status`, or raise --timeout.',
            exitCode: ExitCode.UNREACHABLE,
          }),
        );
      });

      req.on('error', (err) => reject(connectionError(this.baseURL, err, this.adminDir)));

      if (payload) req.write(payload);
      req.end();
    });
  }

  get<T>(path: string): Promise<T> {
    return this.request<T>('GET', path);
  }





















  postStream<T>(path: string, body: NodeJS.ReadableStream, contentLength: number): Promise<T> {
    const url = new URL(this.baseURL + API_BASE + path);
    return new Promise<T>((resolve, reject) => {
      const req = request(
        {
          method: 'POST',
          hostname: url.hostname,
          port: url.port || 443,
          path: url.pathname + url.search,
          agent: this.agent,
          ...(isIPAddress(url.hostname) ? {} : { servername: url.hostname }),
          headers: {
            'content-type': 'application/octet-stream',
            'content-length': contentLength,
          },
          timeout: this.timeoutMs,
        },
        (res) => {
          const chunks: Buffer[] = [];
          res.on('data', (c: Buffer) => chunks.push(c));
          res.on('end', () => {
            const raw = Buffer.concat(chunks).toString('utf8');
            const status = res.statusCode ?? 0;
            let parsed: unknown;
            try {
              parsed = raw ? JSON.parse(raw) : {};
            } catch {
              parsed = { code: 'BAD_RESPONSE', message: raw.slice(0, 200) };
            }
            if (status >= 400) {
              reject(apiError(status, parsed as ApiErrorBody));
              return;
            }
            resolve(parsed as T);
          });
        },
      );
      req.on('timeout', () => {
        req.destroy();
        reject(
          new BatonError({
            code: 'TIMEOUT',
            message: `the control plane did not respond within ${this.timeoutMs}ms`,
            remediation: 'Check that it is running with `baton status`, or raise --timeout.',
            exitCode: ExitCode.UNREACHABLE,
          }),
        );
      });
      req.on('error', (err) => reject(connectionError(this.baseURL, err, this.adminDir)));
      body.pipe(req);
    });
  }



  postRaw(path: string): Promise<Buffer> {
    const url = new URL(this.baseURL + API_BASE + path);
    return new Promise<Buffer>((resolve, reject) => {
      const req = request(
        {
          method: 'POST',
          hostname: url.hostname,
          port: url.port || 443,
          path: url.pathname + url.search,
          agent: this.agent,
          ...(isIPAddress(url.hostname) ? {} : { servername: url.hostname }),
          timeout: this.timeoutMs,
        },
        (res) => {
          const chunks: Buffer[] = [];
          res.on('data', (c: Buffer) => chunks.push(c));
          res.on('end', () => {
            const raw = Buffer.concat(chunks);
            const status = res.statusCode ?? 0;
            if (status >= 400) {
              let parsed: unknown;
              try {
                parsed = JSON.parse(raw.toString('utf8'));
              } catch {
                parsed = { code: 'BAD_RESPONSE', message: raw.toString('utf8').slice(0, 200) };
              }
              reject(apiError(status, parsed as ApiErrorBody));
              return;
            }
            resolve(raw);
          });
        },
      );
      req.on('timeout', () => {
        req.destroy();
        reject(
          new BatonError({
            code: 'TIMEOUT',
            message: `the control plane did not respond within ${this.timeoutMs}ms`,
            remediation: 'Check that it is running with `baton status`, or raise --timeout.',
            exitCode: ExitCode.UNREACHABLE,
          }),
        );
      });
      req.on('error', (err) => reject(connectionError(this.baseURL, err, this.adminDir)));
      req.end();
    });
  }
  post<T>(path: string, body?: unknown): Promise<T> {
    return this.request<T>('POST', path, body);
  }
  delete<T>(path: string): Promise<T> {
    return this.request<T>('DELETE', path);
  }
  put<T>(path: string, body?: unknown): Promise<T> {
    return this.request<T>('PUT', path, body);
  }
}








function apiError(status: number, body: ApiErrorBody): BatonError {
  const exitCode =
    status === 401 || status === 403
      ? ExitCode.AUTH
      : status === 404
        ? ExitCode.PRECONDITION
        : status === 409
          ? ExitCode.CONFLICT
          : status === 501
            ? ExitCode.UNSUPPORTED
            : status === 503
              ? ExitCode.UNREACHABLE
              : status >= 500
                ? ExitCode.INTERNAL
                : ExitCode.CONFIG;

  return new BatonError({
    code: body.code ?? `HTTP_${status}`,
    message: body.message ?? `the control plane returned ${status}`,
    remediation: body.remediation ?? 'See the control plane logs for more.',
    exitCode,
    requestId: body.request_id,
    details: body.details,
  });
}


export function connectionError(baseURL: string, err: NodeJS.ErrnoException, adminDir?: string): BatonError {
  const common = { exitCode: ExitCode.UNREACHABLE, cause: err };

  if (err.code === 'ECONNREFUSED') {
    return new BatonError({
      ...common,
      code: 'CONNECTION_REFUSED',
      message: `nothing is listening at ${baseURL}`,
      remediation: 'Start the control plane with `baton start`, or check --master.',
    });
  }
  if (err.code === 'ENOTFOUND' || err.code === 'EAI_AGAIN') {
    return new BatonError({
      ...common,
      code: 'DNS_FAILED',
      message: `cannot resolve the host in ${baseURL}`,
      remediation: 'Check the hostname in --master, and that DNS is working.',
    });
  }
  if (err.code === 'CERT_HAS_EXPIRED') {
    return new BatonError({
      ...common,
      code: 'CERT_EXPIRED',
      message: 'the control plane certificate has expired',
      remediation: 'Restart the control plane; it reissues its serving certificate on startup.',
    });
  }
  if (err.code === 'UNABLE_TO_VERIFY_LEAF_SIGNATURE' || err.code === 'SELF_SIGNED_CERT_IN_CHAIN') {
    return new BatonError({
      ...common,
      code: 'CERT_UNTRUSTED',















      message: 'the control plane certificate is not signed by the CA in your admin directory',
      remediation:
        `This is a fact, not a guess: the certificate ${baseURL} presented was issued by a ` +
        'different CA than the one here' +
        (adminDir ? ` (${adminDir})` : ' (--admin-dir, or the default)') +
        '.\n' +
        '  The usual cause is the one you can check on this machine: a PREVIOUS `agent create` ' +
        'here left those credentials behind, and `destroy` does not remove them — so a network ' +
        'founded afterwards has a new CA while the old admin/ is still on disk.\n' +
        '  Move that directory aside (do not delete it — it may be the only copy of another ' +
        "cluster's credentials) and run the command again, or point --admin-dir at the set that " +
        'belongs to this control plane.',
      exitCode: ExitCode.AUTH,
    });
  }














  const raw = String(err.message ?? '');











  if (err.code === 'ERR_TLS_CERT_ALTNAME_INVALID' ||
      /alternative certificate subject name|does not match certificate's altnames/i.test(raw)) {
    return new BatonError({
      ...common,
      code: 'CERT_HOSTNAME_MISMATCH',
      message: `the control plane's SERVER certificate does not cover the address you dialed (${baseURL})`,
      remediation:
        'This is the serving certificate, not your credentials — nothing was revoked. Connect via an ' +
        'address the certificate names, or have the master reissue it to cover this one: ' +
        '`baton setup master --advertise-url https://<this address>:8443` (the serving certificate ' +
        'is reissued to cover what is advertised).',
      exitCode: ExitCode.AUTH,
    });
  }
  if (/alert (bad certificate|unknown ca|certificate (revoked|expired|unknown))/i.test(raw) ||
      /alert number (42|48|44|46)\b/.test(raw) ||
      /^ERR_TLS/.test(err.code ?? '')) {
    return new BatonError({
      ...common,
      code: 'CERT_REFUSED',
      message: `${baseURL} refused these operator credentials`,
      remediation:
        'The control plane answered — it will not accept this certificate. It may have ' +
        'been revoked (`baton operator list` on the master shows revoked ones), it may have ' +
        'expired, or it may belong to a different cluster. Enrol again with a new invitation.',
      exitCode: ExitCode.AUTH,
    });
  }
  return new BatonError({
    ...common,
    code: 'CONNECTION_FAILED',
    message: `could not reach ${baseURL}: ${err.message}`,
    remediation: 'Check that the control plane is running and reachable.',
  });
}
