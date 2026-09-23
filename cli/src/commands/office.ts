import { defaultDataDir } from '../api/client.js';
// SPDX-License-Identifier: Apache-2.0





























import { existsSync, readFileSync, writeFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { ExitCode, BatonError, preconditionError, usageError } from '../errors.js';
import { json } from '../output.js';
import { execCapture, execSucceeds } from '../runtime/engine.js';
import { flagBool, flagString, type ParsedArgs } from '../args.js';
import { snapshot } from './snapshot.js';
import type { LocalNodeState } from './create.js';
import { loadWorkspaceTemplate, materialise, resolveTemplateRef } from '../runtime/template.js';


const RUNTIME_HOME = '/var/lib/baton/home';

function registerPath(args: ParsedArgs, name: string): string {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  return join(dataDir, 'nodes', `${name}.json`);
}

export function readOffice(args: ParsedArgs, name: string): LocalNodeState {
  const p = registerPath(args, name);
  if (!existsSync(p)) {
    const dir = join(args.global.dataDir ?? defaultDataDir(), 'nodes');
    const known = existsSync(dir)
      ? readdirSync(dir).filter((f) => f.endsWith('.json')).map((f) => f.replace(/\.json$/, ''))
      : [];
    throw preconditionError(
      `this server has no node called ${name}`,
      known.length > 0
        ? `On the register: ${known.join(', ')}.`
        : 'Open one with `baton agent create --name <name>`.',
    );
  }
  return JSON.parse(readFileSync(p, 'utf8')) as LocalNodeState;
}

export function writeOffice(args: ParsedArgs, state: LocalNodeState): void {
  writeFileSync(registerPath(args, state.name), JSON.stringify(state, null, 2) + '\n', {
    mode: 0o600,
  });
}
































export async function moveIn(args: ParsedArgs, name: string, owner: string): Promise<number> {

  const office = readOffice(args, name);
  if (office.owner) {
    throw new BatonError({
      code: 'CONFLICT',
      message: `${name} still belongs to ${office.owner}`,
      remediation:
        'A node belongs to one agent, and ownership is fixed when it is built.' +
        ` Build ${owner} a node of their own: baton agent create ` +
        `--name ${owner}. To reuse this machine's resources instead, snapshot ` +
        `${name}, destroy it, and build again.`,
      exitCode: ExitCode.CONFLICT,
    });
  }

  if (flagString(args, 'runtime')) {
    throw usageError(
      '--runtime is gone: a spec file is a Workspace Template, named by --template',
      'Give --template <file|name>: it is the one flag that names a spec file.',
    );
  }
  const templateRef = flagString(args, 'template');
  if (!templateRef) {
    throw usageError(
      'moving in needs a fit-out',
      'An owner works somewhere. Give --template <file|name>: a Workspace Template.',
    );
  }
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const t = loadWorkspaceTemplate(resolveTemplateRef(templateRef, dataDir), dataDir);






  const { templateCopy } = materialise(t, dataDir, office.name);
  office.owner = owner;
  office.template = t.name;
  office.template_file = templateCopy;
  writeOffice(args, office);

  if (args.global.output === 'json') {
    process.stdout.write(json(office) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    `\n  ${owner} has moved into ${name}.\n\n` +
      `    fit-out   ${t.name}\n` +
      `    node id   ${office.node_id}   unchanged\n\n` +
      (office.driver_network
        ? '  The node is in a company, so the new owner is registered there too.\n'
        : '  The node is not in a company yet — the owner is on this server only.\n'),
  );
  return ExitCode.OK;
}
