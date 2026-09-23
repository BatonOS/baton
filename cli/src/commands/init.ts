// SPDX-License-Identifier: Apache-2.0
































import { createHash } from 'node:crypto';
import { pluginsDir, readLedger, walkFiles, writeLedger, type LedgerLine } from '../runtime/plugins.js';
import { copyFileSync, cpSync, existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, readlinkSync, statSync, symlinkSync, writeFileSync } from 'node:fs';
import { loadWorkspaceTemplate, readTemplateHead } from '../runtime/template.js';
import { dirname, join, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

import { defaultDataDir } from '../api/client.js';
import { ExitCode, preconditionError, unsupportedError, usageError } from '../errors.js';
import { flagBool, type ParsedArgs } from '../args.js';

export interface InitResult {
  dataDir: string;
  created: boolean;
}









export function prepareMachine(args: ParsedArgs): InitResult {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const created = !existsSync(dataDir);

  mkdirSync(join(dataDir, 'nodes'), { recursive: true, mode: 0o700 });
  mkdirSync(join(dataDir, 'compose'), { recursive: true, mode: 0o700 });



  seedPlugins(dataDir);
  seedTemplates(dataDir);
  return { dataDir, created };
}





























export function seedPlugins(dataDir: string, from?: string): void {



  const shipped = from ?? join(dirname(fileURLToPath(import.meta.url)), '..', 'plugins');
  if (!existsSync(shipped)) return;
  const dest = pluginsDir(dataDir);
  mkdirSync(dest, { recursive: true, mode: 0o700 });












  const kept: LedgerLine[] = readLedger(dataDir).filter((l) => l.door !== 'seeded');

















  const stale: { id: string; why: string }[] = [];
  const shippedIDs = readdirSync(shipped).filter((id) => statSync(join(shipped, id)).isDirectory());
  for (const id of shippedIDs) {
    const from = join(shipped, id);
    if (!existsSync(join(dest, id))) continue;
    const shippedFiles = walkFiles(from);
    let why: string | undefined;
    for (const rel of shippedFiles) {
      let onDisk: Buffer;
      try { onDisk = readFileSync(join(dest, id, rel)); } catch { why = `${rel} is missing`; break; }













      if (!onDisk.equals(readFileSync(join(from, rel)))) { why = `${rel} differs`; break; }
    }



    if (!why) {
      const extra = walkFiles(join(dest, id)).find((rel) => !shippedFiles.includes(rel));
      if (extra) why = `${extra} is not shipped`;
    }
    if (why) stale.push({ id, why });
  }
  if (stale.length > 0) {
    throw preconditionError(
      `${stale.length} shipped facilit${stale.length === 1 ? 'y' : 'ies'} in ${dest} ${stale.length === 1 ? 'is' : 'are'} not what this build ships: ` +
        stale.map((x) => `${x.id} (${x.why})`).join(', '),
      'init does not overwrite a facility that is already there, and it does not stamp one it did not ship as `seeded` — ' +
        `so these would stay refused by every official template and untrusted on every node. Put this build's copy back:\n` +
        stale.map((x) => `  rm -rf ${join(dest, x.id)}`).join('\n') +
        '\n  baton init\n' +
        'If you edited one on purpose, install your edit under its own id with `baton plugin install`.',
    );
  }

  const ledger: LedgerLine[] = [];
  for (const id of shippedIDs) {
    const from = join(shipped, id);



    if (!existsSync(join(dest, id))) cpSync(from, join(dest, id), { recursive: true });















    for (const rel of walkFiles(join(dest, id))) {
      const onDisk = readFileSync(join(dest, id, rel));
      let shippedBytes: Buffer;
      try {
        shippedBytes = readFileSync(join(from, rel));
      } catch {
        continue;
      }
      if (!onDisk.equals(shippedBytes)) continue;
      const digest = createHash('sha256').update(onDisk).digest('hex');
      ledger.push({ digest, door: 'seeded', path: `${id}/${rel.split(sep).join('/')}` });
    }
  }


  writeLedger(dataDir, [...kept, ...ledger]);
}

export function seedTemplates(dataDir: string, from?: string): void {


  const shipped = from ?? join(dirname(fileURLToPath(import.meta.url)), '..', 'templates');
  if (!existsSync(shipped)) return;
  const dest = join(dataDir, 'templates');
  mkdirSync(dest, { recursive: true, mode: 0o700 });















  const keep = (src: string): boolean => {






    return readTemplateHead(src)?.official === true;
  };










  const stale: string[] = [];
  for (const f of readdirSync(shipped)) {
    if (!f.endsWith('.yaml')) continue;
    const src = join(shipped, f);
    if (!keep(src)) continue;
    const target = join(dest, f);
    if (existsSync(target) || lstatSync(target, { throwIfNoEntry: false })) {
      try {
        loadWorkspaceTemplate(target, dataDir);
      } catch (err) {
        stale.push(`${f} — ${(err instanceof Error ? err.message : String(err)).split('\n')[0]}`);
      }
      continue;
    }
    const st = lstatSync(src);



    if (st.isSymbolicLink()) symlinkSync(readlinkSync(src), target);
    else copyFileSync(src, target);
  }
  if (stale.length > 0) {
    process.stderr.write(
      `\x1b[33m warn\x1b[0m ${stale.length} shipped template(s) in ${dest} cannot be read by this build, and init never overwrites one that is there:\n` +
        stale.map((x) => `    ${x}`).join('\n') + '\n' +
        `  If they are your edits, fix them; if they are left over from an earlier build, delete them and init puts this build's back:\n` +
        stale.map((x) => `    rm ${join(dest, x.split(' — ')[0] ?? '')}`).join('\n') + '\n    baton init\n',
    );
  }
}

export async function init(args: ParsedArgs): Promise<number> {








  if (flagBool(args, 'ssh-config') || flagBool(args, 'print')) {
    throw unsupportedError(
      'writing the SSH client config is not implemented yet',
      'Its shape is decided — a delimited, idempotent block in ~/.ssh/config ' +
        'that `--print` emits and uninstall removes. `baton ssh <agent>` works today ' +
        'without it; this flag is reserved so the name cannot come to mean something else.',
    );
  }






  const sub = args.positionals[1];
  if (sub !== undefined) {
    throw usageError(
      `init ${sub} is not something init takes`,
      '`baton init` prepares this machine and takes no subcommand.',
    );
  }

  const { dataDir, created } = prepareMachine(args);

  process.stdout.write(
    (created ? `prepared ${dataDir}\n` : `${dataDir} is already prepared\n`) +
      '\n' +
      '  This machine can now talk to a control plane. It does not have one.\n' +
      '\n' +
      '    baton agent create --name agent-1    an agent here (founds a network if none exists)\n' +
      '    baton status --master <url>          use a control plane somewhere else\n',
  );
  return ExitCode.OK;
}
