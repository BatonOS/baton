// SPDX-License-Identifier: Apache-2.0























import { existsSync, mkdirSync, readdirSync, readFileSync, statSync, writeFileSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { parse as parseYAML, stringify as stringifyYAML } from 'yaml';

import { BatonError, ExitCode, preconditionError, unsupportedError, usageError } from '../errors.js';
import { defaultDataDir } from '../api/client.js';
import { json } from '../output.js';
import { driverNetwork, flagBool, flagString, type ParsedArgs } from '../args.js';
import { containerExists, containerInventory, execCapture, projectRun } from '../runtime/engine.js';
import { containerName } from '../runtime/target.js';
import { reconcileApplied } from '../runtime/register.js';
import { channelOf, fetchDescriptor, networkKeyAuthority, networkKeySource, resolveNetwork, verifyDescriptor, type Channel } from '../runtime/resolver.js';
import { boundMounts, listTemplates, loadWorkspaceTemplate } from '../runtime/template.js';
import { prepareMachine } from './init.js';
import { anonymousGet } from './resource.js';
import { foundMaster, masterURLForContainer, nodeCreate, type LocalNodeState } from './create.js';
import { moveIn, readOffice, writeOffice } from './office.js';
import { internalVerbsOn } from '../internal-verbs.js';

















































export function foundingResumeSteps(entry: string, name: string): string {
  return (
    `    baton --master ${entry} network publish --endpoint ${entry}\n` +
    `    baton agent join --name ${name} --network ${entry}\n` +
    `    baton --master ${entry} network add --agent ${name}\n`
  );
}
















export type ControlPlaneEvidence = 'register' | 'engine';

export function controlPlaneEvidence(
  dataDir: string,
  inventory: (prefix: string) => { name: string }[] = containerInventory,
): ControlPlaneEvidence | undefined {
  const composeDir = join(dataDir, 'compose');
  if (
    existsSync(composeDir) &&
    readdirSync(composeDir).some((f) => f.startsWith('master-') && f.endsWith('.yml'))
  ) {
    return 'register';
  }
  return inventory('baton-master-').length > 0 ? 'engine' : undefined;
}

export function hasLocalControlPlane(
  dataDir: string,




  inventory: (prefix: string) => { name: string }[] = containerInventory,
): boolean {
  return controlPlaneEvidence(dataDir, inventory) !== undefined;
}









function officeByID(dataDir: string, id: string): LocalNodeState {
  const dir = join(dataDir, 'nodes');
  const offices: LocalNodeState[] = existsSync(dir)
    ? readdirSync(dir)
        .filter((f) => f.endsWith('.json'))
        .map((f) => JSON.parse(readFileSync(join(dir, f), 'utf8')) as LocalNodeState)
    : [];
  if (id.length < 8) {
    throw usageError(
      `--node-id ${id} is too short to identify a node`,
      'Give the full id, or at least the eight characters `baton node list` shows.',
    );
  }
  const hits = offices.filter((o) => o.node_id === id || o.node_id.startsWith(id));
  if (hits.length === 1) return hits[0]!;
  if (hits.length > 1) {
    throw usageError(
      `--node-id ${id} matches ${hits.length} nodes on this machine`,
      `Give more of the id. Matches: ${hits.map((o) => `${o.node_id} (${o.name})`).join(', ')}.`,
    );
  }
  throw preconditionError(
    `this machine has no node with id ${id}`,
    offices.length > 0
      ? `On the register: ${offices.map((o) => `${o.node_id.slice(0, 8)} ${o.name}`).join(', ')}. ` +
        'Ids of nodes created on another machine are not on it — this verb moves an owner into an office here.'
      : 'Nothing is on the register yet. `baton node create <name>` opens an office; ' +
        '`baton agent create --name <n>` without --node-id opens one and moves in at once.',
  );
}

export async function agent(args: ParsedArgs): Promise<number> {
  const sub = args.positionals[1] ?? '';





  if (sub === 'create' && !internalVerbsOn()) return agentCreate(args);
  if (sub === 'join') return agentJoin(args);
  if (sub === 'config') return agentConfig(args);
  if (sub === 'secret') return agentSecret(args);
  if (sub === 'shell') return agentShell(args);
  throw usageError(
    `agent ${sub || '(nothing)'} is not a subcommand`,
    (internalVerbsOn()
      ? 'Available: baton agent join --name <n> --network <network> | agent config show|set <n> | '
      : 'Available: baton agent create --name <n> [--node-id <id>] [--no-network] [--template <t>] | ' +
        'baton agent join --name <n> --network <network> | agent config show|set <n> | ') +
      'agent secret list|set <n> <NAME> | agent shell <n> [on|off]',
  );
}


















async function agentShell(args: ParsedArgs): Promise<number> {
  const name = args.positionals[2];
  const want = args.positionals[3] ?? '';
  if (!name) {
    throw usageError('agent shell needs an agent', 'For example: baton agent shell bob on   ·   baton agent shell bob   (prints on/off)');
  }
  if (want !== '' && want !== 'on' && want !== 'off') {
    throw usageError(
      `agent shell takes on or off, not ${want}`,
      'With neither, it prints the node\'s current answer: baton agent shell ' + name,
    );
  }
  const dataDir = args.global.dataDir ?? defaultDataDir();




  const office = officeOfAgent(args, dataDir, name);
  const container = containerName('agent', office.name);
  if (!containerExists(container)) {
    throw preconditionError(
      `${container} is not on this machine`,
      'The switch is the node\'s own, and this command reaches it through that container. ' +
        `If ${name} runs on another host, run \`baton agent shell ${name}${want ? ' ' + want : ''}\` there.`,
    );
  }
  const res = execCapture(container, ['baton-agent', 'shell', ...(want ? [want] : [])]);
  const said = (res.stdout || '').trim();
  if (!res.ok || (said !== 'on' && said !== 'off')) {
    throw new BatonError({
      code: 'SHELL_SWITCH_FAILED',
      message: `${name}'s node did not answer with on or off: ${(res.stderr || res.stdout || '').trim() || 'no output'}`,
      remediation:
        'The node daemon owns this switch (`baton-agent shell`). An older image does not have that ' +
        'subcommand yet — check with `docker exec ' + container + ' baton-agent version`, and rebuild the ' +
        'node on an image that carries it (`baton rebuild ' + office.name + '`).',
      exitCode: ExitCode.PRECONDITION,
    });
  }
  if (args.global.output === 'json') {
    process.stdout.write(json({ agent: name, node: office.name, remote_shell: said }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    want
      ? `${name}: remote shell ${said}\n` +
          (said === 'on'
            ? '  An operator with a certificate can now open a shell on this node through the control plane.\n'
            : '  The node will refuse `/pty` from the control plane; a shell here on this host is unaffected.\n')
      : `${name}: remote shell ${said}\n`,
  );
  return ExitCode.OK;
}


























async function agentConfig(args: ParsedArgs): Promise<number> {
  const op = args.positionals[2];
  const name = args.positionals[3];
  if ((op !== 'show' && op !== 'set') || !name) {
    throw usageError('agent config needs show|set and an agent', 'For example: baton agent config show bob');
  }
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const file = join(dataDir, 'compose', `agent-${name}.template.yaml`);
  if (op === 'show') {
    let yaml: string;
    try { yaml = readFileSync(file, 'utf8'); } catch {
      throw preconditionError(`${name} has no kept workspace config at ${file}`, 'It was created without a template, or from another data directory.');
    }
    if (args.global.output === 'json') { process.stdout.write(json({ agent: name, path: file, yaml }) + '\n'); return ExitCode.OK; }
    process.stdout.write(yaml.endsWith('\n') ? yaml : yaml + '\n');
    return ExitCode.OK;
  }




  const body = readFileSync(0, 'utf8');
  if (!body.trim()) throw usageError('agent config set reads the new config from stdin', 'Pipe a Node Template in: baton agent config set bob --stdin < t.yaml');
  const tmp = join(dataDir, 'compose', `agent-${name}.template.yaml.incoming`);
  writeFileSync(tmp, body, { mode: 0o644 });
  try {
    loadWorkspaceTemplate(tmp, dataDir);
  } catch (err) {
    rmSync(tmp, { force: true });
    throw err instanceof BatonError ? err : usageError(`the config is not a valid Workspace Template: ${err instanceof Error ? err.message : String(err)}`, 'It must be kind: WorkspaceTemplate with a node section; the runtime comes from a plugin listed under `plugins:`, and that list may be empty.');
  }
  writeFileSync(file, body, { mode: 0o644 });
  rmSync(tmp, { force: true });
  if (args.global.output === 'json') { process.stdout.write(json({ agent: name, path: file, saved: true }) + '\n'); return ExitCode.OK; }
  process.stdout.write(
    `saved ${name}'s workspace config (${file}).\n` +
      `  The office running now is unchanged: it mounts the spec materialised when it was\n` +
      `  created, and \`baton rebuild\` re-creates the container from the same manifest —\n` +
      `  it swaps the image, not the contract.\n` +
      `  This copy is what a node MADE from it gets (\`baton clone\`, \`baton migrate\`).\n` +
      `  To refit this office with it:\n` +
      `    baton snapshot ${name}      # pack up the work first\n` +
      `    baton destroy ${name} --yes\n` +
      `    baton agent create --name ${name} --template ${file}\n`,
  );
  return ExitCode.OK;
}

























async function agentSecret(args: ParsedArgs): Promise<number> {
  const op = args.positionals[2];
  const name = args.positionals[3];
  if (op !== 'list' && op !== 'set') {
    throw usageError('agent secret needs list|set', 'For example: baton agent secret list bob');
  }
  if (!name) throw usageError('agent secret needs an agent', 'For example: baton agent secret list bob');
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const templateFile = join(dataDir, 'compose', `agent-${name}.template.yaml`);
  if (!existsSync(templateFile)) {
    throw preconditionError(`${name} has no kept workspace config at ${templateFile}`, 'It was created without a template, or from another data directory.');
  }


  const t = loadWorkspaceTemplate(templateFile, dataDir);





  const bound = boundMounts(dataDir, name);
  type Decl = { name?: string; from?: string; mountPath?: string };
  const decls: { name: string; from: string; mountPath: string; plugin: string }[] = [];
  for (const p of t.plugins) {
    const rt = p.runtime as { execution?: { secrets?: Decl[] } } | undefined;
    for (const x of [...(rt?.execution?.secrets ?? []), ...(p.service?.secrets ?? [])] as Decl[]) {
      if (!x.name) continue;
      const mountPath = x.mountPath ?? `/run/secrets/${x.name}`;
      const source = bound.get(mountPath);
      decls.push({ name: x.name, from: source ? `file:${source}` : (x.from ?? ''), mountPath, plugin: p.id });
    }
  }


  if (op === 'list') {
    const rows = decls.map((d) => {
      const hostPath = d.from.startsWith('file:') ? d.from.slice('file:'.length) : '';



      let present = false;
      try { present = hostPath !== '' && statSync(hostPath).size > 0; } catch { present = false; }
      return { name: d.name, mount_path: d.mountPath, present, plugin: d.plugin };
    });
    if (args.global.output === 'json') { process.stdout.write(json({ agent: name, secrets: rows }) + '\n'); return ExitCode.OK; }
    if (!rows.length) { process.stdout.write(`${name}'s template declares no secrets.\n`); return ExitCode.OK; }
    for (const r of rows) process.stdout.write(`  ${r.present ? '✓' : '·'} ${r.name}${r.mount_path ? ` → ${r.mount_path}` : ''}  (${r.plugin})${r.present ? '' : '   (not set)'}\n`);
    return ExitCode.OK;
  }


  const targetName = args.positionals[4];
  if (!targetName) throw usageError('agent secret set needs a secret NAME', `For example: baton agent secret set ${name} MODEL_API_KEY < token.txt`);
  const decl = decls.find((d) => d.name === targetName);
  if (!decl) {
    throw usageError(
      `${name}'s template declares no secret called ${targetName}`,
      `It declares: ${decls.map((d) => d.name).join(', ') || '(none)'}. A secret must be declared in the template before its value can be set.`,
    );
  }
  if (!decl.from.startsWith('file:')) {
    throw preconditionError(`secret ${targetName} has from: ${decl.from || '(none)'}; only file: sources take a value`, 'Fix the template so the secret reads from a file.');
  }
  const hostPath = decl.from.slice('file:'.length);
  const value = readFileSync(0);
  if (!value.length) throw usageError('agent secret set reads the value from stdin', `Pipe it in: baton agent secret set ${name} ${targetName} < value.txt`);
  mkdirSync(dirname(hostPath), { recursive: true });
  writeFileSync(hostPath, value, { mode: 0o600 });
  if (args.global.output === 'json') { process.stdout.write(json({ agent: name, secret: targetName, path: hostPath, bytes: value.length, saved: true }) + '\n'); return ExitCode.OK; }
  process.stdout.write(`set secret ${targetName} for ${name} (${value.length} bytes, mode 0600).\n  It takes effect when the office next reads it: for a running one, baton rebuild ${name}.\n`);
  return ExitCode.OK;
}














































async function agentJoinStatus(args: ParsedArgs, name: string): Promise<number> {
  const dataDir = args.global.dataDir ?? defaultDataDir();
  reconcileApplied(dataDir, name);
  const officePath = join(dataDir, 'nodes', `${name}.json`);
  if (!existsSync(officePath)) {
    throw usageError(`no office named ${name} on this machine`,
      "baton agent list shows the offices here. --status reads this machine's register; it does not search a network.");
  }
  const office = JSON.parse(readFileSync(officePath, 'utf8')) as LocalNodeState;






  const membership: string = office.driver_network
    ? 'member'
    : office.applied_to
      ? 'enrolling'
      : 'none';


  const requestId = office.applied_request_id;
  const entry = office.applied_entry;
  let application: Record<string, unknown> = { state: 'unknown', request_id: requestId ?? null, asked: null };

  if (membership === 'member' && !office.applied_to) {



    application = { state: 'consumed', request_id: requestId ?? null, asked: null };
  } else if (requestId && entry) {
    try {
      const doc = await anonymousGet<Record<string, unknown>>(entry, `/join-requests/${encodeURIComponent(requestId)}`);
      application = {
        state: String(doc.state ?? 'unknown'),
        request_id: requestId,
        decided_at: doc.decided_at ?? null,


        decided_by: doc.decided_by ?? null,
        reason: doc.reason ?? null,
        asked: entry,
      };
    } catch {



      application = { state: 'unknown', request_id: requestId, asked: entry };
    }
  }

  const out = {
    agent: name,
    application,
    membership: { state: membership },





    network: office.network_ref ?? office.applied_to ?? null,
    network_id: office.network_id ?? null,
    fingerprint: office.network_fingerprint ?? null,
  };
  if (args.global.output === 'json') {
    process.stdout.write(json(out) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(`${name}\n`);
  process.stdout.write(`  application  ${String(application.state)}${application.request_id ? `  (${String(application.request_id)})` : ''}\n`);
  if (application.state === 'unknown') {
    process.stdout.write(application.asked
      ? `               asked ${String(application.asked)} — no answer\n`
      : `               nobody to ask: this office carries no request id\n`);
  }
  if (application.decided_by) process.stdout.write(`               decided by ${String(application.decided_by)}\n`);
  if (application.reason) process.stdout.write(`               reason ${String(application.reason)}\n`);
  process.stdout.write(`  membership   ${membership}\n`);
  if (out.network_id) process.stdout.write(`  network_id   ${out.network_id}\n`);

  if (out.fingerprint) process.stdout.write(`  network key  ${out.fingerprint}\n`);
  return ExitCode.OK;
}

async function agentJoin(args: ParsedArgs): Promise<number> {
  const name = flagString(args, 'name') ?? args.positionals[2];
  if (!name) {
    throw usageError(
      'agent join needs --name: which agent is applying',
      'For example: baton agent join --name reviewer --network https://host:8443',
    );
  }
  for (const gone of ['enrollment-token-file', 'out-file'] as const) {
    if (flagString(args, gone)) {
      throw usageError(
        `--${gone} is gone: joining is an application, not a credential hand-off — the agent applies by naming the network, the master admits it by name, and no token file passes through a person`,
        'Say which network: baton agent join --name ' + name + ' --network <network>. The master admits with `baton network add --agent ' + name + '`.',
      );
    }
  }


  if (args.flags.get('status') === true) return agentJoinStatus(args, name);

  const network = flagString(args, 'network');
  if (!network) {
    throw usageError(
      '--network is required: which network is this agent applying to',
      'A domain (example.com), a hosted name (team@<registry>), or the entry point itself (https://host:8443).',
    );
  }
  return joinNetwork(args, name, network);
}








export async function joinNetwork(args: ParsedArgs, name: string, networkRef: string): Promise<number> {
  let network = networkRef;
  const dataDir = args.global.dataDir ?? defaultDataDir();
  const first = officeOfAgent(args, dataDir, name);
  reconcileApplied(dataDir, first.name);
  const office = officeOfAgent(args, dataDir, name);
  if (!office.owner) {
    throw preconditionError(
      `${office.name} has no agent in it, so there is nothing that can join`,
      'A node cannot join a network; an agent joins, with the office it holds. Move one in first: ' +
        `baton agent create --name ${name} --node-id ${office.node_id.slice(0, 8)} --template <file|name>`,
    );
  }
  if (office.applied_to) {
    throw new BatonError({
      code: 'APPLICATION_PENDING',
      message: `${name} has already applied to ${office.applied_to} and is waiting for its master`,
      remediation: `On that master: baton network requests · baton network add --agent ${name}. ` +
        'A second application does not queue behind the first.',
      exitCode: ExitCode.CONFLICT,
    });
  }
  if (office.driver_network) {
    throw new BatonError({
      code: 'ALREADY_JOINED',
      message: `${name} is already in a network`,
      remediation:
        'Moving an agent between networks is a transfer, not a join: the losing network\'s master ' +
        'signs it — baton network invite / approve-transfer.',
      exitCode: ExitCode.CONFLICT,
    });
  }






  let entryPoint: string | null = null;
  let networkFP: string | null = null;





  let networkID: string | null = null;
  let pin: string | null = null;
  let pinChannel = '';
  try {
    const self = await new (await import('../api/client.js')).Client({ baseURL: args.global.master, adminDir: args.global.adminDir ?? (await import('../api/client.js')).defaultAdminDir(args.global.dataDir), timeoutMs: args.global.timeoutMs }).get<{ display_name?: string; network_id?: string }>('/networks/self');
    if (network === self.display_name || network === self.network_id) {
      entryPoint = args.global.master;



      networkID = self.network_id ?? null;




      try {
        const d = await fetchDescriptor(entryPoint);
        networkFP = d.identity?.fingerprint ?? null;
      } catch {  }
    }
  } catch {  }

  if (!entryPoint) {




  const resolved = await resolveNetwork(network);
  const refusals: string[] = [];
  for (const ep of resolved.candidates) {
    try {
      const d = await fetchDescriptor(ep);
      const v = verifyDescriptor(d, resolved.pin);
      if (v.ok) { entryPoint = ep; networkFP = d.identity.fingerprint; networkID = d.network_id || null; pin = resolved.pin; pinChannel = resolved.channel; break; }
      refusals.push(`${ep}: ${v.reason}`);
    } catch (err) {
      refusals.push(`${ep}: ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  if (!entryPoint) {





    throw new BatonError({
      code: 'NETWORK_UNREACHABLE',
      message: `no entry point of ${network} could be verified`,
      remediation:
        (refusals.length ? refusals.join('\n  ') + '\n  ' : '') +
        (resolved.channel === 'entry-point'
          ? 'The entry point serves no descriptor: its operator publishes with `baton network publish --endpoint …` first.'
          : 'Recovery re-asks the same channel; the other channel is not a fallback.'),
      exitCode: ExitCode.PRECONDITION,
    });
  }
  }








  const resolvedEntry = entryPoint;

  const composeFile = join(dataDir, 'compose', `agent-${office.name}.yml`);
  if (!existsSync(composeFile)) {
    throw preconditionError(
      `${office.name} has no manifest in ${join(dataDir, 'compose')}`,
      'It was created from another data directory, or its manifest was removed. Join from where it was created.',
    );
  }





  const doc = parseYAML(readFileSync(composeFile, 'utf8')) as {
    services?: Record<string, { environment?: Record<string, string> }>;
    networks?: Record<string, { name?: string }>;
  };
  const driverNet = doc.networks?.default?.name ?? driverNetwork({ ...args, flags: new Map([...args.flags].filter(([k]) => k !== 'network')) }).name;





















  const namedNetwork = channelOf(network) !== 'entry-point';
  const entry = args.global.dryRun
    ? network
    : namedNetwork
      ? network
      : masterURLForContainer(resolvedEntry, driverNet, dataDir);

  if (args.global.dryRun) {
    process.stdout.write(
      `\n  Would apply for ${name} (office ${office.name}, ${office.node_id.slice(0, 8)}) to join ${network}.\n` +
        '  Nothing was sent.\n',
    );
    return ExitCode.OK;
  }



  const applied = projectRun(composeFile, 'agent',
    ['apply', '--network', entry, '--name', name, ...(pin ? ['--pin', pin] : [])]);
  if (!applied.ok) {
    const answered = enrollFailure(applied.stderr);










    const answeredCode = /^(?:(?:application|enrollment) failed:\s*)?([A-Z][A-Z0-9_]{2,}):/.exec(answered)?.[1];
    throw new BatonError({
      code: answeredCode ?? 'APPLICATION_FAILED',
      message: answered || `${name} could not apply to ${network}`,








      remediation: answered
        ? ''
        : 'Is the entry point right and reachable from this machine\'s containers? An application ' +
          'already pending by this name is refused — the master decides once.',
      exitCode: ExitCode.PRECONDITION,
    });
  }
  let requestID = '';
  let fingerprint = '';



  let appliedNetworkFP = '';
  try {
    const out = JSON.parse(applied.stdout.trim().split('\n').filter((l) => l.startsWith('{') || l.startsWith(' ') || l.startsWith('}')).join('\n')) as { request_id?: string; fingerprint?: string; network_fingerprint?: string };
    requestID = out.request_id ?? '';
    fingerprint = out.fingerprint ?? '';
    appliedNetworkFP = out.network_fingerprint ?? '';
  } catch {


  }


















  if (rewriteVerdict(network, entry, networkFP, appliedNetworkFP) === 'mismatch') {





    throw new BatonError({
      code: 'NETWORK_IDENTITY_MISMATCH',
      message:
        `the application was lodged with a different network than the one verified here.\n` +
        `    verified at  ${network}  ${networkFP}\n` +
        `    lodged at    ${entry}  ${appliedNetworkFP}`,
      remediation:
        `An application${requestID ? ` (${requestID})` : ''} is now pending at the second one. ` +
        `Denying it there (\`baton network deny --agent ${name} --reason …\`, on that master) is also what ` +
        `frees this office to apply again — it holds one outstanding application and clears it when the ` +
        `answer arrives.\n` +
        `  The container cannot reach ${network} by that address, so it was rewritten to the control ` +
        `plane on this office's driver network, and that is a different network. Give ` +
        `--network an address the CONTAINER can reach (this machine's LAN or public address, not ` +
        `loopback), or put the office on the driver network of the control plane it should join.`,
      exitCode: ExitCode.CONFLICT,
    });
  }


  const rewritten = entry !== network;





  const identityCarries = rewriteVerdict(network, entry, networkFP, appliedNetworkFP) === 'same-network';




  const svc = doc.services?.agent;
  if (svc) {
    svc.environment = { ...(svc.environment ?? {}), BATON_MASTER_URL: entry };
    writeFileSync(composeFile, stringifyYAML(doc, { lineWidth: 0 }), { mode: 0o644 });
  }


  office.applied_to = networkRef;












  office.applied_request_id = requestID || null;

















  office.applied_entry = network || null;

















  office.network_id = identityCarries ? networkID : null;
  office.network_fingerprint = identityCarries ? networkFP : (appliedNetworkFP || null);













  office.network_ref = networkRef;
  writeOffice(args, office);





  if (args.global.output === 'json') {
    process.stdout.write(json({
      agent: name, network: networkRef, request_id: requestID || null,
      fingerprint: fingerprint || null,
      network_key: (identityCarries ? networkFP : appliedNetworkFP) || null,



      network_id: identityCarries ? networkID : null,



      lodged_at: rewritten ? entry : null,



      network_key_source: networkKeySource(Boolean(pin), pinChannel as Channel),



      network_key_authority: networkKeyAuthority(Boolean(pin), pinChannel as Channel, networkRef),
      state: 'pending',
    }) + '\n');
    return ExitCode.OK;
  }
  process.stderr.write(
    `\n  ${name} applied to join ${networkRef}${networkRef !== network ? ` (${network})` : ''}.\n\n` +
      (requestID ? `    request     ${requestID}\n` : '') +
      (fingerprint ? `    fingerprint ${fingerprint}   ← what the master sees; compare it out of band\n` : '') +
      (rewritten ? `    lodged at   ${entry}   ← rewritten from ${network}; a container cannot dial the host's loopback\n` : '') +
      (!identityCarries ? '    network key unknown — this office reached another address and nothing proved it is the same network\n' : '') +
      (identityCarries && networkFP ? `    network key ${networkFP}   ${
        networkKeySource(Boolean(pin), pinChannel as Channel) === 'first-contact'
          ? '(learned on first contact — channel 0 pins nothing)'
          : `(pinned by ${networkKeySource(Boolean(pin), pinChannel as Channel)})`}\n` : '') +
      '\n  Nothing more to do here. On the master:\n\n' +
      '    baton network requests\n' +
      `    baton network add --agent ${name}\n\n` +
      '  When admitted, this node enrols by itself and `baton status` shows it joined.\n',
  );
  return ExitCode.OK;
}
























export function rewriteVerdict(
  given: string, lodged: string, hostKey: string | null, lodgedKey: string | null,
): 'same-network' | 'mismatch' | 'unproven' {
  if (given === lodged) return 'same-network';
  if (!hostKey || !lodgedKey) return 'unproven';
  return hostKey === lodgedKey ? 'same-network' : 'mismatch';
}








function enrollFailure(stderr: string): string {
  const lines = stderr.split('\n');
  const at = lines.findIndex((l) => /^(application|enrollment) failed:/.test(l));
  const tail = (at >= 0 ? lines.slice(at) : lines.filter((l) => !/^\s*Container |^\{/.test(l)))
    .map((l) => l.trimEnd()).filter((l) => l.trim().length > 0);
  return tail.join('\n').trim();
}


export function officeOfAgent(args: ParsedArgs, dataDir: string, agent: string): LocalNodeState {
  const dir = join(dataDir, 'nodes');
  const p = join(dir, `${agent}.json`);
  if (existsSync(p)) return readOffice(args, agent);
  const offices: LocalNodeState[] = existsSync(dir)
    ? readdirSync(dir).filter((f) => f.endsWith('.json'))
        .map((f) => JSON.parse(readFileSync(join(dir, f), 'utf8')) as LocalNodeState)
    : [];
  const owned = offices.filter((o) => o.owner === agent);
  if (owned.length === 1) return owned[0]!;
  if (owned.length > 1) {
    throw preconditionError(
      `${agent} owns ${owned.length} offices on this machine`,
      `One agent, one office. Offices: ${owned.map((o) => o.name).join(', ')}.`,
    );
  }
  throw preconditionError(
    `this machine has no agent called ${agent}`,
    offices.length > 0
      ? `On the register: ${offices.map((o) => `${o.name}${o.owner ? ` (@${o.owner})` : ' (empty)'}`).join(', ')}. ` +
        `Create the agent first: baton agent create --name ${agent} --no-network --template <file>`
      : `Create it first: baton agent create --name ${agent} --no-network --template <file>`,
  );
}















export async function agentCreate(args: ParsedArgs): Promise<number> {
  const name = flagString(args, 'name') ?? args.positionals[2];







  const say = (text: string): void => {
    (args.global.output === 'json' ? process.stderr : process.stdout).write(text);
  };
  if (!name) {
    throw usageError(
      'agent create needs a name',
      'For example: baton agent create --name agent-1',
    );
  }




  if (flagString(args, 'network')) {
    throw unsupportedError(
      '--network is reserved: joining a network by address is not implemented',
      'An agent is created here, then applies: `baton agent join --name <n> --network <network>`; ' +
        'the master admits with `baton network add --agent <n>`.',
    );
  }


  const { dataDir } = prepareMachine(args);






  if (flagString(args, 'enrollment-token-file')) {
    throw usageError(
      '--enrollment-token-file is not accepted by agent create: creating never joins',
      `Create the agent first, then apply: baton agent create --name ${name} --no-network … && ` +
        `baton agent join --name ${name} --network <network>`,
    );
  }










  if (!flagString(args, 'template')) {








    {
      const present = listTemplates(dataDir);



      const official = present.filter((t) => t.official).map((t) => t.name);
      const any = present.map((t) => t.name);










      throw usageError(
        'a runtime is always named: pass --template <name>',
        official.length
          ? `Choose one: ${official.map((n) => `--template ${n}`).join(' or ')}. (Official templates Baton ships: ${official.join(', ')}.)`
          : any.length
            ? `This installation ships no official Node Template, so \`baton init\` has nothing to seed. ` +
              `Templates on this machine: ${any.join(', ')} — name one with --template <name>.`
            : 'This installation ships no official Node Template, so `baton init` has nothing to seed, ' +
              'and there is none on this machine either. Put a Node Template under <data-dir>/templates/ ' +
              'and pass --template <name>.',
      );
    }
  }












  const noNetwork = flagBool(args, 'no-network');
  const evidence = controlPlaneEvidence(dataDir);
  const founding = !noNetwork && evidence === undefined;








  if (!noNetwork && evidence === 'engine') {
    say(
      `\n  Not founding a control plane: this machine already runs one that ${'this install did not create'}.\n` +
        `  Its container is here (\`baton-master-*\`), while ${dataDir}/compose has no master of its own.\n` +
        '  On a shared host that master may belong to somebody else. Declining is the safe half —\n' +
        '  founding a second one on top of an existing network cannot be undone.\n' +
        `  To found one anyway, do it on a machine of your own, or point --data-dir at a host with no other master.\n`,
    );
  }







  const nodeIDFlag = flagString(args, 'node-id');
  let officeName = name;
  if (nodeIDFlag) {
    const office = officeByID(dataDir, nodeIDFlag);
    officeName = office.name;









    if (args.global.dryRun) {
      say(
        `\n  Would move ${name} into ${office.name} (${office.node_id}). Nothing was written.\n`,
      );
      if (founding) {
        say(
          '  This machine has no control plane, so it would also found one here\n' +
            `  and give it to ${name}.\n`,
        );
      }
      return ExitCode.OK;
    }
    const rc = await moveIn(args, office.name, name);
    if (rc !== ExitCode.OK) return rc;
  } else {







    const nodeArgs: ParsedArgs = {
      ...args,
      positionals: ['node', 'create', name],
      flags: new Map([...args.flags, ['owner', flagString(args, 'owner') ?? name]]),
    };
    const rc = await nodeCreate(nodeArgs);
    if (rc !== ExitCode.OK) return rc;
  }














  if (founding && args.global.dryRun) {
    say(
      '\n' +
        '  This machine has no control plane, so creating this agent would found\n' +
        `  one here and give it to ${name}. Nothing was written.\n`,
    );
    return ExitCode.OK;
  }

  if (founding) {
    const { code: rcMaster, entry } = await foundMaster(args, officeName);
    if (rcMaster !== ExitCode.OK) return rcMaster;





    say(
      '\n' +
        '  This machine had no control plane, so one was founded here.\n' +
        `  ${name} holds it. Agents created here later still join by applying —\n` +
        '  `baton agent join` — and this master admits them.\n' +
        '\n' +
        '  The control plane is its own container and runs no agent. Your\n' +
        '  agent lives in its office, so the office now joins the network it\n' +
        '  just founded, so its identity and mailbox live on the node where its\n' +
        '  daemon runs; you, as the network\'s only operator, admit it —\n' +
        '  published, applied, admitted, enrolled:\n' +
        '\n',
    );





    const partial = (step: string, detail: string): BatonError =>
      new BatonError({
        code: 'FOUNDING_PARTIAL',
        message: `the network is founded, but the founder's office is not enrolled yet — ${step} (${detail})`,
        remediation:
          'Everything that succeeded stands: the control plane is up and the network exists.\n' +
          '  Continue from the breakpoint (each step is safe to repeat):\n' +
          foundingResumeSteps(entry, name) +
          '  `baton node list` shows the office as not-enrolled until then — that is the true state.',
        exitCode: ExitCode.PARTIAL,
      });




    const { Client } = await import('../api/client.js');
    const client = new Client({
      baseURL: entry,
      adminDir: args.global.adminDir ?? join(dataDir, 'admin'),
      timeoutMs: args.global.timeoutMs,
    });





    try {
      await client.post('/networks/self/endpoints', { endpoint: entry, replace: false });
      say(`  published ${entry} as this network's entry point.\n`);
    } catch (err) {
      throw partial('the entry point is not published', err instanceof Error ? err.message : String(err));
    }


    try {
      const rcJoin = await joinNetwork({ ...args, global: { ...args.global, master: entry } }, name, entry);
      if (rcJoin !== ExitCode.OK) throw partial('the application did not go through', `join exited ${rcJoin}`);
    } catch (err) {
      if (err instanceof BatonError && err.code === 'FOUNDING_PARTIAL') throw err;
      throw partial('the application did not go through', err instanceof Error ? err.message : String(err));
    }






    try {
      await client.post(`/join-requests/${encodeURIComponent(name)}/admit`, {});
    } catch (err) {
      throw partial('your admission is still owed', err instanceof Error ? err.message : String(err));
    }
    say(`  admitted @${name} to the network you just founded — you hold the\n  master; this admission was yours, not automatic.\n`);




    let enrolled = false;
    for (let i = 0; i < 30; i++) {
      try {





        const view = await client.get<{ status?: string; roles?: string[] }>(`/nodes/${encodeURIComponent(name)}`);
        if (view.status === 'active' && (view.roles ?? []).includes('agent')) { enrolled = true; break; }
      } catch {  }
      await new Promise((resolve) => setTimeout(resolve, 2000));
    }
    if (!enrolled) throw partial('the office has not finished enrolling', 'no active node record after 60s');
    say(`  @${name}'s office enrolled — the identity lives on the node whose\n  daemon answers for it, so mail to @${name} has a reader.\n`);
  }






  if (noNetwork) {
    say(
      '\n' +
        `  agent @${name} ${args.global.dryRun
          ? 'would exist on this machine and would be in no network'
          : 'exists on this machine and is in no network'}.\n` +
        '\n' +
        `  Nothing ${args.global.dryRun ? 'would be' : 'was'} founded: no network identity, no certificate authority.\n` +
        '  `baton node list` shows it as not-enrolled, which is a state, not a\n' +
        '  failure. It joins one when its master admits it:\n' +
        '\n' +
        `    (here)           baton agent join --name ${name} --network <network>\n` +
        `    (on the master)  baton network add --agent ${name}\n`,
    );
    return ExitCode.OK;
  }




  if (nodeIDFlag && !founding && !officeByID(dataDir, nodeIDFlag).driver_network) {
    say(
      `\n  agent @${name} is in ${officeName} and in no network — the office joined\n` +
        '  nothing when it was opened, and moving in does not change that.\n',
    );
    return ExitCode.OK;
  }






  if (!founding && !nodeIDFlag) {
    say(
      `\n  agent @${name} exists on this machine and is in no network yet.\n` +
        '\n' +
        '  This machine\'s control plane does not change that: joining is the\n' +
        '  agent\'s act: the agent applies, and the network\'s master admits or\n' +
        '  denies it. Two commands:\n' +
        '\n' +
        `    baton agent join --name ${name} --network <network>\n` +
        `    baton network add --agent ${name}     (on the master)\n`,
    );
    return ExitCode.OK;
  }
  say(`\n  agent @${name} is on ${founding ? 'the network it just founded' : 'this network'}.\n`);
  return ExitCode.OK;
}
