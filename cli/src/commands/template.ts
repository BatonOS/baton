// SPDX-License-Identifier: Apache-2.0













import { defaultDataDir } from '../api/client.js';
import { ExitCode, usageError } from '../errors.js';
import { json, table } from '../output.js';
import type { ParsedArgs } from '../args.js';
import { listTemplates, loadWorkspaceTemplate, resolveTemplateRef, templatesDir } from '../runtime/template.js';
import { pluginView } from '../runtime/plugins.js';
import { readFileSync } from 'node:fs';




export async function template(args: ParsedArgs): Promise<number> {
  const sub = args.positionals[1] ?? 'list';
  const dataDir = args.global.dataDir ?? defaultDataDir();
  if (sub === 'show') {



    const ref = args.positionals[2];




    if (!ref) throw usageError('show needs a template', 'Name one: `baton template show <name>`. See what is here with `baton template list`.');
    const path = resolveTemplateRef(ref, dataDir);
    let yaml: string;
    try { yaml = readFileSync(path, 'utf8'); } catch { throw usageError(`no template ${ref} at ${path}`, 'See names with `baton template list`.'); }
    if (args.global.output === 'json') {
























      let resolved: unknown = null;
      let plugins: unknown;
      let error: string | undefined;
      try {
        const t = loadWorkspaceTemplate(path, dataDir);
        resolved = t.runtime ?? null;









        plugins = t.plugins.map(pluginView);
      } catch (err) {




        error = err instanceof Error ? err.message : String(err);
      }
      process.stdout.write(json({ name: ref, path, yaml, resolved, plugins, error }) + '\n');
      return ExitCode.OK;
    }
    process.stdout.write(yaml.endsWith('\n') ? yaml : yaml + '\n');
    return ExitCode.OK;
  }
  if (sub !== 'list') {
    throw usageError(`template ${sub} is not a subcommand`, 'Available: baton template list | show <name>');
  }
  const items = listTemplates(dataDir);



  if (args.global.output === 'json') {
    process.stdout.write(json({ apiVersion: 'baton.mailloop.dev/v1alpha1', kind: 'TemplateList', dir: templatesDir(dataDir), items }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    table(
      items,
      [
        { header: 'name', get: (t) => t.name },


        { header: 'package', get: (t) => t.image ?? '-' },
        { header: 'cpu', get: (t) => t.cpu ?? '-' },
        { header: 'memory', get: (t) => t.memory ?? '-' },
        { header: 'secrets', get: (t) => t.secrets.length ? t.secrets.map((x) => x.name).filter(Boolean).join(',') : '-' },



        { header: 'source', get: (t) => t.source },
        { header: 'official', get: (t) => (t.official ? 'yes' : '-') },
        { header: 'note', get: (t) => t.error ? `unreadable: ${t.error.split('\n')[0]}` : (t.differs_from_shipped ? 'differs from the shipped file of this name (edited, or seeded before it changed)' : '') },
      ],
      `No templates in ${templatesDir(dataDir)}. Put a Node Template there as <name>.yaml, or give --template a path.`,
    ) + '\n' +





      `\n  Name one of these with --template <name>. There is no default.\n`,
  );
  return ExitCode.OK;
}
