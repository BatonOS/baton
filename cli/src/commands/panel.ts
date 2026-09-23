// SPDX-License-Identifier: Apache-2.0




















































import { spawnSync } from 'node:child_process';
import { createServer, request as httpRequest, type IncomingMessage, type ServerResponse } from 'node:http';
import { handlePtyUpgrade } from '../panel/pty-proxy.js';
import { API_BASE } from '../api/client.js';
import type { CloudStatusJSON } from './cloud.js';
import { request as httpsRequest } from 'node:https';
import { readFileSync, existsSync, statSync, realpathSync, readdirSync } from 'node:fs';
import { join, normalize, extname, resolve as resolvePath } from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  AUTH_DEFAULTS, bearerFrom, checkCredentials, issueToken, passwordIsDefault, revokeAllTokens,
  resetToDefault, revokeToken, sessionIdFor, setPassword, tokenIsValid,
} from '../panel/auth.js';
import { defaultAdminDir, defaultDataDir } from '../api/client.js';
import { nodeCreate, setupMaster } from './create.js';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { flagBool, flagString, knowsFlag, type ParsedArgs } from '../args.js';


const DEFAULT_PORT = 8043;

const MIME: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
};









function selfPath(): string {
  return fileURLToPath(new URL('../bin/baton.js', import.meta.url));
}

function bundledPanel(): string | undefined {
  const dir = fileURLToPath(new URL('../../panel', import.meta.url));
  return existsSync(join(dir, 'index.html')) ? dir : undefined;
}








export function canBuildPanelHere(makefile = fileURLToPath(new URL('../../../../Makefile', import.meta.url))): boolean {
  try {
    return /^panel-bundle:/m.test(readFileSync(makefile, 'utf8'));
  } catch {
    return false;
  }
}










































const READ_VERBS: Record<string, string[] | null> = {
  status: null, events: null, sessions: null,



  agents: ['list', 'show'],







  inbox: ['list', 'policy'],
  version: null, doctor: null,


  kb: ['list', 'show'],
  node: ['list', 'show'],






  integrations: ['list'],



  template: ['list', 'show'],




  plugin: ['list'],


  agent: ['config', 'secret'],







  skills: ['list'],





  resource: ['list', 'get', 'discover', 'fetch', 'verify'],




  cloud: ['status', 'templates', 'skills', 'networks'],


  network: ['show', 'requests', 'resolve'],
  token: ['list'],
  capability: ['list'],
};














export function readAdmits(verb: string, sub: string): boolean {
  if (!Object.prototype.hasOwnProperty.call(READ_VERBS, verb)) return false;
  const subs = READ_VERBS[verb];
  if (!subs) return true;
  if (!subs.includes(sub)) return false;








  if (WRITE_SUBS[verb]?.has(sub)) return false;
  return true;
}











export const READ_DUAL: Record<string, Record<string, { third?: string; allows: Set<string> }>> = {
  agent: {

    config: { third: 'show', allows: new Set(['--output']) },


    secret: { third: 'list', allows: new Set(['--output']) },
  },
  resource: {


    fetch: { allows: new Set(['--from', '--output']) },
  },
  inbox: {


    policy: { allows: new Set(['--output']) },
  },
};






export function readAdmitsDual(verb: string, argv: string[]): boolean {
  const entry = READ_DUAL[verb]?.[argv[1] ?? ''];
  if (!entry) return false;
  if (!Object.prototype.hasOwnProperty.call(READ_VERBS, verb)) return false;
  const subs = READ_VERBS[verb];
  if (subs && !subs.includes(argv[1] ?? '')) return false;
  if (entry.third !== undefined && argv[2] !== entry.third) return false;
  const flags = argv.slice(1).filter((a) => a.startsWith('-'));
  return flags.every((f) => entry.allows.has(f));
}


export function dualSubs(): string[] {
  const out: string[] = [];
  for (const [verb, subs] of Object.entries(READ_VERBS)) {
    if (!subs) continue;
    for (const sub of subs) if (WRITE_SUBS[verb]?.has(sub)) out.push(`${verb} ${sub}`);
  }
  return out;
}


















export const READ_FLAGGED: Record<string, Record<string, { requires: string; allows: Set<string> }>> = {
  agent: {
    join: { requires: '--status', allows: new Set(['--status', '--name', '--output']) },
  },
};






export function readAdmitsFlagged(verb: string, argv: string[]): boolean {
  const entry = READ_FLAGGED[verb]?.[argv[1] ?? ''];
  if (!entry) return false;
  const flags = argv.slice(1).filter((a) => a.startsWith('-'));
  if (!flags.includes(entry.requires)) return false;
  return flags.every((f) => entry.allows.has(f));
}























export const BODY_TO_STDIN: [string, string][] = [
  ['cloud', 'phone-code'],
  ['cloud', 'phone-connect'],
  ['cloud', 'token-check'],
  ['agent', 'config'],
  ['agent', 'secret'],
  ['kb', 'set'],
  ['resource', 'publish'],
  ['inbox', 'attach'],
  ['integrations', 'add'],
];

export const WRITE_FLAGS: Record<string, Set<string>> = {

  cloud: new Set(['--output']),



  core: new Set(['--plan-digest', '--note']),



















  destroy: new Set(['--keep-data', '--yes']),
















  node: new Set(['--template', '--owner', '--yes', '--reason']),






  plugin: new Set(['--node']),




  network: new Set(['--agent', '--clear', '--stdin']),

  agents: new Set(['--label', '--clear']),















  send: new Set(['--stdin', '--reply-to', '--attach-ref']),











  inbox: new Set(['--name', '--act-on', '--allow', '--allow-network', '--remove', '--remove-network']),
  kb: new Set(['--title', '--tag', '--scope', '--content-type', '--author', '--stdin']),



  resource: new Set(['--type', '--name', '--version', '--folder', '--scope', '--content-type', '--summary', '--tag', '--source', '--hash', '--license', '--network', '--from', '--save', '--binding', '--credential', '--limit', '--with-avatars', '--stdin', '--reason']),
  snapshot: new Set<string>(),




  restore: new Set(['--from']),
  start: new Set<string>(),
  stop: new Set<string>(),
  restart: new Set<string>(),






  agent: new Set(['--name', '--template', '--node-id', '--no-network', '--stdin', '--network']),
  integrations: new Set(['--passkey-file', '--scopes', '--provider']),



  access: new Set(['--subject', '--key', '--scope', '--expiry']),
};

























export const CONFIRM: Set<string> = new Set([


  'resource admit',
  'resource deny',



  'core approve',
  'core reject',


















































  'destroy',


  'restore',
  'node revoke',

  'resource unpublish',





  'access received rm',









  'access grant issue',

  'integrations revoke',









  'agent secret',


]);






































































































export const CONFIRM_BY_RUNG: Set<string> = new Set(['core approve', 'core reject']);










export function rungWaivesPassword(rung: unknown): boolean {
  return rung === 'button';
}

export const CONFIRM_EXEMPT: Set<string> = new Set([











  'cloud phone-code',
  'cloud phone-connect',

  'cloud token-check',
  'node create',
  'node suspend',
  'node resume',
  'integrations add',
  'integrations enable',
  'integrations disable',
  'agent create',
  'agent config',
  'agent join',
  'agents set',
  'agents set-avatar',
  'snapshot',
  'start',
  'stop',
  'restart',
  'network add',
  'network deny',
  'network set-name',
  'network set-avatar',
  'network admission',
  'network visibility',
  'send',
  'kb set',
  'kb rm',
  'resource publish',
  'resource move',
  'resource tag',
  'resource fetch',
  'resource copy',
  'access grant revoke',
  'inbox open',
  'inbox delete',
  'inbox mark-all-read',
  'inbox attach',





  'inbox policy',








  'plugin install',
  'plugin remove',
]);









export function confirmationKey(verb: string, argv: string[]): string {
  return WRITE_SUBS[verb] ? `${verb} ${writeSubKey(verb, argv)}` : verb;
}










export function needsConfirmation(verb: string, argv: string[]): boolean {
  const key = confirmationKey(verb, argv);
  if (CONFIRM.has(key)) return true;
  if (CONFIRM_EXEMPT.has(key)) return false;
  return true;
}































export const HTTP_FOR_EXIT: Record<number, number> = {
  [ExitCode.CONFIG]:       400,
  [ExitCode.PRECONDITION]: 409,
  [ExitCode.UNREACHABLE]:  502,
  [ExitCode.AUTH]:         403,
  [ExitCode.CONFLICT]:     409,




  [ExitCode.PARTIAL]:      500,
  [ExitCode.UNSUPPORTED]:  501,
  [ExitCode.INTERNAL]:     500,
};

















export const HTTP_FOR_CODE: Record<string, number> = {
  NOT_FOUND:          404,
  ENDPOINT_MISSING:   404,
  METHOD_NOT_ALLOWED: 405,
};

export function httpStatusForCli(exitStatus: number | null, code?: string): number {

  if (code !== undefined && code in HTTP_FOR_CODE) return HTTP_FOR_CODE[code]!;








  if (exitStatus !== null && exitStatus in HTTP_FOR_EXIT) return HTTP_FOR_EXIT[exitStatus]!;
  return 500;
}








export function writeSubKey(verb: string, argv: string[]): string {
  const subs = WRITE_SUBS[verb];
  const threeLevel = subs ? [...subs].some((k) => k.includes(' ')) : false;
  return threeLevel ? argv.slice(1, 3).join(' ').trim() : (argv[1] ?? '');
}








export function writeAdmits(verb: string, argv: string[]): boolean {
  if (!WRITE_VERBS.has(verb)) return false;
  const subs = WRITE_SUBS[verb];
  if (subs && !subs.has(writeSubKey(verb, argv))) return false;
  const sub = argv[1] ?? '';
  if (NO_HOST_PATHS[verb]?.has(sub)) {


    const positionals = argv.slice(2).filter((a, i, all) => !a.startsWith('--') && !(i > 0 && all[i - 1]?.startsWith('--')));
    if (positionals.some(namesAHostPath)) return false;
  }
  return true;
}























































export const WRITE_VERBS = new Set(['destroy', 'node', 'integrations', 'agent', 'agents', 'snapshot', 'start', 'stop', 'restart', 'restore', 'network', 'send', 'kb', 'resource', 'access', 'inbox', 'plugin', 'cloud', 'core']);















export const WRITE_SUBS: Record<string, Set<string>> = {
  node: new Set(['create', 'revoke', 'suspend', 'resume']),




  core: new Set(['approve', 'reject']),










  network: new Set(['add', 'deny', 'set-name', 'set-avatar', 'admission', 'visibility']),
  integrations: new Set(['add', 'enable', 'disable', 'revoke']),











  access: new Set(['grant issue', 'grant revoke', 'received rm']),






  cloud: new Set(['phone-code', 'phone-connect', 'token-check']),












  agent: new Set(['create', 'config', 'secret', 'join']),

  agents: new Set(['set', 'set-avatar']),















  inbox: new Set(['open', 'delete', 'mark-all-read', 'attach', 'policy']),
  kb: new Set(['set', 'rm']),












  resource: new Set(['publish', 'unpublish', 'move', 'tag', 'fetch', 'copy', 'admit', 'deny']),







  plugin: new Set(['install', 'remove']),
};












export const NO_HOST_PATHS: Record<string, Set<string>> = {
  plugin: new Set(['install']),
};

function namesAHostPath(arg: string): boolean {
  return arg.includes('/') || arg.includes('\\') || arg.startsWith('~');
}


























export function flagsThePanelAdmitsThatBatonDoesNot(): string[] {
  const out: string[] = [];
  for (const [verb, flags] of Object.entries(WRITE_FLAGS)) {
    for (const f of flags) {
      if (!knowsFlag(f)) out.push(`${verb} ${f}`);
    }
  }
  return out.sort();
}

export function writeVerbNotReadAll(): string[] {
  const verbs = new Set([...WRITE_VERBS, ...Object.keys(WRITE_SUBS)]);
  return [...verbs].filter(
    (v) => Object.prototype.hasOwnProperty.call(READ_VERBS, v) && READ_VERBS[v] === null,
  );
}
















export function twoAssetSources(dist: string | undefined, upstream: string | undefined): string | undefined {
  if (dist && upstream) {
    return '--dist and --upstream both say where the panel page comes from, and this serves one page';
  }
  return undefined;
}

export async function panel(args: ParsedArgs): Promise<number> {













  if (args.positionals[1] === 'passwd') return panelPasswd(args);








  if (args.positionals[1] !== undefined) {
    throw usageError(
      `panel ${args.positionals[1]} is not a subcommand`,
      '`baton panel` opens the panel. Its one subcommand is `baton panel passwd`.',
    );
  }














  if (flagString(args, 'panel-port')) {
    throw usageError(
      '--panel-port is not the flag this command takes',
      'Here the port is `--port`: baton panel --port 8199. `--panel-port` belongs ' +
        'to `baton setup master`, which has a second port to name.',
    );
  }











const BOOTSTRAP_NODE = 'local';








function hasControlPlane(dataDir: string): boolean {
  const composeDir = join(dataDir, 'compose');
  if (!existsSync(composeDir)) return false;
  return readdirSync(composeDir).some((f) => f.startsWith('master-') && f.endsWith('.yml'));
}









async function bootstrapControlPlane(args: ParsedArgs): Promise<ParsedArgs> {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const registerDir = join(dataDir, 'nodes');
  const offices = existsSync(registerDir)
    ? readdirSync(registerDir).filter((f) => f.endsWith('.json')).map((f) => f.replace(/\.json$/, ''))
    : [];




  if (offices.length > 1) {
    throw preconditionError(
      'this machine has more than one node and no control plane, so there is nothing to infer',
      `Say which one holds it: baton setup master <${offices.join('|')}>, then run baton panel again.`,
    );
  }
  const name = offices[0] ?? BOOTSTRAP_NODE;











  const inherited = new Map(args.flags);
  inherited.delete('port');
  const cpPort = flagString(args, 'control-plane-port');
  if (cpPort) inherited.set('port', cpPort);
  const forward = (positionals: string[]): ParsedArgs =>
    ({ ...args, positionals, flags: inherited });

  process.stdout.write(
    '\n  This machine has no control plane yet. Building one before serving the panel:\n\n',
  );
  if (offices.length === 0) {




    process.stdout.write(`    node    ${name}    (new)\n`);
    const code = await nodeCreate(forward(['node', 'create', name]));
    if (code !== ExitCode.OK) throw preconditionError('could not create this machine\'s node', 'See the error above.');
  } else {
    process.stdout.write(`    node    ${name}    (already here)\n`);
  }
  process.stdout.write('    network local-master    (founded here, and it can join others later)\n\n');
  const code = await setupMaster(forward(['setup', 'master', name]));
  if (code !== ExitCode.OK) {
    throw preconditionError(
      'could not bring up this machine\'s control plane',
      'The error above says why. If the port is already taken — something else on 8443 — ' +
        'name another one: baton panel --control-plane-port 8643. ' +
        'The control plane is what nodes dial, so its port is chosen rather than picked at random.',
    );
  }















  return { ...args, global: { ...args.global, master: `https://127.0.0.1:${cpPort ?? '8443'}` } };
}

  const port = Number.parseInt(flagString(args, 'port') ?? String(DEFAULT_PORT), 10);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    throw preconditionError(`--port ${flagString(args, 'port')} is not a port`, 'Use 1–65535.');
  }






  const remoteFaceFlag = flagString(args, 'remote-face-port');
  const remoteFacePort = remoteFaceFlag === undefined ? undefined : Number.parseInt(remoteFaceFlag, 10);
  if (remoteFacePort !== undefined &&
      (!Number.isInteger(remoteFacePort) || remoteFacePort < 1 || remoteFacePort > 65535 || remoteFacePort === port)) {
    throw preconditionError(`--remote-face-port ${remoteFaceFlag} is not a usable port`,
      'Use 1–65535, and not the same port as --port.');
  }

  const adminDir = args.global.adminDir ?? defaultAdminDir(args.global.dataDir);


  const panelDataDir = args.global.dataDir ?? defaultDataDir();
  const cert = join(adminDir, 'admin.crt');
  const key = join(adminDir, 'admin.key');
  const ca = join(adminDir, 'ca.crt');
  if (![cert, key, ca].every((f) => existsSync(f))) {





















    if (args.flags.has('master')) {
      throw preconditionError(
        `--master names ${args.global.master}, but there are no operator credentials at ${adminDir}`,
        'Point --admin-dir at the credential set that control plane issued. ' +
          'Drop both flags to build and serve this machine\'s own.',
      );
    }
    if (hasControlPlane(args.global.dataDir ?? defaultDataDir())) {
      throw preconditionError(
        `operator credentials not found at ${adminDir}`,
        'This machine HAS a control plane, so this is a wrong turn rather than a missing one: ' +
          'point --admin-dir at the credential set it issued, or drop the flag for the default.',
      );
    }
    args = await bootstrapControlPlane(args);
  }

  const master = new URL(args.global.master);







  const upstream = flagString(args, 'upstream');
  const explicitDist = flagString(args, 'dist');
  const clash = twoAssetSources(explicitDist, upstream);
  if (clash) throw preconditionError(clash, 'Pass one of them, not both.');
  if (explicitDist && !existsSync(explicitDist)) {
    throw preconditionError(`--dist ${explicitDist} does not exist`, 'Point it at the panel build output.');
  }
  const dist = explicitDist ?? (upstream ? undefined : bundledPanel());
  if (!dist && !upstream) {
    throw preconditionError(
      'this installation has no panel in it',
      canBuildPanelHere()
        ? 'Build one with `make panel-bundle`, or point at one with --dist / --upstream.'
        : 'This release of baton ships without the panel. Point at a panel build with --dist <dir>, ' +
          'or at a running panel server with --upstream <url>.',
    );
  }

  const tls = {
    cert: readFileSync(cert),
    key: readFileSync(key),
    ca: readFileSync(ca),
  };





  const handle = async (req: IncomingMessage, res: ServerResponse, remote: boolean): Promise<void> => {
    const url = req.url ?? '/';
    if (!admissible(req, res, port)) return;




    if (remote && passwordIsDefault(panelDataDir)) {
      refuse(res, 403, 'PANEL_PASSWORD_DEFAULT',
        'this panel still has the password it shipped with, so its remote face stays shut',
        'On the workspace, change it with `baton panel passwd`; the remote face opens by itself.');
      return;
    }

    const authPath = url.split('?')[0] ?? '';








    if (authPath === '/auth/login') {
      if (req.method !== 'POST') {
        refuse(res, 405, 'METHOD_NOT_ALLOWED', `${req.method} is not used here`, 'Log in with POST.');
        return;
      }
      let raw = '';
      req.on('data', (c) => { raw += c; if (raw.length > 4096) req.destroy(); });
      req.on('end', () => {
        let body: { username?: string; password?: string } = {};
        try { body = JSON.parse(raw || '{}'); } catch {  }
        if (!checkCredentials(panelDataDir, body.username ?? '', body.password ?? '')) {




          refuse(res, 401, 'INVALID_CREDENTIALS', 'that username and password were not accepted',
            'The shipped default is admin/admin until it is changed with `baton panel passwd`.');
          return;
        }
        const { token, expiresAt } = issueToken();
        res.writeHead(200, { 'content-type': 'application/json; charset=utf-8' });
        res.end(JSON.stringify({
          access_token: token,
          token_type: 'Bearer',
          expires_at: expiresAt,
          identity: 'admin',



          password_is_default: passwordIsDefault(panelDataDir),
        }));
      });
      return;
    }

    if (authPath === '/auth/logout') {
      revokeToken(bearerFrom(req.headers.authorization));
      res.writeHead(204).end();
      return;
    }



















    if (authPath === '/auth/passwd') {



      if (remote) {
        refuse(res, 403, 'REMOTE_READ_ONLY', 'the remote face does not change the panel password',
          'Change it on the workspace itself with `baton panel passwd` or the local panel.');
        return;
      }
      if (req.method !== 'POST') {
        refuse(res, 405, 'METHOD_NOT_ALLOWED', `${req.method} is not used here`, 'Change it with POST.');
        return;
      }
      if (!tokenIsValid(bearerFrom(req.headers.authorization))) {
        refuse(res, 401, 'UNAUTHENTICATED', 'this request carried no valid session',
          'Log in at POST /auth/login.');
        return;
      }
      let raw = '';
      req.on('data', (c) => { raw += c; if (raw.length > 4096) req.destroy(); });
      req.on('end', () => {
        let body: { current_password?: string; new_password?: string } = {};
        try { body = JSON.parse(raw || '{}'); } catch {  }
        if (!checkCredentials(panelDataDir, 'admin', body.current_password ?? '')) {
          refuse(res, 401, 'INVALID_CREDENTIALS', 'the current password was not accepted',
            'A session alone is not enough to replace the credential it was issued against.');
          return;
        }
        const next = body.new_password ?? '';
        if (next.length < 8) {
          refuse(res, 400, 'PASSWORD_TOO_SHORT', 'a panel password is at least 8 characters',
            'This process holds an operator certificate; the gate in front of it should not be guessable.');
          return;
        }
        if (next === AUTH_DEFAULTS.password) {
          refuse(res, 400, 'PASSWORD_UNCHANGED', 'that is the password it already ships with',
            'Changing it to the shipped default would leave the warning on and the credential unchanged.');
          return;
        }
        setPassword(panelDataDir, next);
        revokeAllTokens();
        res.writeHead(204).end();
      });
      return;
    }

    if (authPath === '/auth/me') {
      if (!tokenIsValid(bearerFrom(req.headers.authorization))) {
        refuse(res, 401, 'UNAUTHENTICATED', 'this request carried no valid session',
          'Log in at POST /auth/login.');
        return;
      }
      res.writeHead(200, { 'content-type': 'application/json; charset=utf-8' });







      res.end(JSON.stringify({
        identity: 'admin',
        password_is_default: passwordIsDefault(panelDataDir),
        control_plane_url: master.origin,
        cloud: cloudBlock(master, adminDir),
      }));
      return;
    }












    if (authPath.startsWith('/local/')) {
      refuse(res, 410, 'ENDPOINT_REMOVED', `${authPath} no longer exists`,
        'The panel reads through `/run?cmd=<verb>`. These paths were removed once ' +
          'nothing used them (rule 1: deletion is complete).');
      return;
    }

    if (authPath === '/run' &&
        !tokenIsValid(bearerFrom(req.headers.authorization))) {
      refuse(res, 401, 'UNAUTHENTICATED', 'this request carried no valid session',
        'Log in at POST /auth/login and send `Authorization: Bearer <token>`.');
      return;
    }




































    const localPath = url.split('?')[0] ?? '';















    if (localPath === '/run') {
      const isWrite = !READ_METHODS.has(req.method ?? '');




      if (remote && isWrite) {
        refuse(res, 403, 'REMOTE_READ_ONLY', 'the remote face runs readers only',
          'Writes go through `baton` with your operator certificate, which records who did it.');
        return;
      }






      const actingFor = isWrite
        ? 'panel-session:' + (sessionIdFor(bearerFrom(req.headers.authorization)) ?? 'unknown')
        : undefined;
      if (isWrite && req.method !== 'POST') {
        refuse(res, 405, 'METHOD_NOT_ALLOWED', `${req.method} is not used here`,
          'Reads are GET. The one write is POST.');
        return;
      }
      const cmd = (new URL(req.url ?? '/', 'http://x').searchParams.get('cmd') ?? '').trim();






      let argv: string[];
      try {
        argv = tokenizeCmd(cmd);
      } catch {
        refuse(res, 400, 'BAD_COMMAND', 'the command has an unbalanced quote', 'Close every " and \' in the cmd.');
        return;
      }
      const verb = argv[0] ?? '';
      const admitted = readAdmits(verb, argv[1] ?? '') || readAdmitsFlagged(verb, argv) || readAdmitsDual(verb, argv);
      if (!cmd) {
        refuse(res, 400, 'NO_COMMAND', 'give a command: /run?cmd=node+list',
          `Readable: ${Object.keys(READ_VERBS).join(', ')}.`);
        return;
      }
      if (isWrite) {
        const subs = WRITE_SUBS[verb];
        const wsub = writeSubKey(verb, argv);
        if (subs && !subs.has(wsub)) {
          refuse(res, 403, 'NOT_WRITABLE',
            `\`baton ${verb} ${wsub}\` is not one of the writes this panel performs`,
            `Of \`${verb}\` it performs: ${[...subs].join(', ')}. ` +
              'The rest go through `baton` with your own certificate.');
          return;
        }
        if (!WRITE_VERBS.has(verb)) {
          refuse(res, 403, 'NOT_WRITABLE',
            `\`baton ${cmd}\` is not one of the writes this panel performs`,
            `It performs: ${[...WRITE_VERBS].join(', ')}. Everything else that changes the ` +
              'cluster goes through `baton` with your own certificate.');
          return;
        }






        if (needsConfirmation(verb, argv) &&
            !(CONFIRM_BY_RUNG.has(confirmationKey(verb, argv)) &&
              rungWaivesPassword(await requiredRungOf(master, tls, argv[2] ?? '')))) {
          const confirm = req.headers['x-baton-confirm'];
          const pw = Array.isArray(confirm) ? '' : (confirm ?? '');
          if (!checkCredentials(panelDataDir, 'admin', pw)) {
            refuse(res, 401, 'CONFIRMATION_REQUIRED',
              `\`baton ${verb} ${writeSubKey(verb, argv)}\` asks for your password again`,
              'Send it as the `X-Baton-Confirm` header. Being logged in says somebody logged in ' +
                'earlier; this asks whether the person deciding is here now.');
            return;
          }
        }




        if (verb === 'core' && passwordIsDefault(panelDataDir)) {
          refuse(res, 403, 'PANEL_PASSWORD_DEFAULT',
            'this panel still has the password it shipped with, so it does not take decisions',
            'Change it with `baton panel passwd`, then decide again.');
          return;
        }
        const allowed = WRITE_FLAGS[verb] ?? new Set<string>();
        const rejected = argv.slice(1).filter((a) => a.startsWith('-') && !allowed.has(a));
        if (rejected.length) {
          refuse(res, 403, 'FLAG_NOT_ALLOWED',
            `\`baton ${verb}\` does not take ${rejected[0]} through this panel`,
            `It accepts: ${[...allowed].join(', ') || '(no flags)'}. The rest go through ` +
              '`baton` with your own certificate.');
          return;
        }
        if (verb === 'restore') {




          const snapDir = resolvePath(join(args.global.dataDir ?? defaultDataDir(), 'snapshots'));
          const i = argv.indexOf('--from');
          const given = i >= 0 ? argv[i + 1] : undefined;

          let real = '';
          try { real = given ? realpathSync(resolvePath(given)) : ''; } catch { real = ''; }
          if (!given || !real || !(real.startsWith(snapDir + '/'))) {
            refuse(res, 403, 'PATH_NOT_ALLOWED',
              `restore through this panel reads only files under ${snapDir}`,
              'That is where `baton snapshot` writes. A snapshot elsewhere is restored with `baton restore` ' +
                'and your own certificate.');
            return;
          }
          argv[i + 1] = real;
        }






        const wantsStdin = (verb === 'cloud' && (argv[1] === 'phone-code' || argv[1] === 'phone-connect' || argv[1] === 'token-check')) || (verb === 'agent' && argv[1] === 'config' && argv[2] === 'set') || (verb === 'agent' && argv[1] === 'secret' && argv[2] === 'set') || verb === 'send' || (verb === 'kb' && argv[1] === 'set') || (verb === 'resource' && argv[1] === 'publish') || (verb === 'network' && argv[1] === 'set-avatar' && !argv.includes('--clear')) || (verb === 'agents' && argv[1] === 'set-avatar' && !argv.includes('--clear')) || (verb === 'inbox' && argv[1] === 'attach') || (verb === 'integrations' && argv[1] === 'add');


        if (verb === 'send' && !argv.includes('--stdin')) argv.push('--stdin');


        if (verb === 'integrations' && argv[1] === 'add' && !argv.includes('--stdin')) argv.push('--stdin');
        if (!wantsStdin) argv.push('--yes');
        else {





          const chunks: Buffer[] = [];
          let size = 0;
          req.on('data', (c: Buffer) => { size += c.length; if (size > 3 * 1024 * 1024) { req.destroy(); return; } chunks.push(c); });
          req.on('end', () => { runCli(res, argv, master, adminDir, panelDataDir, cmd, Buffer.concat(chunks), actingFor); });
          req.on('error', () => refuse(res, 400, 'BAD_BODY', 'could not read the request body', 'Send the content as the POST body.'));
          return;
        }
      } else if (!admitted) {
        refuse(res, 403, 'NOT_READ_ONLY',
          `\`baton ${cmd}\` is not one of the read-only commands this panel runs`,
          'This process holds your operator certificate, so it runs readers only. ' +
            `Readable: ${Object.keys(READ_VERBS).join(', ')}. ` +
            'Anything that changes the cluster goes through `baton` with your own ' +
            'certificate, where `console` records who did it.');
        return;
      }
      runCli(res, argv, master, adminDir, panelDataDir, cmd, undefined, actingFor);
      return;
    }























    const probe = ptyProbeTarget(req.method ?? '', url);
    if (probe !== undefined) {
      proxyToControlPlane(req, res, master, tls, probe);
      return;
    }

    if (url.startsWith('/api/')) {
      if (!READ_METHODS.has(req.method ?? '')) {




        refuse(res, 403, 'READ_ONLY', `the panel proxy does not forward ${req.method} requests`,
          'Writes go through `baton` with your operator certificate, which records who did it.');
        return;
      }
      proxyToControlPlane(req, res, master, tls);
    } else if (upstream) {
      proxyToUpstream(req, res, new URL(upstream));
    } else {
      serveStatic(req, res, dist!);
    }
  };

  const server = createServer((req, res) => { handle(req, res, false).catch(() => { if (!res.headersSent) refuse(res, 500, 'PANEL_INTERNAL', 'the panel failed while handling this request', 'Retry; if it repeats, restart `baton panel`.'); }); });





  server.on('upgrade', (req, socket, head) => {
    void head;
    const path = (req.url ?? '/').split('?')[0];
    if (path === '/pty') {
      handlePtyUpgrade(req, socket, { master, adminDir, isValid: tokenIsValid });
      return;
    }
    socket.destroy();
  });

  server.on('error', (err: NodeJS.ErrnoException) => {
    if (err.code === 'EADDRINUSE') {
      process.stderr.write(
        `error: port ${port} is already in use\n  Pick another with --port, or stop what is holding it.\n`,
      );
      process.exit(ExitCode.PRECONDITION);
    }
    process.stderr.write(`error: ${err.message}\n`);
    process.exit(ExitCode.INTERNAL);
  });




  const remoteFace = remoteFacePort === undefined
    ? undefined
    : createServer((req, res) => { handle(req, res, true).catch(() => { if (!res.headersSent) refuse(res, 500, 'PANEL_INTERNAL', 'the panel failed while handling this request', 'Retry; if it repeats, restart `baton panel`.'); }); });
  remoteFace?.on('upgrade', (_req, socket) => { socket.destroy(); });
  remoteFace?.on('error', (err: NodeJS.ErrnoException) => {
    process.stderr.write(`error: remote face: ${err.message}\n`);
    process.exit(err.code === 'EADDRINUSE' ? ExitCode.PRECONDITION : ExitCode.INTERNAL);
  });

  return new Promise((resolve) => {
    if (remoteFace && remoteFacePort !== undefined) {
      remoteFace.listen(remoteFacePort, '127.0.0.1', () => {
        process.stdout.write(
          `  remote     http://127.0.0.1:${remoteFacePort} — read-only face for the entry connector\n`,
        );
      });
    }


    server.listen(port, '127.0.0.1', () => {
      process.stdout.write(
        `\n  panel      http://127.0.0.1:${port}\n` +
          `  API        proxied to ${master.origin} with your operator certificate, read only\n` +
          `  assets     ${upstream ? upstream : dist === bundledPanel() ? 'bundled with this installation' : dist}\n\n` +
          '  Loopback only. This process holds your operator certificate, so it\n' +
          `  forwards reads and refuses writes. To reach it from another server,\n` +
          `  forward the port: ssh -L ${port}:127.0.0.1:${port} <this-host>\n\n` +
          '  Ctrl-C to stop.\n',
      );


      process.on('SIGINT', () => {
        remoteFace?.close();
        server.close(() => resolve(ExitCode.OK));
      });
    });
  });
}





const READ_METHODS = new Set(['GET', 'HEAD', 'OPTIONS']);









export function ptyProbeTarget(method: string, url: string): string | undefined {
  if (method !== 'GET') return undefined;
  if (url.split('?')[0] !== '/pty') return undefined;
  return API_BASE + url;
}


const LOOPBACK_NAMES = new Set(['127.0.0.1', 'localhost', '[::1]', '::1']);








async function requiredRungOf(
  master: URL,
  tls: { cert: Buffer; key: Buffer; ca: Buffer },
  txId: string,
): Promise<unknown> {
  if (!/^tx_[A-Za-z0-9-]+$/.test(txId)) return undefined;
  return new Promise((resolve) => {
    const req = httpsRequest(
      {
        hostname: master.hostname,
        port: master.port || 443,
        method: 'GET',
        path: `/api/v1alpha1/core/transactions/${encodeURIComponent(txId)}`,
        cert: tls.cert, key: tls.key, ca: tls.ca,
        timeout: 5000,
      },
      (r) => {
        let body = '';
        r.setEncoding('utf8');
        r.on('data', (c: string) => { body += c; });
        r.on('end', () => {
          if (r.statusCode !== 200) return resolve(undefined);
          try {
            const o = JSON.parse(body) as { approval?: { required_rung?: unknown } | null };
            resolve(o.approval ? o.approval.required_rung : undefined);
          } catch {
            resolve(undefined);
          }
        });
      },
    );
    req.on('timeout', () => { req.destroy(); resolve(undefined); });
    req.on('error', () => resolve(undefined));
    req.end();
  });
}

function refuse(res: ServerResponse, status: number, code: string, message: string, remediation: string): void {
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8' });
  res.end(JSON.stringify({ code, message, remediation }));
}
















export function admissible(req: IncomingMessage, res: ServerResponse, port: number): boolean {
  const host = req.headers.host ?? '';

  const name = host.startsWith('[') ? host.slice(0, host.indexOf(']') + 1) : (host.split(':')[0] ?? '');














  if (!LOOPBACK_NAMES.has(name)) {
    refuse(res, 403, 'BAD_HOST',
      `this port answers to a loopback name only, and the request said Host: ${host || '(absent)'}`,
      `Open it as http://127.0.0.1:${port}/, or forward it over SSH — any local port will do.`);
    return false;
  }

  const origin = req.headers.origin;
  if (typeof origin === 'string' && origin !== `http://${host}`) {
    refuse(res, 403, 'CROSS_ORIGIN',
      `a page at ${origin} tried to use this port, and this port acts as the operator`,
      'Nothing needs to do this. If a tool of yours does, it should hold its own certificate.');
    return false;
  }
  return true;
}







function proxyToControlPlane(
  req: IncomingMessage,
  res: ServerResponse,
  master: URL,
  tls: { cert: Buffer; key: Buffer; ca: Buffer },



  pathOverride?: string,
): void {
  const upstream = httpsRequest(
    {
      hostname: master.hostname,
      port: master.port || 443,
      path: pathOverride ?? req.url,
      method: req.method,
      headers: { ...req.headers, host: master.host },
      cert: tls.cert,
      key: tls.key,
      ca: tls.ca,


      rejectUnauthorized: true,
    },
    (up) => {
      res.writeHead(up.statusCode ?? 502, up.headers);
      up.pipe(res);
    },
  );
  upstream.on('error', (err) => {
    res.writeHead(502, { 'content-type': 'application/json' });
    res.end(
      JSON.stringify({
        code: 'UNREACHABLE',
        message: `the control plane did not answer: ${err.message}`,
        remediation: `Check that it is running at ${master.origin}.`,
      }),
    );
  });
  req.pipe(upstream);
}


function proxyToUpstream(req: IncomingMessage, res: ServerResponse, upstream: URL): void {
  const up = httpRequest(
    {
      hostname: upstream.hostname,
      port: upstream.port || 80,
      path: req.url,
      method: req.method,
      headers: { ...req.headers, host: upstream.host },
    },
    (r) => {
      res.writeHead(r.statusCode ?? 502, r.headers);
      r.pipe(res);
    },
  );
  up.on('error', (err) => {
    res.writeHead(502, { 'content-type': 'text/plain; charset=utf-8' });
    res.end(`the panel upstream did not answer: ${err.message}\n`);
  });
  req.pipe(up);
}








function serveStatic(req: IncomingMessage, res: ServerResponse, dist: string): void {
  const raw = (req.url ?? '/').split('?')[0] ?? '/';



  const rel = normalize(decodeURIComponent(raw)).replace(/^(\.\.[/\\])+/, '');
  let file = join(dist, rel);
  if (!file.startsWith(normalize(dist))) {
    res.writeHead(403).end('forbidden\n');
    return;
  }
  if (existsSync(file) && statSync(file).isDirectory()) file = join(file, 'index.html');
  if (!existsSync(file)) {






    if (extname(rel) !== '') {
      res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' });
      res.end(`${rel} is not in the panel build\n`);
      return;
    }
    file = join(dist, 'index.html');
  }
  if (!existsSync(file)) {
    res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' });
    res.end('the panel build output has no index.html\n');
    return;
  }
  res.writeHead(200, { 'content-type': MIME[extname(file)] ?? 'application/octet-stream' });
  res.end(readFileSync(file));
}




















export function tokenizeCmd(cmd: string): string[] {
  const out: string[] = [];
  let cur = '';
  let has = false;
  let mode: 'none' | 'single' | 'double' = 'none';
  for (let i = 0; i < cmd.length; i++) {
    const c = cmd[i]!;
    if (mode === 'single') {
      if (c === "'") mode = 'none';
      else { cur += c; }
      has = true;
    } else if (mode === 'double') {
      if (c === '"') mode = 'none';
      else if (c === '\\' && i + 1 < cmd.length && (cmd[i + 1] === '"' || cmd[i + 1] === '\\')) { cur += cmd[++i]; }
      else { cur += c; }
      has = true;
    } else if (c === "'") { mode = 'single'; has = true; }
    else if (c === '"') { mode = 'double'; has = true; }
    else if (c === '\\' && i + 1 < cmd.length) { cur += cmd[++i]; has = true; }
    else if (/\s/.test(c)) { if (has) { out.push(cur); cur = ''; has = false; } }
    else { cur += c; has = true; }
  }
  if (mode !== 'none') throw new Error('unbalanced quote');
  if (has) out.push(cur);
  return out;
}


















export interface CloudBlock { bound: string; named: string; address_published: string; reachable: string; address: string | null; origin: string }












export function cloudBlockFrom(j: Partial<CloudStatusJSON>, fallbackOrigin: string): CloudBlock {
  const val = (v?: string): string => (v === 'yes' || v === 'no' ? v : 'unknown');
  return {
    bound: val(j.bound),
    named: val(j.named),
    address_published: val(j.address_published),



    reachable: val(j.address_published),
    address: j.address ?? null,
    origin: j.endpoint ?? fallbackOrigin,
  };
}

function cloudBlock(master: URL, adminDir: string): CloudBlock {
  const fallbackOrigin = (process.env.BATON_CLOUD_URL ?? 'https://api.batoncloud.org').replace(/\/+$/, '');
  try {
    const out = spawnSync(
      process.execPath,
      [selfPath(), 'cloud', 'status', '--output', 'json', '--master', master.href, '--admin-dir', adminDir],
      { encoding: 'utf8', timeout: 8000 },
    );






    return cloudBlockFrom(JSON.parse(out.stdout || '{}') as Partial<CloudStatusJSON>, fallbackOrigin);
  } catch {









    return cloudBlockFrom({}, fallbackOrigin);
  }
}

function runCli(res: ServerResponse, argv: string[], master: URL, adminDir: string, dataDir: string, cmd: string, input?: string | Buffer, actingFor?: string): void {
      const out = spawnSync(
    process.execPath,










    [selfPath(), ...argv, '--output', 'json', '--master', master.href,
      '--admin-dir', adminDir, '--data-dir', dataDir],



    { encoding: 'utf8', input, env: actingFor ? { ...process.env, BATON_ACTING_FOR: actingFor } : process.env },
  );
      if (!out.stdout) {














        const err = (out.stderr || '').trim();
        if (err.startsWith('{')) {
          try {
            const parsed = JSON.parse(err) as { code?: string };
            res.writeHead(httpStatusForCli(out.status, parsed.code),
              { 'content-type': 'application/json; charset=utf-8' });
            res.end(err);
            return;
          } catch {


          }
        }
        refuse(res, 502, 'COMMAND_FAILED', `\`baton ${cmd}\` produced no output`,
          err.split('\n')[0] || 'Run it here and see what it says.');
        return;
      }
      res.writeHead(out.status === 0 ? 200 : 502, { 'content-type': 'application/json; charset=utf-8' });
      res.end(out.stdout);
      return;
}

async function panelPasswd(args: ParsedArgs): Promise<number> {



  const dataDir = args.global.dataDir ?? defaultDataDir();



















  if (flagBool(args, 'reset-default')) {




    if (!flagBool(args, 'yes')) {
      if (!process.stdin.isTTY) {
        throw preconditionError(
          'resetting to the shipped password needs confirmation',
          'Re-run with --yes. It sets the password back to the value this ' +
            'package ships with, which is public.',
        );
      }
      process.stdout.write(
        `This sets the panel password back to ${AUTH_DEFAULTS.user}/${AUTH_DEFAULTS.password}, ` +
          'which is public.\n',
      );
      const answer = await readLine('Type yes to continue: ');
      if (answer.trim() !== 'yes') {
        throw preconditionError('not confirmed', 'Nothing was changed.');
      }
    }

    resetToDefault(dataDir);
    process.stdout.write(
      `panel password reset to ${AUTH_DEFAULTS.user}/${AUTH_DEFAULTS.password}.\n` +
        '  The "still using the shipped password" warning is lit again — that is this ' +
        'working, not a new problem.\n' +
        '  Sessions already issued stay valid until they expire; restart `baton panel` to end them now.\n',
    );
    return ExitCode.OK;
  }

  const fromFile = flagString(args, 'from-file');

  let next: string;
  if (fromFile) {
    next = readFileSync(fromFile, 'utf8').replace(/\r?\n$/, '');
  } else {
    if (!process.stdin.isTTY) {
      throw preconditionError(
        'a password has to be typed, or read from a file',
        'Run this in a terminal, or use `baton panel passwd --from-file <path>`.',
      );
    }
    next = await readSecret('New panel password: ');
    const again = await readSecret('Repeat it: ');
    if (next !== again) {



      throw preconditionError('those did not match', 'Nothing was changed. Run it again.');
    }
  }

  if (next.length < 8) {
    throw preconditionError(
      'that password is shorter than 8 characters',
      'The panel holds an operator certificate; the gate in front of it should not be guessable.',
    );
  }
  if (next === AUTH_DEFAULTS.password) {
    throw preconditionError(
      'that is the shipped default',
      'Changing it to the value it already ships with would leave the warning on and the ' +
        'credential unchanged.',
    );
  }

  setPassword(dataDir, next);
  process.stdout.write(
    'panel password changed.\n' +
      '  Sessions already issued stay valid until they expire; restart `baton panel` to end them now.\n',
  );
  return ExitCode.OK;
}









function readLine(prompt: string): Promise<string> {
  return new Promise((resolve) => {
    process.stdout.write(prompt);
    const stdin = process.stdin;
    stdin.resume();
    stdin.setEncoding('utf8');
    let buf = '';
    const onData = (ch: string) => {
      for (const c of ch) {
        if (c === '\n' || c === '\r') {
          stdin.removeListener('data', onData);
          stdin.pause();
          process.stdout.write('\n');
          resolve(buf);
          return;
        }
        buf += c;
      }
    };
    stdin.on('data', onData);
  });
}


function readSecret(prompt: string): Promise<string> {
  return new Promise((resolve, reject) => {
    process.stdout.write(prompt);
    const stdin = process.stdin;
    const wasRaw = stdin.isRaw ?? false;
    stdin.setRawMode?.(true);
    stdin.resume();
    stdin.setEncoding('utf8');
    let buf = '';
    const onData = (ch: string) => {
      for (const c of ch) {
        if (c === '\n' || c === '\r' || c === '\u0004') {
          stdin.removeListener('data', onData);
          stdin.setRawMode?.(wasRaw);
          stdin.pause();
          process.stdout.write('\n');
          resolve(buf);
          return;
        }
        if (c === '\u0003') {
          stdin.removeListener('data', onData);
          stdin.setRawMode?.(wasRaw);
          stdin.pause();
          process.stdout.write('\n');
          reject(new Error('cancelled'));
          return;
        }

        if (c === '\u007f' || c === '\b') buf = buf.slice(0, -1);
        else buf += c;
      }
    };
    stdin.on('data', onData);
  });
}
