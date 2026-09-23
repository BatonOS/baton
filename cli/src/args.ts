// SPDX-License-Identifier: Apache-2.0









import { usageError } from './errors.js';
import type { OutputFormat } from './output.js';

export interface GlobalFlags {
  config?: string;
  dataDir?: string;
  master: string;
  adminDir?: string;
  output: OutputFormat;
  yes: boolean;
  dryRun: boolean;
  verbose: boolean;
  timeoutMs: number;
}

export interface ParsedArgs {
  positionals: string[];
  flags: Map<string, string | boolean>;








  repeated: Map<string, string[]>;
  global: GlobalFlags;
}







const BOOL_FLAGS = new Set([

  'with-cache', 'keep-source', 'stdin', 'clear',
  'all', 'follow', 'force', 'keep-data', 'no-open', 'no-snapshot', 'prune',
  'takeover', 'verify', 'yes', 'dry-run', 'help', 'verbose', 'purge-data',



  'reset-default',


  'insecure',




  'status',


  'sign-up',









  'found-new',




  'ssh-config', 'print',


  'json', 'table',




  'no-network',


  'save',


  'with-avatars',






  'allow-remote-shell',
]);

const VALUE_FLAGS = new Set([
  'config', 'data-dir', 'master', 'admin-dir', 'output', 'timeout',




  'code', 'code-file', 'ca-fingerprint', 'reason',

  'plan-digest', 'note',



  'workspace-dir',


  'bind',
  'role', 'component', 'name', 'advertise-url', 'listen-address', 'version',
  'registry', 'enrollment-token-file', 'out-file', 'ttl', 'max-uses',


  'arg', 'instance', 'agent-token',






  'skill',





  'account-token',
  'node', 'input', 'input-file', 'idempotency-key', 'reason', 'command',
  'labels', 'since-seq', 'limit', 'category', 'event', 'confirm-cluster',
  'image-variant', 'port', 'network', 'tail', 'runtime',



  'dist', 'upstream', 'panel-port', 'remote-face-port', 'template', 'owner',



















  'ws', 'rendezvous', 'bearer-file', 'issuer-key', 'panel-face-port', 'cert', 'hostname', 'dir',




  'control-plane-port',



  'attach',



  'attach-ref',


  'parent',
  'resource-id',


  'name',



  'node-id',

  'agent',

  'endpoint', 'domain',




  'resolver',

  'label',




  'text', 'file', 'state', 'to', 'out', 'from',


  'phone', 'country-code', 'code',


  'identity',



  'summary',

  'secret',





  'passkey-file', 'scopes', 'provider',


  'from-file',


  'from', 'file',




  'url', 'sha256',



  'message-id',




  'reply-to',


  'thread', 'box',




  'act-on', 'channels', 'allow', 'allow-network', 'remove', 'remove-network',


  'tag', 'q', 'title', 'scope', 'content-type', 'author',









  'type', 'folder', 'source', 'hash', 'license',




  'binding', 'credential',


  'receipt', 'receipt-out',




  'subject', 'key', 'key-file', 'expiry',




  'grant', 'token', 'ca',
]);

















const MULTI_FLAGS = new Set(['secret', 'tag', 'attach', 'attach-ref', 'arg', 'allow', 'allow-network', 'remove', 'remove-network']);











export function knowsFlag(name: string): boolean {
  const n = name.replace(/^--?/, '');
  return BOOL_FLAGS.has(n) || VALUE_FLAGS.has(n) || MULTI_FLAGS.has(n);
}

export function parse(argv: string[]): ParsedArgs {
  const positionals: string[] = [];
  const flags = new Map<string, string | boolean>();
  const repeated = new Map<string, string[]>();

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]!;

    if (arg === '--') {
      positionals.push(...argv.slice(i + 1));
      break;
    }

    if (arg.startsWith('--')) {
      let name = arg.slice(2);
      let value: string | undefined;

      const eq = name.indexOf('=');
      if (eq >= 0) {
        value = name.slice(eq + 1);
        name = name.slice(0, eq);
      }

      if (VALUE_FLAGS.has(name)) {
        if (value === undefined) {
          value = argv[++i];
          if (value === undefined) {
            throw usageError(`--${name} needs a value`, `Try --${name} <value>.`);
          }
        }
        flags.set(name, value);
        if (MULTI_FLAGS.has(name)) {
          const seen = repeated.get(name) ?? [];
          seen.push(value);
          repeated.set(name, seen);
        }
      } else if (BOOL_FLAGS.has(name)) {
        flags.set(name, value === undefined ? true : value !== 'false');
      } else {




























        throw usageError(
          `--${name} is not a flag baton knows`,
          'Run `baton --help`. Flags are recognised for the whole CLI, not per ' +
            'command, so a flag that belongs to another verb is accepted here and ' +
            'does nothing. For JSON, the flag is `--output json`.',
        );
      }
      continue;
    }

    if (arg.startsWith('-') && arg.length > 1) {
      const short: Record<string, string> = {
        f: 'follow', h: 'help', v: 'verbose', y: 'yes', o: 'output',
      };
      const name = short[arg.slice(1)];
      if (!name) {
        throw usageError(`unknown flag ${arg}`, 'Run `baton --help` for the flag list.');
      }
      if (name === 'output') {
        const value = argv[++i];
        if (!value) throw usageError('-o needs a value', 'Try -o json.');
        flags.set('output', value);
      } else {
        flags.set(name, true);
      }
      continue;
    }

    positionals.push(arg);
  }

  return { positionals, flags, repeated, global: globalFlags(flags) };
}

function globalFlags(flags: Map<string, string | boolean>): GlobalFlags {
  const output = String(flags.get('output') ?? 'table');
  if (output !== 'table' && output !== 'json') {
    throw usageError(
      `--output ${output} is not a format`,
      'Use --output table or --output json.',
    );
  }

  const timeoutRaw = String(flags.get('timeout') ?? '30s');
  const timeoutMs = parseDuration(timeoutRaw);

  return {
    config: strOrUndefined(flags.get('config')),
    dataDir: strOrUndefined(flags.get('data-dir')),
    master: String(flags.get('master') ?? process.env.BATON_MASTER ?? 'https://127.0.0.1:8443'),
    adminDir: strOrUndefined(flags.get('admin-dir')),
    output,
    yes: flags.get('yes') === true,
    dryRun: flags.get('dry-run') === true,
    verbose: flags.get('verbose') === true,
    timeoutMs,
  };
}

function strOrUndefined(v: string | boolean | undefined): string | undefined {
  return typeof v === 'string' ? v : undefined;
}


export function parseDuration(raw: string): number {
  const match = /^(\d+)(ms|s|m|h)?$/.exec(raw.trim());
  if (!match) {
    throw usageError(
      `${raw} is not a duration`,
      'Use a value like 30s, 5m, or 1h.',
    );
  }
  const value = Number.parseInt(match[1]!, 10);
  switch (match[2] ?? 's') {
    case 'ms': return value;
    case 's': return value * 1000;
    case 'm': return value * 60_000;
    default: return value * 3_600_000;
  }
}

export function flagString(
  args: ParsedArgs,
  name: string,
  fallback?: string,
): string | undefined {
  const v = args.flags.get(name);
  return typeof v === 'string' ? v : fallback;
}

export function flagBool(args: ParsedArgs, name: string): boolean {
  return args.flags.get(name) === true;
}

export function requireFlag(args: ParsedArgs, name: string, hint: string): string {
  const v = flagString(args, name);
  if (!v) throw usageError(`--${name} is required`, hint);
  return v;
}

































export function driverNetwork(args: ParsedArgs): { name: string; source: string } {
  const flag = flagString(args, 'network');
  if (flag) return { name: flag, source: '--network' };




  const env = process.env.BATON_DRIVER_NETWORK;
  if (env && env.trim().length > 0) return { name: env, source: 'BATON_DRIVER_NETWORK' };
  return { name: 'baton', source: 'default' };
}
