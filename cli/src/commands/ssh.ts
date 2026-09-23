// SPDX-License-Identifier: Apache-2.0


























import { BatonError, ExitCode } from '../errors.js';
import { json } from '../output.js';
import type { Client } from '../api/client.js';
import { execInteractive, execPlan, execSucceeds } from '../runtime/engine.js';
import { resolveTarget, type ComponentRole } from '../runtime/target.js';
import { flagString, type ParsedArgs } from '../args.js';


function networkFromSAN(sanURI: string | undefined): string | undefined {

  const m = /^baton:\/\/([^/]+)\/node\//.exec(sanURI ?? '');
  return m?.[1];
}

interface NodeDetail {
  display_name?: string;
  status?: string;
  certificate?: { san_uri?: string };
}

export async function ssh(args: ParsedArgs, client: Client): Promise<number> {
  const asked = args.positionals[1];
  if (!asked) {
    throw new BatonError({
      code: 'INVALID_ARGUMENT',
      message: 'ssh needs an agent to open',
      remediation: 'Try `baton ssh coder`, or `baton ssh coder@default` to be explicit.',
      exitCode: ExitCode.CONFIG,
    });
  }




  const at = asked.indexOf('@');


  const lead = asked.startsWith('@');
  const name = lead ? asked : at === -1 ? asked : asked.slice(0, at);
  const askedNetwork = lead || at === -1 ? undefined : asked.slice(at + 1);





  const resolved = resolveTarget(name, { dataDir: args.global.dataDir }).node;
  const detail = await client.get<{ node?: NodeDetail } & NodeDetail>(`/nodes/${resolved}`);
  const node = detail.node ?? detail;
  const network = networkFromSAN(node.certificate?.san_uri);

  if (!network) {
    throw new BatonError({
      code: 'NO_ADDRESS',
      message: `${name} has no certificate, so it has no address to reach it by`,
      remediation:
        'A node gets its address when it enrolls. If this one is still pending, ' +
        'give it a token: `baton agent create --name <n> --enrollment-token-file <f>`.',
      exitCode: ExitCode.PRECONDITION,
    });
  }



  if (askedNetwork !== undefined && askedNetwork !== network) {
    throw new BatonError({
      code: 'WRONG_NETWORK',
      message: `${asked} does not resolve: ${name} is on ${network}`,
      remediation:
        `Use ${name}@${network}, or omit the network to use the one you are pointed at. ` +
        'Reaching another network is federation, which is not in this version.',
      exitCode: ExitCode.CONFIG,
    });
  }

  const address = `${resolved}@${network}`;
  const target = resolveTarget(resolved, {
    role: flagString(args, 'role') as ComponentRole | undefined,
    component: flagString(args, 'component'),
  });










  const shellPath = execSucceeds(target.container, ['test', '-x', '/bin/bash'])
    ? '/bin/bash'
    : '/bin/sh';
  const command = [shellPath, '-l'];




  const tty = process.stdin.isTTY === true;

  if (args.global.dryRun) {
    process.stdout.write(
      args.global.output === 'json'
        ? json({ address, node: name, network, command: execPlan(target.container, command, { tty, stdin: true }) }) + '\n'
        : `address   ${address}\n` +
            `node      ${name}\n` +
            `network   ${network}\n` +
            `runs      ${execPlan(target.container, command, { tty, stdin: true }).join(' ')}\n`,
    );
    return ExitCode.OK;
  }

  if (node.status === 'revoked') {
    throw new BatonError({
      code: 'REVOKED',
      message: `${address} is revoked`,
      remediation: 'A revoked node cannot be reached. Create a new one.',
      exitCode: ExitCode.PRECONDITION,
    });
  }

  process.stdout.write(`  ${address}\n  Ctrl-D to leave.\n\n`);
  const code = await execInteractive(target.container, command, { tty, stdin: true });
  return code === 0 ? ExitCode.OK : ExitCode.INTERNAL;
}
