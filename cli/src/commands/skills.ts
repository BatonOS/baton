// SPDX-License-Identifier: Apache-2.0

import { readCredentialFile } from '../credential-file.js';
import type { Client } from '../api/client.js';
import type { ParsedArgs } from '../args.js';
import { flagString } from '../args.js';
import { BatonError, ExitCode, preconditionError, usageError } from '../errors.js';
import { json, table } from '../output.js';
import { cloudFetch } from './cloud.js';
















interface SkillView {
  skill_id: string;
  name: string;
  version: string;
  sha256: string;
  source_url: string;
  size_bytes: number;
  created_at: string;
  cached: boolean;
  installs: { target_kind: string; target: string; created_at: string }[];
}








async function skillByID(client: Client, skillID: string): Promise<SkillView> {
  const list = await client.get<{ items: SkillView[] }>('/skills');
  const found = (list.items ?? []).find((s) => s.skill_id === skillID);
  if (!found) {
    throw preconditionError(
      `this control plane holds no skill ${skillID}`,
      'Run `baton skills list` to see the ids it does hold. Nothing was minted.',
    );
  }
  return found;
}


function target(args: ParsedArgs): { target_kind: string; target: string } {
  const node = flagString(args, 'node');
  const template = flagString(args, 'template');
  if (node && template) {
    throw usageError(
      'give --node or --template, not both',
      'A node install names one workspace. A template install names a scene and ' +
        'reaches nodes created from it later, including ones that do not exist yet.',
    );
  }
  if (node) return { target_kind: 'node', target: node };
  if (template) return { target_kind: 'template', target: template };
  throw usageError(
    'this needs a target',
    'Give --node <name> for one workspace, or --template <name> for every node ' +
      'set up from that template.',
  );
}

export async function skills(args: ParsedArgs, client: Client): Promise<number> {
  const sub = args.positionals[1] ?? 'list';

  if (sub === 'add') {
    const name = args.positionals[2];
    const url = flagString(args, 'url');
    const sha256 = flagString(args, 'sha256');
    if (!name || !url || !sha256) {
      throw usageError(
        'skills add needs a name, a --url and a --sha256',
        'For example: baton skills add review --url https://…/review.zip --sha256 sha256:…\n' +
          'The digest is not optional. There is no publisher signature behind a skill ' +
          'package, so it is the only check on what the source serves.',
      );
    }

    const created = await client.post<SkillView>('/skills', {
      name,
      version: flagString(args, 'version') ?? '',
      source_url: url,
      sha256,
    });

    if (args.global.output === 'json') {
      process.stdout.write(json(created) + '\n');
      return ExitCode.OK;
    }
    process.stdout.write(
      `added ${created.name} ${created.version}\n` +
        `  id      ${created.skill_id}\n` +
        `  digest  ${created.sha256}\n` +
        `  size    ${created.size_bytes} bytes\n\n` +


        `  Nothing has it yet. Install it with:\n` +
        `    baton skills install ${created.skill_id} --node <name>\n` +
        `    baton skills install ${created.skill_id} --template <name>\n`,
    );
    return ExitCode.OK;
  }
























  if (sub === 'code') {
    if (args.positionals[2] !== 'issue') {
      throw usageError(
        args.positionals[2] ? `skills code ${args.positionals[2]} is not a subcommand` : 'skills code needs a verb',
        'Today there is one: baton skills code issue --node <name> --skill <skill-id>.',
      );
    }
    const nodeRef = flagString(args, 'node');
    const skillID = flagString(args, 'skill');
    if (!nodeRef || !skillID) {
      throw usageError(
        'skills code issue needs --node and --skill',
        'A code binds one skill on one node, so both have to be named. ' +
          '`baton skills list` shows skill ids; `baton node list` shows node names.',
      );
    }































    const accountTokenPath = flagString(args, 'account-token');
    if (!accountTokenPath) {
      throw usageError(
        'skills code issue needs --account-token <path>',
        'Minting is account-gated at the registry, so this command has to present the ' +
          'account you already have. Point it at a file holding the token. Nothing was minted.',
      );
    }



    const accountToken = readCredentialFile(accountTokenPath, 'account token');


    const path = `/skills/${encodeURIComponent(skillID)}/nodes/${encodeURIComponent(nodeRef)}/code`;








    const skill = await skillByID(client, skillID);

    const seen = await client.post<{
      result: string;
      output?: string;
      data?: { content_digest?: string; code_id?: string };
    }>(`/nodes/${encodeURIComponent(nodeRef)}/console`, {
      command: 'skills.show',
      args: { skill: skill.name },
      reason: `minting a skill-code for ${skillID}`,
    });
    const digest = seen.data?.content_digest ?? '';






    if (seen.result !== 'ok' || !digest) {
      throw preconditionError(
        `${nodeRef} could not say what its copy of ${skill.name} is${seen.output ? `: ${seen.output}` : ''}`,
        'A code binds the digest of the file as that node holds it at this moment, so the ' +
          'node has to be online and holding the package. Check with `baton node show ' +
          nodeRef + '`, then try again. Nothing was minted.',
      );
    }

    const agent = flagString(args, 'agent') ?? nodeRef;
    const res = await cloudFetch('/api/dna/skill-code/issue', {
      method: 'POST',
      headers: { authorization: `Bearer ${accountToken}` },
      body: JSON.stringify({
        skill_id: skillID,
        content_digest: digest,
        agent,
        node: nodeRef,
      }),
    });
    const body = (res.body ?? {}) as Record<string, unknown>;
    if (res.status !== 200 && res.status !== 201) {
      throw new BatonError({
        code: String(body.code ?? 'SKILL_CODE_ISSUE_FAILED'),
        message: String(body.message ?? `the registry refused to mint (${res.status})`),
        remediation: String(body.remediation ?? 'Nothing was recorded on this side.'),
        exitCode: ExitCode.PRECONDITION,
      });
    }
    const codeID = String(body.code_id ?? '');
    if (!codeID) {



      throw preconditionError(
        'the registry minted a code but did not name it',
        'A code_id is what gets written into the skill and reported back at run time. ' +
          'Nothing was recorded.',
      );
    }
    await client.put(path, { code_id: codeID });

    if (args.global.output === 'json') {
      process.stdout.write(json({ skill_id: skillID, node: nodeRef, code_id: codeID }) + '\n');
      return ExitCode.OK;
    }
    process.stdout.write(
      `issued ${codeID} for ${skillID} on ${nodeRef}\n` +
        `  bound to what that node's copy says right now: ${digest}\n` +
        '  the node writes it into the skill on its next sync.\n',
    );
    return ExitCode.OK;
  }

  if (sub === 'install' || sub === 'uninstall') {
    const id = args.positionals[2];
    if (!id) {
      throw usageError(
        `skills ${sub} needs a skill id`,
        'Run `baton skills list` to see them.',
      );
    }
    const where = target(args);
    await client.post(`/skills/${encodeURIComponent(id)}/${sub}`, where);

    if (args.global.output === 'json') {
      process.stdout.write(json({ skill_id: id, ...where, action: sub }) + '\n');
      return ExitCode.OK;
    }
    const verb = sub === 'install' ? 'installed on' : 'uninstalled from';
    process.stdout.write(
      `${id} ${verb} ${where.target_kind} ${where.target}\n` +
        (where.target_kind === 'template'
          ? '  Every node set up from this template picks it up, including ones created later.\n'
          : '') +


        '  Connected nodes sync now; offline ones sync when they reconnect.\n',
    );
    return ExitCode.OK;
  }

  if (sub === 'remove') {
    const id = args.positionals[2];
    if (!id) {
      throw usageError('skills remove needs a skill id', 'Run `baton skills list` to see them.');
    }
    await client.delete(`/skills/${encodeURIComponent(id)}`);
    process.stdout.write(`removed ${id}\n  Every install of it goes too.\n`);
    return ExitCode.OK;
  }






  if (sub === 'list') {
    const body = await client.get<{ items: SkillView[] }>('/skills');
    if (args.global.output === 'json') {
      process.stdout.write(json(body) + '\n');
      return ExitCode.OK;
    }
    process.stdout.write(
      table(
        body.items,
        [
          { header: 'SKILL ID', get: (s) => s.skill_id },
          { header: 'NAME', get: (s) => s.name },
          { header: 'VERSION', get: (s) => s.version },



          { header: 'CACHED', get: (s) => (s.cached ? 'yes' : 'no') },
          {
            header: 'INSTALLED ON',
            get: (s) =>
              s.installs.length === 0
                ? '—'
                : s.installs.map((i) => `${i.target_kind}:${i.target}`).join(' '),
          },
        ],
        'no skills\n  Add one with: baton skills add <name> --url <u> --sha256 <d>\n',
      ),
    );
    return ExitCode.OK;
  }

  throw usageError(
    `unknown skills subcommand: ${sub}`,
    'Use add, list, install, uninstall, or remove. To share a skill to a network: baton resource publish --type skill.',
  );
}
