// SPDX-License-Identifier: Apache-2.0

import type { Client } from '../api/client.js';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { cloudURL, cloudFetch, syncEndpointsToCloud, cloudNetworkView, cloudRefusalNote, type Attestation } from './cloud.js';
import { flagBool, flagString, parseDuration, type ParsedArgs } from '../args.js';
import { BatonError, ExitCode, usageError } from '../errors.js';
import { json, table } from '../output.js';
import { approveTransfer, invite } from './invite.js';
import { fetchDescriptor, resolveNetwork, verifyDescriptor } from '../runtime/resolver.js';









interface NetworkView {
  network_id: string;
  display_name: string;
  public_key: string;
  fingerprint: string;
  addresses: { address: string; source: string }[];
  endpoints: { address: string; port: number; protocol: string; priority: number }[];
  avatar: { content_type: string; data_base64: string } | null;
  cloud_address?: string;
  admission?: string;
  admission_changed_at?: string;
  admission_changed_by?: string;
  created_at: string;
}

interface JoinRequestView {
  request_id: string;
  agent: string;
  fingerprint: string;
  from_ip?: string;
  state: string;
  reason?: string;
  created_at: string;
  expires_at: string;
  decided_at?: string;
  decided_by?: string;
}























export function emptyRequestsLine(state: string, total: number | null): string {
  if (state === 'all' || total === 0) return 'Nobody has applied to join this network.';
  if (total === null) {
    return 'No application is waiting. This master did not say how many others it holds — ' +
      'baton network requests --state all shows them.';
  }
  if (state === 'pending') {
    return `No application is waiting. ${total} decided ` +
      (total === 1 ? 'one is' : 'ones are') +
      ' not shown — baton network requests --state all.';
  }
  return `No applications in state ${state}. ${total} in other states — --state all.`;
}

async function requests(args: ParsedArgs, client: Client): Promise<number> {











  const state = flagString(args, 'state') ?? 'pending';
  const filter = state === 'all' ? '' : `?state=${encodeURIComponent(state)}`;
  const res = await client.get<{ items: JoinRequestView[]; counts?: Record<string, number> }>(
    '/join-requests' + filter,
  );
  const items = res.items ?? [];








  const counts = res.counts;
  const total = counts ? Object.values(counts).reduce((a, b) => a + b, 0) : null;
  const hidden = total === null || state === 'all' ? 0 : total - items.length;

  if (args.global.output === 'json') {


    process.stdout.write(json({
      apiVersion: 'baton.mailloop.dev/v1alpha1', kind: 'JoinRequestList',


      state, counts: counts ?? null, items,
    }) + '\n');
    return ExitCode.OK;
  }

  const empty = emptyRequestsLine(state, total);

  process.stdout.write(
    table(
      items,
      [
        { header: 'agent', get: (r) => r.agent },
        { header: 'state', get: (r) => r.state },
        { header: 'fingerprint', get: (r) => r.fingerprint },
        { header: 'from', get: (r) => r.from_ip ?? '-' },
        { header: 'applied', get: (r) => r.created_at },
        { header: 'decided by', get: (r) => r.decided_by || '-' },
      ],
      empty,
    ) + '\n',
  );
  if (items.length > 0 && hidden > 0) {
    process.stdout.write(`  ${hidden} not shown (--state all).\n`);
  }
  return ExitCode.OK;
}











async function decide(args: ParsedArgs, client: Client, decision: 'admit' | 'deny'): Promise<number> {
  const agent = flagString(args, 'agent');
  if (!agent) {
    throw usageError(
      `network ${decision === 'admit' ? 'add' : 'deny'} needs --agent: whose application`,
      `For example: baton network ${decision === 'admit' ? 'add' : 'deny'} --agent reviewer. \`baton network requests\` lists them.`,
    );
  }
  if (flagString(args, 'out-file') || flagString(args, 'ttl')) {
    throw usageError(
      '--out-file and --ttl are gone: admitting answers an application, it does not mint a file',
      `The agent applies from its own machine (baton agent join --name ${agent} --network <this network>); then admit it here.`,
    );
  }
  const reason = flagString(args, 'reason');
  if (args.global.dryRun) {
    process.stdout.write(`would ${decision} ${agent}'s pending application. Nothing was decided.\n`);
    return ExitCode.OK;
  }
  const res = await client.post<JoinRequestView>(`/join-requests/${encodeURIComponent(agent)}/${decision}`,
    reason ? { reason } : {});
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    decision === 'admit'
      ? `admitted ${agent}\n` +
          `  fingerprint ${res.fingerprint}\n` +
          `  request     ${res.request_id}\n\n` +
          `  ${agent}'s node collects its admission and enrols on its own; \`baton node list\` shows it when it has.\n`
      : `denied ${agent}${reason ? ` (${reason})` : ''}\n  request ${res.request_id}\n`,
  );
  return ExitCode.OK;
}




















export async function publishEndpoint(
  client: Client,
  endpoint: string,
  domain?: string,
  replace = false,
): Promise<{ res: { network_id: string; fingerprint: string; endpoint: { address: string; port: number } }; cloudSync: { ok: boolean; note?: string } }> {




  const res = await client.post<{ network_id: string; fingerprint: string; endpoint: { address: string; port: number } }>(
    '/networks/self/endpoints', { endpoint, replace },
  );
  if (domain) await client.post('/networks/self/addresses', { address: domain, source: 'dns' });
  const cloudSync = await syncEndpointsToCloud(client).catch((e) => ({ ok: false, note: e instanceof Error ? e.message : String(e) }));
  return { res, cloudSync };
}

async function publish(args: ParsedArgs, client: Client): Promise<number> {
  const endpoint = flagString(args, 'endpoint');
  if (!endpoint || !/^https:\/\//.test(endpoint)) {
    throw usageError(
      'network publish needs --endpoint https://host:port — where this network answers',
      'For example: baton network publish --endpoint https://baton.example.com:8443 --domain example.com',
    );
  }
  const domain = flagString(args, 'domain');
  if (args.global.dryRun) {
    process.stdout.write(`would publish ${endpoint}${domain ? ` and record ${domain} (dns)` : ''}. Nothing was written.\n`);
    return ExitCode.OK;
  }
  const { res, cloudSync } = await publishEndpoint(client, endpoint, domain);
  if (args.global.output === 'json') {
    process.stdout.write(json({ ...res, domain: domain ?? null,
      cloud_endpoints: cloudSync,
      txt: domain ? { name: `_baton.${domain}`, value: `v=baton1; k=${res.fingerprint}; ep=${endpoint}` } : null }) + '\n');
    return ExitCode.OK;
  }





  const cloudNote = cloudSync.ok
    ? '\n  Its reachable endpoints are now in the BatonCloud registry. Whether its hosted\n' +
      '  name resolves depends on one being reachable from outside — the registry decides that.\n'
    : (cloudSync.note && cloudSync.note !== 'not cloud-connected'
      ? `\n  (registry not updated: ${cloudSync.note} — its hosted name will not resolve until it is)\n`
      : '');
  process.stdout.write(
    `published: ${endpoint}\n` +
      `  network      ${res.network_id}\n` +
      `  fingerprint  ${res.fingerprint}\n\n` +
      '  Its descriptor is now served at ' + endpoint.replace(/\/+$/, '') + '/api/v1alpha1/network,\n' +
      '  and an agent can apply with: baton agent join --name <n> --network ' + endpoint + '\n' +
      (domain
        ? '\n  To be named by your domain, publish this TXT record at _baton.<domain>:\n\n' +
          `    _baton.${domain}.  IN TXT  "v=baton1; k=${res.fingerprint}; ep=${endpoint}"\n\n` +
          `  then agents apply with: baton agent join --name <n> --network ${domain}\n`
        : '\n  To be named by a domain, run again with --domain <yours> and publish the TXT record it prints.\n') +
      cloudNote,
  );
  return ExitCode.OK;
}







async function resolveVerb(args: ParsedArgs): Promise<number> {
  const ref = args.positionals[2];
  if (!ref) throw usageError('network resolve needs a name', 'example.com · team@<registry> · https://host:8443');
  const r = await resolveNetwork(ref);
  const checked: { entry_point: string; verified: boolean; fingerprint: string | null; reason: string | null }[] = [];
  for (const ep of r.candidates) {
    try {
      const d = await fetchDescriptor(ep);
      const v = verifyDescriptor(d, r.pin);
      checked.push({ entry_point: ep, verified: v.ok, fingerprint: d.identity?.fingerprint ?? null, reason: v.reason ?? null });
    } catch (err) {
      checked.push({ entry_point: ep, verified: false, fingerprint: null, reason: err instanceof Error ? err.message : String(err) });
    }
  }
  if (args.global.output === 'json') {
    process.stdout.write(json({ network: r.network, channel: r.channel, pin: r.pin, notes: r.notes, candidates: checked }) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    `network   ${r.network}\n` +
      `channel   ${r.channel}\n` +
      `pin       ${r.pin ?? '(none — channel 0 learns the key on first contact)'}\n` +
      r.notes.map((n) => `note      ${n}\n`).join('') + '\n' +
      table(checked, [
        { header: 'entry point', get: (c) => c.entry_point },
        { header: 'verified', get: (c) => (c.verified ? 'yes' : 'no') },
        { header: 'fingerprint', get: (c) => c.fingerprint ?? '-' },
        { header: 'reason', get: (c) => c.reason ?? '' },
      ], 'No candidate entry points.') + '\n',
  );
  return ExitCode.OK;
}















async function registerName(args: ParsedArgs, client: Client): Promise<number> {
  const name = args.positionals[2];
  if (!name) throw usageError('network register needs a name', 'For example: baton network register acme-team');




  const att = await client.post<Attestation>('/networks/self/attestations', {
    purpose: 'network-register',
    network_name: name,
  });

  const res = await cloudFetch('/api/networks/name', {
    method: 'POST',
    body: JSON.stringify({
      network_id: att.network_id,
      network_name: name,
      public_key: att.public_key,
      payload: att.payload,
      signed_record: att.signature,
    }),
  });

  if (res.status !== 200) {
    const code = String(res.body?.code ?? res.status);



    const remediation =
      code === 'ACCOUNT_REQUIRED' || code === 'NETWORK_NOT_REGISTERED'
        ? 'This network has no account bound yet. Bind one first: baton cloud connect.'
      : code === 'NAME_TAKEN' || code === 'NAME_RESERVED'
        ? `The name ${name} is not available. Choose another.`
      : code === 'INVALID_NAME'
        ? 'A name is a bare label — no dots, no @, no +, within the length limit.'
      : code === 'CLOCK_SKEW' || code === 'REPLAYED'
        ? 'A timing or replay check failed; retry.'
      : (res.body?.remediation ?? 'The registry refused the name.');
    throw new BatonError({
      code,
      message: res.body?.message ?? `the registry refused to register ${name} (${res.status})`,
      remediation,











      requestId: res.body?.request_id,
      exitCode: code === 'ACCOUNT_REQUIRED' ? ExitCode.AUTH : ExitCode.PRECONDITION,
    });
  }






  const address = String(res.body?.cloud?.address ?? '');

  if (args.global.output === 'json') { process.stdout.write(json({ network_name: name, address }) + '\n'); return ExitCode.OK; }
  process.stdout.write(`registered: ${address || name}\n  This network's formal name is now ${name}.\n`);
  return ExitCode.OK;
}










async function admission(args: ParsedArgs, client: Client): Promise<number> {
  const want = args.positionals[2];
  if (want !== 'open' && want !== 'close' && want !== 'closed') {
    throw usageError(
      'network admission takes open or close',
      'baton network admission close — stop taking new applications. ' +
        'baton network admission open — take them again. `baton network show` says which it is now.',
    );
  }
  const value = want === 'open' ? 'open' : 'closed';
  if (args.global.dryRun) {
    process.stdout.write(`would set this network's admission to ${value}. Nothing was changed.\n`);
    return ExitCode.OK;
  }
  const res = await client.post<{ admission: string; admission_changed_at: string }>(
    '/networks/self/admission', { admission: value });
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    res.admission === 'closed'
      ? 'admission closed\n\n' +
          '  New applications are refused at the door, with a reason (they are not dropped in silence).\n' +
          '  Unchanged: the nodes already in this network, the applications already waiting for a\n' +
          '  decision, an admitted node\'s ability to enrol, and this network\'s discoverability.\n'
      : 'admission open\n\n  This network takes new applications again.\n',
  );
  return ExitCode.OK;
}

export async function network(args: ParsedArgs, client: Client): Promise<number> {
  const sub = args.positionals[1] ?? 'show';
  if (sub === 'publish') return publish(args, client);
  if (sub === 'resolve') return resolveVerb(args);
  if (sub === 'requests') return requests(args, client);
  if (sub === 'add') return decide(args, client, 'admit');
  if (sub === 'deny') return decide(args, client, 'deny');
  if (sub === 'invite') return invite(args, client);
  if (sub === 'approve-transfer') return approveTransfer(args, client);
  if (sub === 'set-name') return setName(args, client);
  if (sub === 'register') return registerName(args, client);
  if (sub === 'set-avatar') return setAvatar(args, client);
  if (sub === 'admission') return admission(args, client);
  if (sub === 'visibility') return visibility(args, client);
  if (sub !== 'show') {
    throw usageError(
      `network ${sub} is not a subcommand`,
      'Available: baton network show | set-name <name> | register <name> | set-avatar | admission open|close | publish --endpoint <url> | resolve <name> | requests | add --agent <name> | ' +
        'deny --agent <name> | invite <agent> --from <network> | approve-transfer --file <f> | visibility public|private',
    );
  }

  const net = await client.get<NetworkView>('/networks/self');





  let cloud: { bound: string; named: string; address_published: string; address: string } | null = null;



























  let registered_at_batoncloud: 'yes' | 'no' | 'unknown' = 'unknown';
  try {










    const v = await cloudNetworkView(client, net.network_id);
    registered_at_batoncloud = 'yes';
    cloud = { bound: v.bound, named: v.named, address_published: v.address_published, address: v.address };
  } catch (err) {










    const code = err instanceof BatonError ? err.code : '';
    if (code === 'NOT_FOUND') registered_at_batoncloud = 'no';
  }

  if (args.global.output === 'json') {
    process.stdout.write(json({ ...net, registered_at_batoncloud, cloud }) + '\n');
    return ExitCode.OK;
  }





  process.stdout.write(`network  ${net.network_id}  (${net.display_name})\n`);
  process.stdout.write(`identity ${net.fingerprint}\n`);



  process.stdout.write(
    `door     ${net.admission ?? 'unknown'}` +
      (net.admission === 'closed' ? '  (new applications are refused; nothing else is affected)' : '') +
      (net.admission_changed_at ? `  since ${net.admission_changed_at}` +
        (net.admission_changed_by ? ` by ${net.admission_changed_by}` : '') : '') + '\n',
  );
  if (cloud) process.stdout.write(`cloud    ${cloud.address}  (bound ${cloud.bound} · named ${cloud.named} · address published ${cloud.address_published})\n`);
  if (net.avatar) process.stdout.write(`avatar   ${net.avatar.content_type} (${Math.round((net.avatar.data_base64.length * 3) / 4 / 1024)} KiB)\n`);
  process.stdout.write('\n');

  if (net.addresses.length) {
    process.stdout.write(
      table(net.addresses, [
        { header: 'address', get: (a) => a.address },


        { header: 'resolved by', get: (a) => a.source },
      ], 'no addresses') + '\n',
    );
  } else {
    process.stdout.write('This network has no published address yet.\n');
    process.stdout.write('  A domain resolves through DNS; `baton cloud connect` takes a hosted name.\n\n');
  }

  if (net.endpoints.length) {
    process.stdout.write(
      table(net.endpoints, [
        { header: 'endpoint', get: (e) => `${e.address}:${e.port}` },
        { header: 'protocol', get: (e) => e.protocol },
        { header: 'priority', get: (e) => String(e.priority) },
      ], 'no endpoints published') + '\n',
    );
  }




  process.stdout.write(`\n${net.public_key.trim()}\n`);
  return ExitCode.OK;
}







async function setName(args: ParsedArgs, client: Client): Promise<number> {
  const name = args.positionals[2];
  if (!name) {
    throw usageError('set-name needs the name', 'For example: baton network set-name acme-lab');
  }
  if (args.global.dryRun) {
    process.stdout.write(`would name this network ${name}; its id, key, addresses and endpoints would not change\n`);
    return ExitCode.OK;
  }
  const res = await client.post<{ network_id: string; display_name: string }>('/networks/self/name', { display_name: name });
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(`network  ${res.network_id}  is now called ${res.display_name}\n` +
    '  Its identity, addresses and endpoints are unchanged; `baton status` and the panel show the new name.\n');
  return ExitCode.OK;
}








async function setAvatar(args: ParsedArgs, client: Client): Promise<number> {
  if (flagBool(args, 'clear')) {
    await client.post('/networks/self/avatar', { content_type: '', data_base64: '' });
    process.stdout.write('avatar cleared.\n');
    return ExitCode.OK;
  }
  const data = readFileSync(0);
  if (!data.length) throw usageError('set-avatar reads the image from stdin', 'Pipe one in: baton network set-avatar < logo.png (png, jpeg or webp), or --clear to remove.');
  const contentType = sniffImage(data);
  if (!contentType) throw usageError('the image is not a png, jpeg or webp', 'A network avatar is a raster image; SVG is not accepted.');
  if (data.length > 2 * 1024 * 1024) throw usageError(`the avatar is ${Math.round(data.length / 1024 / 1024 * 10) / 10} MiB, over the 2 MiB limit`, 'Shrink the image.');
  const res = await client.post<{ content_type: string; bytes: number }>('/networks/self/avatar', { content_type: contentType, data_base64: data.toString('base64') });
  const synced = await syncAvatarToCloud(client, contentType, data).catch((e) => ({ ok: false, note: e instanceof Error ? e.message : String(e) }));
  if (args.global.output === 'json') { process.stdout.write(json({ ...res, cloud_sync: synced }) + '\n'); return ExitCode.OK; }
  process.stdout.write(`avatar set (${res.content_type}, ${res.bytes} bytes).\n`);
  if (synced.ok) process.stdout.write(`  synced to Baton Cloud.\n`);
  else if (synced.note) process.stdout.write(`  not synced to cloud: ${synced.note}\n`);
  return ExitCode.OK;
}


function sniffImage(b: Buffer): string {
  if (b.length >= 8 && b[0] === 0x89 && b[1] === 0x50 && b[2] === 0x4e && b[3] === 0x47) return 'image/png';
  if (b.length >= 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) return 'image/jpeg';
  if (b.length >= 12 && b.toString('ascii', 0, 4) === 'RIFF' && b.toString('ascii', 8, 12) === 'WEBP') return 'image/webp';
  return '';
}












async function syncAvatarToCloud(client: Client, contentType: string, data: Buffer): Promise<{ ok: boolean; note?: string }> {
  const self = await client.get<{ network_id?: string; cloud_address?: string }>('/networks/self');
  if (!self.cloud_address || !self.network_id) return { ok: false, note: '' };

  const body = Buffer.from(`{"content_type":${JSON.stringify(contentType)},"data_base64":${JSON.stringify(data.toString('base64'))}}`, 'utf8');
  const bodySha256 = createHash('sha256').update(body).digest('hex');
  const path = `/api/networks/${self.network_id}/avatar`;













  const sig = await client.post<{ signature: string; timestamp: string; nonce: string }>('/networks/self/cloud-sign', { method: 'POST', path, body_sha256: bodySha256 });
  const res = await cloudFetch(path, {
    method: 'POST',
    headers: {
      'content-type': 'application/json',
      'x-baton-timestamp': sig.timestamp,
      'x-baton-nonce': sig.nonce,
      'x-baton-signature': sig.signature,
    },
    body,
  });
  if (res.status >= 200 && res.status < 300) return { ok: true };
  return { ok: false, note: `${cloudURL()} answered ${res.status}${res.body?.code ? ` (${res.body.code})` : ''}` };
}









export function parseVisibility(value: string | undefined): 'public' | 'private' {
  if (value === 'public' || value === 'private') return value;
  throw usageError(
    value
      ? `"${value}" is not a directory visibility`
      : 'network visibility needs a value',
    'A network is either listed in the public directory or it is not: ' +
      '`baton network visibility public` or `baton network visibility private`.',
  );
}




















async function visibility(args: ParsedArgs, client: Client): Promise<number> {
  const wanted = parseVisibility(args.positionals[2]);

  const self = await client.get<{ network_id?: string }>('/networks/self');
  if (!self.network_id) {
    throw new BatonError({
      code: 'NO_NETWORK_IDENTITY',
      message: 'this control plane has no network identity',
      remediation: 'A directory row is a statement about a network; create or join one first.',
      exitCode: ExitCode.UNSUPPORTED,
    });
  }

  const body = Buffer.from(JSON.stringify({ visibility: wanted }), 'utf8');
  const bodySha256 = createHash('sha256').update(body).digest('hex');
  const path = `/api/networks/${self.network_id}/directory`;
  const sig = await client.post<{ signature: string; timestamp: string; nonce: string }>(
    '/networks/self/cloud-sign', { method: 'PUT', path, body_sha256: bodySha256 });
  const res = await cloudFetch(path, {
    method: 'PUT',
    headers: {
      'content-type': 'application/json',
      'x-baton-timestamp': sig.timestamp,
      'x-baton-nonce': sig.nonce,
      'x-baton-signature': sig.signature,
    },
    body,
  });
  if (res.status < 200 || res.status >= 300) {















    throw new BatonError({
      code: res.body?.code ?? 'CLOUD_REFUSED',
      message: cloudRefusalNote(cloudURL(), res.status, res.body),


      remediation: res.body?.remediation ?? 'Nothing about the network changed.',
      exitCode: ExitCode.UNSUPPORTED,
    });
  }

  const effective = res.body?.visibility;
  if (args.global.output === 'json') {
    process.stdout.write(json({ visibility: effective ?? null }) + '\n');
    return ExitCode.OK;
  }
  if (effective !== 'public' && effective !== 'private') {


    process.stdout.write('  The directory accepted the request but did not say what it now holds.\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    effective === 'public'
      ? '  Listed in the public directory — the network can be found by search.\n'
      : '  Not listed — search will not find this network. Resolving it by name is unaffected.\n',
  );
  return ExitCode.OK;
}
