// SPDX-License-Identifier: Apache-2.0















import { spawn } from 'node:child_process';
import { platform } from 'node:os';
import { Client } from '../api/client.js';
import { ExitCode } from '../errors.js';
import { json } from '../output.js';
import { flagBool, type ParsedArgs } from '../args.js';

interface Handoff {
  handoff_token: string;
  expires_in: number;
  url: string;
}

export async function web(args: ParsedArgs, client: Client): Promise<number> {


  const handoff = await client.post<Handoff>('/web/handoff');




  const url = `${args.global.master.replace(/\/+$/, '')}/web/session?token=${handoff.handoff_token}`;

  if (args.global.output === 'json') {
    process.stdout.write(json({ url, expires_in: handoff.expires_in }) + '\n');
    return ExitCode.OK;
  }

  process.stdout.write(
    `${url}\n\n` +
      `  This link works once and expires in ${handoff.expires_in}s.\n` +
      '  The session it creates can read; every change still goes through the CLI.\n' +
      '  The console is reachable wherever this port is published — the generated\n' +
      '  manifests bind it to 127.0.0.1, and that binding is what keeps it local.\n',
  );

  if (flagBool(args, 'no-open')) return ExitCode.OK;

  const opener =
    platform() === 'darwin' ? 'open' : platform() === 'win32' ? 'start' : 'xdg-open';

  const child = spawn(opener, [url], { stdio: 'ignore', detached: true });
  child.on('error', () => {


    process.stderr.write(`\n  (could not launch a browser; open the link above)\n`);
  });
  child.unref();

  return ExitCode.OK;
}
