









import { readFileSync, writeFileSync } from 'node:fs';
import { readCredentialFile } from '../credential-file.js';
import type { Client } from '../api/client.js';
import type { ParsedArgs } from '../args.js';
import { flagString } from '../args.js';
import { BatonError, ExitCode, usageError, preconditionError } from '../errors.js';
import { json } from '../output.js';
import { cloudFetch, cloudURL } from './cloud.js';
import { documentHash, packageHash, methodFor, readCarrier, carrierLine } from '../dna/content-hash.js';
import type { HashMethod, PackageEntry } from '../dna/content-hash.js';

export async function dna(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const sub = args.positionals[1];
  switch (sub) {
    case 'add':
      return addDNA(args, newClient);
    case 'trace':
      return traceDNA(args);
    case 'verify-skill':
      return verifySkill(args, newClient);
    default:
      throw usageError(
        sub ? `dna ${sub} is not a subcommand` : 'dna needs a subcommand',
        'Available: baton dna add <file> --type <t> --name <n> | baton dna trace <file> | ' +
          'baton dna verify-skill --node <n> --skill <id> --instance <url> --agent-token <path>.',
      );
  }
}


function hashOf(path: string, type: string): { hash: string; method: HashMethod } {
  const method = methodFor(type);
  if (method === 'baton.text.v1') {
    return { hash: documentHash(readFileSync(path)), method };
  }
  return { hash: packageHash(readPackage(path)), method };
}










function readPackage(dir: string): PackageEntry[] {
  const { readdirSync, statSync } = require('node:fs') as typeof import('node:fs');
  const { join, relative, sep } = require('node:path') as typeof import('node:path');
  const out: PackageEntry[] = [];
  const walk = (d: string) => {
    for (const name of readdirSync(d)) {
      const full = join(d, name);
      if (statSync(full).isDirectory()) walk(full);



      else out.push({ path: relative(dir, full).split(sep).join('/'), content: readFileSync(full) });
    }
  };
  walk(dir);
  return out;
}


async function addDNA(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const path = args.positionals[2];
  if (!path) throw usageError('dna add needs a file', 'For example: baton dna add ./onboarding.md --type knowledge --name onboarding');
  const type = flagString(args, 'type');
  if (!type) throw usageError('dna add needs --type', 'One of: skill, template, workflow (packages) or knowledge, policy, other (documents). The type chooses the hashing channel.');
  const name = flagString(args, 'name');
  if (!name) throw usageError('dna add needs --name', 'The registry records WHICH resource was registered; a record that cannot name it is not a record.');

  const { hash, method } = hashOf(path, type);
  const resourceId = flagString(args, 'resource-id') ?? name;
  const version = flagString(args, 'version') ?? '';


  const parent = flagString(args, 'parent') ?? '';




  const att = await newClient().post<{
    network_id: string; public_key: string; payload: string; signature: string;
  }>('/networks/self/attestations', {
    purpose: 'dna-register',
    resource_id: resourceId,
    resource_type: type,
    resource_name: name,
    resource_version: version,
    content_hash: hash,
    parent_ref: parent,
  });










  const res = await cloudFetch('/api/dna/register', {
    method: 'POST',
    body: JSON.stringify({
      network_id: att.network_id,
      public_key: att.public_key,
      payload: att.payload,
      signed_record: att.signature,
    }),
  });
  const body = (res.body ?? {}) as Record<string, unknown>;
  if (res.status !== 200 && res.status !== 201) {
    throw new BatonError({
      code: String(body.code ?? 'DNA_REGISTER_FAILED'),
      message: String(body.message ?? `the registry refused (${res.status})`),
      remediation: String(body.remediation ?? 'The refusal codes are in the DNA contract; none of them mean "try again unchanged".'),
      exitCode: ExitCode.PRECONDITION,
    });
  }








  const token = String(body.trace_token ?? '');
  const recordId = String(body.record_id ?? '');
  const keyId = String(body.key_id ?? '');
  if (!token || !recordId) {
    throw preconditionError('the registry accepted the record but did not name it',
      'A carrier line needs the token the registry minted (trace_token). Nothing was written to the file.');
  }









  let out: string | null = null;
  if (method === 'baton.text.v1') {
    const original = readFileSync(path, 'utf8');
    const stamped = (original.endsWith('\n') ? original : original + '\n') +
      carrierLine(token, method, keyId) + '\n';
    writeFileSync(path, stamped);
    const after = documentHash(readFileSync(path));
    if (after !== hash) {
      throw preconditionError(
        `the stamp changed the content hash (${hash} -> ${after})`,
        'The file has been written. The carrier line is not being excluded from the hash for this file — do not trust a trace of it until that is fixed.',
      );
    }
    out = path;
  }

  if (args.global.output === 'json') {
    process.stdout.write(json({ record_id: recordId, token, method, content_hash: hash, key_id: keyId, stamped: out }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(`registered ${type}:${name}\n`);
  process.stdout.write(`  token        ${token}\n`);
  process.stdout.write(`  content      ${hash} (${method})\n`);
  process.stdout.write(out
    ? `  carrier line appended to ${out}, and the hash is unchanged.\n`
    : `  a package is not stamped in place — record the token with it.\n`);
  return ExitCode.OK;
}


async function traceDNA(args: ParsedArgs): Promise<number> {
  const path = args.positionals[2];
  if (!path) throw usageError('dna trace needs a file', 'For example: baton dna trace ./onboarding.md');

  const raw = readFileSync(path, 'utf8');
  const carrier = readCarrier(raw);
  if (!carrier) {


    process.stdout.write(`${path} carries no DNA line — nothing here says it was registered.\n`);
    return ExitCode.OK;
  }









  const parts = carrier.token.split(':');
  const issuer = parts[1] ?? '';
  const recordId = parts.slice(2).join(':');
  if (!issuer || !recordId) {
    throw preconditionError(`${path} carries a DNA line that is not a token: ${carrier.token}`,
      'A token is dna:<issuer>:<record_id>. Nothing was looked up.');
  }








  const type = flagString(args, 'type');
  const method = carrier.method as HashMethod;
  const hash = method === 'baton.zip.v1'
    ? packageHash(readPackage(path))
    : documentHash(readFileSync(path));
  void type;

  const res = await cloudFetch(
    `/api/dna/resolve/${encodeURIComponent(issuer)}/${encodeURIComponent(recordId)}?content_hash=${encodeURIComponent(hash)}`,
    { method: 'GET' },
  );
  const body = (res.body ?? {}) as Record<string, unknown>;












  if (res.status !== 200 || body.found === false || (body.code && body.found !== true)) {
    const code = String(body.code ?? 'DNA_RESOLVE_FAILED');













    const remediation = code === 'NOT_OUR_ISSUER'
      ? `The line in ${path} names the issuer "${issuer}", and ${cloudURL()} does not serve that name. ` +
        'This says NOTHING about whether the registration exists — a registry that renamed its issuer still holds ' +
        'every record it held before, under the new name. Re-register the file to get a current line, or ask this ' +
        "registry's operator which issuer it serves."
      : String(body.remediation ?? 'NO_RECORD means this registry has never seen that record id.');
    throw new BatonError({
      code,
      message: String(body.message ?? body.detail ?? `the registry could not resolve ${carrier.token} (${res.status})`),
      remediation,
      exitCode: ExitCode.PRECONDITION,
    });
  }

  if (args.global.output === 'json') {
    process.stdout.write(json({ ...body, token: carrier.token, content_hash: hash }) + '\n');
    return ExitCode.OK;
  }
  return renderTrace(body, carrier.token, hash);
}













export function renderTrace(body: Record<string, unknown>, token: string, hash: string): number {
  const match = body.content_match;
  process.stdout.write(`token    ${token}\n`);
  process.stdout.write(`content  ${hash}\n`);

  if (match === false) {



    process.stdout.write('\n⚠  This DNA line does not belong to this content.\n');
    process.stdout.write('   The token resolves — it is a real registration — but the bytes it was\n');
    process.stdout.write('   registered over are not the bytes in this file. The line may have been\n');
    process.stdout.write('   copied here, or the file may have been edited since it was registered.\n');
    process.stdout.write('   No source is reported for this file.\n');
    return ExitCode.OK;
  }
  if (match === undefined || match === null) {



    process.stdout.write('\n!  The content was NOT checked on this run — the registry returned no\n');
    process.stdout.write('   content_match. What follows describes the record, not this file.\n');
  }

  const rec = (body.record ?? body) as Record<string, unknown>;
  process.stdout.write('\nregistered\n');
  for (const [label, key] of [['name', 'resource_name'], ['type', 'resource_type'],
    ['network', 'network_id'], ['when', 'registered_at']] as const) {
    if (rec[key]) process.stdout.write(`  ${label.padEnd(9)}${String(rec[key])}\n`);
  }

  const lineage = Array.isArray(body.lineage) ? body.lineage : [];
  if (lineage.length > 0) {
    process.stdout.write('\nclaimed ancestry — signed by the registrant, not checked by anyone\n');
    for (const l of lineage as Record<string, unknown>[]) {
      process.stdout.write(`  ${String(l.parent_ref ?? l.ref ?? '?')}\n`);
    }
  }

  const similar = Array.isArray(body.observed_similarity) ? body.observed_similarity : [];
  if (similar.length > 0) {
    process.stdout.write('\nobserved by the registry — a measurement, with no relationship named\n');
    for (const s of similar as Record<string, unknown>[]) {
      const label = String(s.label ?? 'PRIOR MATCH');


      const c = s.confidence === null || s.confidence === undefined
        ? 'not measured'
        : String(s.confidence);
      process.stdout.write(`  ${label.padEnd(20)} ${String(s.record_id ?? s.token ?? '')}  confidence ${c}\n`);
    }
  }
  return ExitCode.OK;
}







































export async function verifySkill(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const nodeRef = flagString(args, 'node');
  const skillID = flagString(args, 'skill');
  const instance = flagString(args, 'instance');
  const tokenPath = flagString(args, 'agent-token');
  if (!nodeRef || !skillID || !instance || !tokenPath) {
    throw usageError(
      'dna verify-skill needs --node, --skill, --instance and --agent-token',
      'The node holds the file, the skill id names which one, the instance is the registry ' +
        'to ask, and the ticket is what authenticates the agent this run belongs to.',
    );
  }



  const ticket = readCredentialFile(tokenPath, 'verification ticket');

  const client = newClient();





  const list = await client.get<{ items: { skill_id: string; name: string }[] }>('/skills');
  const skill = (list.items ?? []).find((s) => s.skill_id === skillID);
  if (!skill) {
    throw preconditionError(
      `this control plane holds no skill ${skillID}`,
      'Run `baton skills list` to see the ids it does hold. Nothing was asked of the registry.',
    );
  }



  const seen = await client.post<{
    result: string;
    output?: string;
    data?: { content_digest?: string; code_id?: string };
  }>(`/nodes/${encodeURIComponent(nodeRef)}/console`, {
    command: 'skills.show',
    args: { skill: skill.name },
    reason: `verifying the skill-code on ${nodeRef}`,
  });
  if (seen.result !== 'ok') {
    throw preconditionError(
      `${nodeRef} could not say what its copy of ${skill.name} is${seen.output ? `: ${seen.output}` : ''}`,
      'The node has to be online and holding the package. Nothing was asked of the registry.',
    );
  }
  const currentStrip = seen.data?.content_digest ?? '';
  const codeID = seen.data?.code_id ?? '';







  if (!codeID) {
    return report(args, { state: 'no_code', node: nodeRef, skill: skill.name });
  }

  const ask = async (path: string, body: unknown) => {
    const res = await fetch(instance.replace(/\/$/, '') + path, {
      method: 'POST',
      headers: { 'content-type': 'application/json', authorization: `Bearer ${ticket}` },
      body: JSON.stringify(body),
    });
    let parsed: any = {};
    try {
      parsed = await res.json();
    } catch {
      parsed = {};
    }
    return { status: res.status, body: parsed as Record<string, any> };
  };




  const run = await ask('/api/dna/runs', { tools: [{ skill_id: skillID }] });
  if (run.status !== 201) {
    return report(args, {
      state: 'unverified',
      node: nodeRef,
      skill: skill.name,
      code_id: codeID,
      reason: `the registry would not open a run (${run.status} ${run.body.code ?? ''}): ${run.body.message ?? ''}`,
    });
  }
  const runID = String(run.body.run_id ?? '');
  const seq = Number(run.body.tools_recorded?.[0]?.seq ?? -1);

  const verdict = await ask('/api/dna/skill-code/verify', {
    code_id: codeID,
    current_strip: currentStrip,
    node: nodeRef,
    run_id: runID,
    seq,
  });
  if (verdict.status !== 200 || !verdict.body.state) {
    return report(args, {
      state: 'unverified',
      node: nodeRef,
      skill: skill.name,
      code_id: codeID,
      run_id: runID,




      issuer_key_id: String(run.body.issuer_key_id ?? ''),
      reason:
        verdict.status === 200
          ? 'the registry answered 200 without naming a state'
          : `the registry refused the question (${verdict.status} ${verdict.body.code ?? ''}): ${verdict.body.message ?? ''}`,
    });
  }



  return report(args, {
    state: String(verdict.body.state),
    node: nodeRef,
    skill: skill.name,
    code_id: codeID,
    run_id: runID,
    seq,
    attests: String(verdict.body.attests ?? ''),




    issuer_key_id: String(run.body.issuer_key_id ?? ''),
  });
}

function report(args: ParsedArgs, out: Record<string, unknown>): number {
  if (args.global.output === 'json') {
    process.stdout.write(json(out) + '\n');
    return ExitCode.OK;
  }
  const lines = [`${out.state}`];
  for (const [k, v] of Object.entries(out)) {
    if (k !== 'state' && v !== '' && v !== undefined) lines.push(`  ${k}: ${String(v)}`);
  }
  process.stdout.write(lines.join('\n') + '\n');
  return ExitCode.OK;
}
