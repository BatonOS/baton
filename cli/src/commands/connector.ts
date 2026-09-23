// SPDX-License-Identifier: Apache-2.0




















import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { createServer } from 'node:http';
import { connect as tlsConnect, createSecureContext, TLSSocket } from 'node:tls';
import { duplexPair } from 'node:stream';
import type { Duplex } from 'node:stream';
import type { ParsedArgs } from '../args.js';
import { flagString } from '../args.js';
import { ExitCode, preconditionError } from '../errors.js';
import { generateKeyAndCsr } from '../api/csr.js';
import { createFront } from '../connector/front.js';
import { loadIssuerKey } from '../connector/ticket.js';
import { startTunnel } from '../connector/tunnel.js';

function requiredFlag(args: ParsedArgs, name: string, what: string): string {
  const v = flagString(args, name);
  if (!v) throw preconditionError(`connector serve needs --${name}`, `${what}.`);
  return v;
}

function readMaterial(path: string, what: string): string {
  try {
    return readFileSync(path, 'utf8');
  } catch {
    throw preconditionError(`could not read ${path}`, `${what} — provisioning drops it there.`);
  }
}


const HOSTNAME = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/i;










function keygen(args: ParsedArgs): number {
  const hostname = requiredFlag(args, 'hostname', "The workspace's public name, e.g. <id>.workspace.<domain>");
  if (!HOSTNAME.test(hostname)) {
    throw preconditionError(`--hostname ${hostname} is not a DNS name`,
      'A SAN dNSName is ASCII letters, digits, hyphens and dots.');
  }
  const dir = requiredFlag(args, 'dir', 'Where to put entry-tls.key and entry-tls.csr');
  const keyPath = join(dir, 'entry-tls.key');
  const csrPath = join(dir, 'entry-tls.csr');
  if (existsSync(keyPath)) {


    throw preconditionError(`${keyPath} already exists`,
      'If you mean to replace the key, remove it first — any certificate issued for it dies with it.');
  }
  const { privateKeyPem, csrPem } = generateKeyAndCsr(hostname, hostname);
  mkdirSync(dir, { recursive: true });
  writeFileSync(keyPath, privateKeyPem, { mode: 0o600 });
  writeFileSync(csrPath, csrPem, { mode: 0o644 });
  process.stdout.write(
    `wrote ${keyPath} (0600 — stays on this machine)\n` +
    `wrote ${csrPath} (give this to the platform signer; it returns entry-tls.crt)\n`,
  );
  return ExitCode.OK;
}

export async function connectorVerb(args: ParsedArgs): Promise<number> {
  const sub = args.positionals[1] ?? '';
  if (sub === 'keygen') return keygen(args);
  if (sub !== 'serve') {
    process.stderr.write('usage: baton connector serve --ws <id> --rendezvous <host:port> ' +
      '--bearer-file <path> --issuer-key <path> --cert <path> --key <path> --panel-face-port <port>\n' +
      '       baton connector keygen --hostname <fqdn> --dir <dir>\n');
    return ExitCode.CONFIG;
  }

  const ws = requiredFlag(args, 'ws', 'The workspace id this connector serves');
  const rendezvous = requiredFlag(args, 'rendezvous', 'The rendezvous broker, host:port');
  const [rHost, rPortRaw] = rendezvous.split(':');
  const rPort = Number.parseInt(rPortRaw ?? '', 10);
  if (!rHost || !Number.isInteger(rPort) || rPort < 1 || rPort > 65535) {
    throw preconditionError(`--rendezvous ${rendezvous} is not host:port`, 'Give the rendezvous broker as host:port — provisioning supplies it.');
  }
  const faceRaw = requiredFlag(args, 'panel-face-port', "The panel's remote read-only face (its --remote-face-port)");
  const facePort = Number.parseInt(faceRaw, 10);
  if (!Number.isInteger(facePort) || facePort < 1 || facePort > 65535) {
    throw preconditionError(`--panel-face-port ${faceRaw} is not a port`, 'Use 1–65535.');
  }

  const bearer = readMaterial(requiredFlag(args, 'bearer-file', 'The node-scoped rendezvous token'),
    'The bearer token file').trim();
  const issuerKey = loadIssuerKey(readMaterial(requiredFlag(args, 'issuer-key', "The ticket issuer's SPKI public key"),
    'The issuer public key'));
  const cert = readMaterial(requiredFlag(args, 'cert', 'The per-workspace TLS certificate'), 'The certificate');
  const key = readMaterial(requiredFlag(args, 'key', 'The per-workspace TLS private key'), 'The private key');
  const secureContext = createSecureContext({ cert, key });

  const front = createFront({ ws, issuerKey, panelRemoteFacePort: facePort });

  const server = createServer(front.listener);
  server.on('upgrade', front.onUpgrade);

  const log = (line: string) => process.stdout.write(`connector: ${line}\n`);

  const tunnel = startTunnel({
    ws,
    bearer,
    dial: () =>
      new Promise<Duplex>((resolve, reject) => {
        const sock = tlsConnect({ host: rHost, port: rPort, servername: rHost }, () => resolve(sock));
        sock.on('error', reject);
      }),
    onData: (conn, client) => {










      const [towardTunnel, towardTls] = duplexPair();
      conn.pipe(towardTunnel);
      towardTunnel.pipe(conn);
      conn.on('error', () => towardTunnel.destroy());
      conn.on('close', () => towardTunnel.destroy());
      const tls = new TLSSocket(towardTls, { isServer: true, secureContext });
      tls.on('error', () => {
        tls.destroy();
        conn.destroy();
      });
      tls.on('close', () => conn.destroy());
      if (client) log(`session from ${client}`);
      server.emit('connection', tls);
    },
    log,
  });

  log(`serving workspace ${ws}: rendezvous ${rendezvous}, panel face 127.0.0.1:${facePort}`);

  return new Promise((resolve) => {
    process.on('SIGINT', () => {


      tunnel.stop();
      server.close(() => resolve(ExitCode.OK));
      server.closeAllConnections?.();
      setTimeout(() => resolve(ExitCode.OK), 1000).unref();
    });
  });
}
