// SPDX-License-Identifier: Apache-2.0

















import { createReadStream, readFileSync, statSync } from 'node:fs';
import { basename } from 'node:path';
import { Readable } from 'node:stream';

import type { Client } from '../api/client.js';
import { flagBool, flagString, type ParsedArgs } from '../args.js';
import { ExitCode, preconditionError, usageError } from '../errors.js';
import { json, table } from '../output.js';

interface MessageEnvelope {
  message_id: string;
  sender: string;
  recipient: string;
  state: string;
  created_at: string;
  payload_size: number;





  delivered_at?: string | null;











  inbox_policy?: 'allowed' | 'held' | 'unknown';

  held_reason?: string;
}


interface InboxPolicy {
  identity: string;
  act_on: 'everyone' | 'allowlist' | 'nobody';
  allow_senders: string[];
  allow_networks: string[];

  channels: 'allow' | 'hold';

  policy_set: boolean;
}
const CHANNELS = ['allow', 'hold'] as const;

const ACT_ON = ['everyone', 'allowlist', 'nobody'] as const;

interface IdentityView {
  name: string;
  node_id?: string;
  bound: boolean;
}


function identityName(raw: string): string {
  return raw.startsWith('@') ? raw.slice(1) : raw;
}

















function parseAttachRef(ref: string): { name: string; content_type: string; sha256: string } {
  const cut = ref.indexOf(':');
  const sha = cut < 0 ? '' : ref.slice(0, cut);
  const name = cut < 0 ? '' : ref.slice(cut + 1);
  if (!/^[0-9a-f]{64}$/.test(sha) || !name) {
    throw usageError(
      `--attach-ref ${ref} is not <sha256>:<name>`,
      'The digest is the 64-character value `baton inbox attach` printed; the name is everything after the first colon. For example: --attach-ref 9f2c…e1:report.pdf',
    );
  }
  return { name, content_type: contentTypeOf(name), sha256: sha };
}


function attachPaths(args: ParsedArgs): string[] {
  return args.repeated.get('attach') ?? [];
}













async function uploadAttachments(
  client: Client,
  args: ParsedArgs,
): Promise<{ name: string; content_type: string; sha256: string }[]> {
  const paths = attachPaths(args);



  const out: { name: string; content_type: string; sha256: string }[] = [];
  for (const ref of args.repeated.get('attach-ref') ?? []) {
    out.push(parseAttachRef(ref));
  }
  for (const p of paths) {
    let size: number;
    try {
      size = statSync(p).size;
    } catch {


      throw preconditionError(
        `${p} cannot be read`,
        'Check the path. Nothing has been sent — the message goes out only after every attachment is stored.',
      );
    }
    const stored = await client.postStream<{ sha256: string; size: number }>(
      '/messages/attachments',
      createReadStream(p),
      size,
    );
    if (!stored?.sha256) {
      throw preconditionError(
        `the control plane stored ${basename(p)} without answering with a digest`,
        'Nothing was sent. This is a control-plane fault, not something the message can work around.',
      );
    }
    out.push({


      name: basename(p),
      content_type: contentTypeOf(p),
      sha256: stored.sha256,
    });
  }
  return out;
}







function contentTypeOf(path: string): string {
  const byExt: Record<string, string> = {
    '.txt': 'text/plain', '.md': 'text/markdown', '.json': 'application/json',
    '.yaml': 'application/yaml', '.yml': 'application/yaml', '.csv': 'text/csv',
    '.pdf': 'application/pdf', '.png': 'image/png', '.jpg': 'image/jpeg',
    '.jpeg': 'image/jpeg', '.gif': 'image/gif', '.svg': 'image/svg+xml',
    '.zip': 'application/zip', '.tar': 'application/x-tar', '.gz': 'application/gzip',
    '.log': 'text/plain', '.html': 'text/html',
  };
  const dot = path.lastIndexOf('.');
  if (dot < 0) return 'application/octet-stream';
  return byExt[path.slice(dot).toLowerCase()] ?? 'application/octet-stream';
}

export async function send(args: ParsedArgs, client: Client): Promise<number> {
  const to = args.positionals[1];
  if (!to) {
    throw usageError(
      'send needs a recipient',
      'An agent here: baton send @reviewer --text "…"\n' +



        '  An agent on another network: baton send dns.coder@batonnetwork.org --text "…"\n' +
        '  (that address is matched against networks you have already established a\n' +
        '   relationship with — it is never looked up, so an unknown domain is a refusal)',
    );
  }

  const text = flagString(args, 'text');
  const file = flagString(args, 'file');



  const fromStdin = flagBool(args, 'stdin');
  const chosen = [text !== undefined, !!file, fromStdin].filter(Boolean).length;
  if (chosen === 0) {
    throw usageError(
      'send needs a body',
      'Pass --text "…", --file ./task.md, or --stdin (body on standard input). A task is usually a file; --text is for one-liners.',
    );
  }
  if (chosen > 1) {
    throw usageError('send takes one of --text, --file, --stdin', 'Pick one source for the body.');
  }

  const body = file ? readFileSync(file) : fromStdin ? readFileSync(0) : Buffer.from(text!, 'utf8');








  const messageID = flagString(args, 'message-id');





  const replyTo = flagString(args, 'reply-to');




  const msgType = flagString(args, 'type');







  if (args.global.dryRun) {
    const plan = {
      would_send: true,
      to: identityName(to),
      bytes: body.length,
      content_type: file ? 'application/octet-stream' : 'text/plain',
      message_id: messageID ?? null,
      reply_to: replyTo ?? null,
      type: msgType ?? null,
    };
    process.stdout.write(
      args.global.output === 'json'
        ? json(plan) + '\n'
        : `would send ${body.length} bytes (${plan.content_type}) to @${plan.to}` +
            (messageID ? ` as ${messageID}` : '') +





            (attachPaths(args).length
              ? `\n  with ${attachPaths(args).length} attachment(s): ${attachPaths(args).join(', ')}` +
                '\n  (not uploaded, and not checked against the size limit — that happens when it is sent)'
              : '') +
            '\n  nothing was sent.\n',
    );
    return ExitCode.OK;
  }







  const attachments = await uploadAttachments(client, args);

  const res = await client.post<{

    message?: MessageEnvelope;
    delivered?: boolean;
    duplicate?: boolean;




    message_id?: string;
    to?: string;
    destination_network?: string;
    accepted_by_network?: boolean;
    accepted_by_mailbox?: boolean;
    mailbox?: string;
  }>('/messages', {
    to: identityName(to),
    body: body.toString('base64'),
    content_type: file ? 'application/octet-stream' : 'text/plain',
    ...(messageID ? { message_id: messageID } : {}),
    ...(replyTo ? { reply_to: replyTo } : {}),
    ...(msgType ? { type: msgType } : {}),
    ...(attachments.length ? { attachments } : {}),
  });

  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }




  if (res.accepted_by_mailbox) {




    process.stdout.write(
      `left ${res.message_id} in the offline mailbox for ${res.to}\n` +
        `  ${res.destination_network} is not reachable right now, so this went to the\n` +
        '  registry-run mailbox it keeps for that network. **It has not been delivered**,\n' +
        '  and the bytes are resting on a third party until that network collects them.\n' +
        '  You will not be told when it does.\n',
    );
    return ExitCode.OK;
  }
  if (res.accepted_by_network) {
    process.stdout.write(
      `accepted by ${res.destination_network} for ${res.to}\n` +
        '  that network took it. Whether its recipient has read it is on the other side\n' +
        '  of the boundary — this network cannot see that and does not claim to.\n',
    );
    return ExitCode.OK;
  }

  if (res.duplicate) {



    process.stdout.write(
      `already sent ${res.message?.message_id} to @${res.message?.recipient}\n` +
        '  this id was used before, so nothing was sent a second time.\n',
    );
    return ExitCode.OK;
  }

  process.stdout.write(
    !res.delivered
      ? `queued ${res.message?.message_id} for @${res.message?.recipient}\n` +
          '  it is not reachable right now; it will be delivered when it comes back.\n' +
          `  retry safely with: baton send ${to} --message-id ${res.message?.message_id} …\n`
      : `delivered ${res.message?.message_id} to @${res.message?.recipient}\n`,
  );
  return ExitCode.OK;
}






















async function attachUpload(args: ParsedArgs, client: Client): Promise<number> {
  const name = flagString(args, 'name');
  if (!name) {
    throw usageError(
      'inbox attach needs --name: what this file is called',
      'The name travels in the envelope, so a recipient sees what they were sent without fetching it: baton inbox attach --name report.pdf < ./report.pdf',
    );
  }
  const body = readFileSync(0);
  if (body.length === 0) {
    throw usageError(
      'nothing arrived on standard input',
      'The bytes are the body: baton inbox attach --name report.pdf < ./report.pdf',
    );
  }
  const stored = await client.postStream<{ sha256: string; size: number }>(
    '/messages/attachments',
    Readable.from(body),
    body.length,
  );
  if (args.global.output === 'json') {
    process.stdout.write(json(stored) + '\n');
    return ExitCode.OK;
  }



  process.stdout.write(`stored ${name}\n  sha256 ${stored.sha256}\n  size   ${stored.size}\n`);
  return ExitCode.OK;
}

export async function inbox(args: ParsedArgs, client: Client): Promise<number> {





  if (args.positionals[1] === 'open') return open_(args, client);
  if (args.positionals[1] === 'delete') return deleteMessage(args, client);
  if (args.positionals[1] === 'mark-all-read') return markAllRead(args, client);
  if (args.positionals[1] === 'attach') return attachUpload(args, client);
  if (args.positionals[1] === 'policy') return policy(args, client);




  const who = args.positionals[1] === 'list' ? args.positionals[2] : args.positionals[1];
  const state = flagString(args, 'state');


  const thread = flagString(args, 'thread');
  const box = flagString(args, 'box');

  const q = new URLSearchParams();
  if (who) q.set('to', identityName(who));
  if (state) q.set('state', state);
  if (thread) q.set('thread', thread);
  if (box) q.set('box', box);
  const suffix = q.toString() ? `?${q}` : '';

  const res = await client.get<{ messages: MessageEnvelope[] }>(`/messages${suffix}`);
  const messages = res.messages ?? [];

  process.stdout.write(
    args.global.output === 'json'
      ? json(messages) + '\n'
      : table(
          messages,
          [
            { header: 'message', get: (m) => m.message_id },
            { header: 'from', get: (m) => m.sender },
            { header: 'to', get: (m) => `@${m.recipient}` },
            { header: 'state', get: (m) => m.state },





            { header: 'delivered', get: (m) => (m.delivered_at ? 'yes' : '-') },
            { header: 'policy', get: (m) => m.inbox_policy === 'held' ? `held (${m.held_reason || '?'})` : (m.inbox_policy ?? 'unknown') },
            { header: 'bytes', get: (m) => String(m.payload_size) },
          ],
          'no messages.',
        ) + '\n',
  );
  return ExitCode.OK;
}

















export async function policy(args: ParsedArgs, client: Client): Promise<number> {
  const raw = args.positionals[2];
  if (!raw) {
    throw usageError('baton inbox policy needs an agent', 'baton inbox policy <agent> [--act-on everyone|allowlist|nobody] [--channels allow|hold] [--allow <sender>]… [--allow-network <id>]…');
  }
  const identity = identityName(raw);
  const path = `/inbox/policy/${encodeURIComponent(identity)}`;

  const actOn = flagString(args, 'act-on');
  const channels = flagString(args, 'channels');
  const allow = args.repeated.get('allow') ?? [];
  const allowNet = args.repeated.get('allow-network') ?? [];
  const remove = args.repeated.get('remove') ?? [];
  const removeNet = args.repeated.get('remove-network') ?? [];
  const writes = actOn !== undefined || channels !== undefined || allow.length + allowNet.length + remove.length + removeNet.length > 0;

  const current = await client.get<InboxPolicy>(path);
  if (!writes) {
    process.stdout.write(args.global.output === 'json' ? json(current) + '\n' : renderPolicy(current));
    return ExitCode.OK;
  }

  if (channels !== undefined && !(CHANNELS as readonly string[]).includes(channels)) {
    throw usageError(`--channels ${channels} is not one of ${CHANNELS.join(' | ')}`,
      '`hold` keeps mail a facility\'s service relayed in (envelope `via`) received and not delivered; `allow` acts on it like any other mail.');
  }
  if (actOn !== undefined && !(ACT_ON as readonly string[]).includes(actOn)) {
    throw usageError(`--act-on ${actOn} is not one of ${ACT_ON.join(' | ')}`,
      '`nobody` holds all mail (received, not delivered); `allowlist` consults --allow / --allow-network; `everyone` consults nothing.');
  }
  for (const s of [...allow, ...remove]) {
    if (s.includes('@') && !s.startsWith('@')) {
      throw usageError(`--allow/--remove ${s} names an address, not a local identity`,
        'A sender on another network is admitted a whole network at a time: --allow-network <network_id>. ' +
          'Local names only here — an address comes from the network\'s hosted name, not its identity, and can change, so a policy must not be anchored to one.');
    }
  }
  const senders = new Set(current.allow_senders ?? []);
  for (const s of allow) senders.add(identityName(s));
  for (const s of remove) senders.delete(identityName(s));
  const networks = new Set(current.allow_networks ?? []);
  for (const n of allowNet) networks.add(n);
  for (const n of removeNet) networks.delete(n);
  const next = {
    act_on: actOn ?? current.act_on,
    allow_senders: [...senders].sort(),
    allow_networks: [...networks].sort(),
    channels: channels ?? current.channels,
  };
  const res = await client.put<{ policy: InboxPolicy; redelivery: string }>(path, next);
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
  } else {
    process.stdout.write(renderPolicy(res.policy));



    process.stdout.write(`redelivery ${res.redelivery} — held mail is re-evaluated against this policy now; read \`baton inbox list ${identity}\` (delivered / policy columns) for what moved.\n`);
  }
  return ExitCode.OK;
}

function renderPolicy(p: InboxPolicy): string {
  const lines = [
    `identity        @${p.identity}`,
    `act_on          ${p.act_on}${p.policy_set ? '' : '   (default — nobody has set a policy for this identity)'}`,
    `allow_senders   ${p.act_on === 'allowlist' ? (p.allow_senders.length ? p.allow_senders.join(', ') : '(none — under allowlist that means nobody)') : `${p.allow_senders.length ? p.allow_senders.join(', ') : '-'}   (not consulted under ${p.act_on})`}`,
    `allow_networks  ${p.act_on === 'allowlist' ? (p.allow_networks.length ? p.allow_networks.join(', ') : '(none)') : `${p.allow_networks.length ? p.allow_networks.join(', ') : '-'}   (not consulted under ${p.act_on})`}`,
    `channels        ${p.channels}${p.channels === 'hold' ? '   (mail relayed in by a facility service is received, not delivered)' : ''}`,
  ];
  return lines.join('\n') + '\n';
}
























async function deleteMessage(args: ParsedArgs, client: Client): Promise<number> {
  const id = args.positionals[2];
  if (!id) {
    throw usageError('inbox delete needs a message id', 'Find one with `baton inbox`.');
  }
  const res = await client.delete<{ message_id: string; box: string }>(`/messages/${encodeURIComponent(id)}`);
  process.stdout.write(
    args.global.output === 'json' ? json(res) + '\n' : `moved ${res.message_id} to the ${res.box} box\n`,
  );
  return ExitCode.OK;
}












async function markAllRead(args: ParsedArgs, client: Client): Promise<number> {
  const who = flagString(args, 'to');
  const q = who ? `?to=${encodeURIComponent(identityName(who))}` : '';
  const res = await client.post<{ marked: number; unread: number }>(`/messages/mark-all-read${q}`, {});
  process.stdout.write(
    args.global.output === 'json'
      ? json(res) + '\n'
      : `marked ${res.marked} as read; ${res.unread} unread remain\n`,
  );
  return ExitCode.OK;
}

export async function open_(args: ParsedArgs, client: Client): Promise<number> {
  const id = args.positionals[2];
  if (!id) {
    throw usageError('inbox open needs a message id', 'Find one with `baton inbox`.');
  }
  const body = await client.postRaw(`/messages/${encodeURIComponent(id)}/payload`);
  process.stdout.write(body);
  return ExitCode.OK;
}





















export const AGENT_LIST_COLUMNS = [
  { header: 'agent', get: (i: IdentityView) => `@${i.name}` },
  { header: 'node', get: (i: IdentityView) => i.node_id ?? '—' },
  { header: 'bound', get: (i: IdentityView) => (i.bound ? 'yes' : 'no') },
];








export async function agents(args: ParsedArgs, client: Client): Promise<number> {
  const sub = args.positionals[1];

  if (sub === 'bind' || sub === 'unbind') {
    const name = args.positionals[2];
    if (!name) {
      throw usageError(`agents ${sub} needs a name`, 'For example: baton agents bind reviewer --node reviewer01');
    }
    const nodeID = sub === 'unbind' ? '' : (flagString(args, 'node') ?? '');
    if (sub === 'bind' && !nodeID) {
      throw usageError(
        'agents bind needs a node',
        'Give one with --node <name>, or use `baton agents unbind <name>` to detach the identity.',
      );
    }
    const res = await client.post<IdentityView>('/identities', { name: identityName(name), node_id: nodeID });
    process.stdout.write(
      res.bound ? `@${res.name} -> ${res.node_id}\n` : `@${res.name} is unbound; messages will queue.\n`,
    );
    return ExitCode.OK;
  }

  if (sub === 'show') return agentShow(args, client);
  if (sub === 'set') return agentSet(args, client);
  if (sub === 'set-avatar') return agentSetAvatar(args, client);

  if (sub && sub !== 'list') {
    throw usageError(
      `agents ${sub} is not a subcommand`,
      'Available: baton agents [list] | show <name> | set <name> --label <text> | set-avatar <name> | bind <name> --node <node> | unbind <name>',
    );
  }

  const res = await client.get<{ identities: IdentityView[] }>('/identities');
  const identities = res.identities ?? [];
  process.stdout.write(
    args.global.output === 'json'
      ? json(identities) + '\n'
      : table(
          identities,
          AGENT_LIST_COLUMNS,
          'no agents are registered.',
        ) + '\n',
  );
  return ExitCode.OK;
}







async function agentShow(args: ParsedArgs, client: Client): Promise<number> {
  const raw = args.positionals[2];
  if (!raw) throw usageError('show needs an agent', 'For example: baton agents show @bob');
  const name = identityName(raw);

  const ids = await client.get<{ identities: IdentityView[] }>('/identities');
  const id = (ids.identities ?? []).find((i) => i.name === name);
  if (!id) {
    throw usageError(`no agent called @${name} is known to this network`, 'See them with `baton agents`.');
  }
  interface NodeView {
    node_id?: string; status?: string; network?: string; host_address?: string; label?: string; labels?: Record<string, string>;
    enrolled_at?: string; last_seen_at?: string; last_seen_age_sec?: number; connected?: boolean;
    agent_version?: string; health?: string;
    workspace?: { template?: string; state?: string; image?: string } | null;
  }
  let node: NodeView | null = null;
  if (id.node_id) {
    try { node = await client.get<NodeView>(`/nodes/${encodeURIComponent(id.node_id)}`); } catch { node = null; }
  }



  let cloudAddress: string | null = null;
  try {
    const self = await client.get<{ cloud_address?: string }>('/networks/self');
    if (self.cloud_address && self.cloud_address.includes('@')) {
      const [netName, domain] = self.cloud_address.split('@');
      cloudAddress = `${netName}.${name}@${domain}`;
    }
  } catch {  }


  let avatar: unknown = null, avatarThumb: unknown = null;
  if (id.node_id) {
    try {
      const av = await client.get<{ avatar?: unknown; avatar_thumb?: unknown }>(`/nodes/${encodeURIComponent(id.node_id)}/avatar`);
      avatar = av.avatar ?? null; avatarThumb = av.avatar_thumb ?? null;
    } catch {  }
  }
  const msgs = await client.get<{ messages: MessageEnvelope[] }>(`/messages?to=${encodeURIComponent(name)}`).catch(() => ({ messages: [] as MessageEnvelope[] }));
  const counts: Record<string, number> = {};
  for (const m of msgs.messages ?? []) counts[m.state] = (counts[m.state] ?? 0) + 1;
  const latest = (msgs.messages ?? []).map((m) => m.created_at).sort().pop() ?? null;

  const out = {
    agent: name,
    bound: id.bound,
    node_id: id.node_id ?? null,
    node_status: node?.status ?? null,
    network: node?.network ?? null,
    host_address: node?.host_address || null,
    cloud_address: cloudAddress,
    avatar,
    avatar_thumb: avatarThumb,
    label: node?.label || null,
    labels: node?.labels ?? null,
    template: node?.workspace?.template ?? null,
    workspace_state: node?.workspace?.state ?? null,
    enrolled_at: node?.enrolled_at ?? null,
    last_seen_at: node?.last_seen_at ?? null,
    connected: node?.connected ?? null,
    health: node?.health ?? null,
    agent_version: node?.agent_version ?? null,
    messages: { total: (msgs.messages ?? []).length, by_state: counts, latest_at: latest },
  };
  if (args.global.output === 'json') {
    process.stdout.write(json(out) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    `@${name}\n` +
      `  node      ${out.node_id ?? 'unbound'}${out.node_status ? ` (${out.node_status})` : ''}\n` +
      `  network   ${out.network ?? '-'}\n` +
      `  cloud     ${out.cloud_address ?? '-'}\n` +
      `  address   ${out.host_address ?? '-'}\n` +
      `  label     ${out.label ?? '-'}\n` +
      `  template  ${out.template ?? '-'}\n` +
      `  enrolled  ${out.enrolled_at ?? '-'}\n` +
      `  last seen ${out.last_seen_at ?? '-'}\n` +
      `  health    ${out.health ?? 'unknown'}\n` +
      `  messages  ${out.messages.total} (${Object.entries(counts).map(([k, v]) => `${k} ${v}`).join(', ') || 'none'})\n`,
  );
  return ExitCode.OK;
}









async function agentSet(args: ParsedArgs, client: Client): Promise<number> {
  const raw = args.positionals[2];
  if (!raw) throw usageError('set needs an agent', 'For example: baton agents set @bob --label "infra on-call"');
  const name = identityName(raw);
  const label = flagString(args, 'label');
  if (label === undefined) {
    throw usageError(
      'set needs --label <text> (the only editable field)',
      "The agent's name is an address and is not edited here; its driver/host labels are set at enrolment. " +
        'Give --label "some text", or --label "" to clear it.',
    );
  }
  const ids = await client.get<{ identities: IdentityView[] }>('/identities');
  const id = (ids.identities ?? []).find((i) => i.name === name);
  if (!id || !id.node_id) {
    throw usageError(
      `@${name} is not bound to a node, so it has no settings to change`,
      'A label lives on the node the agent occupies. See `baton agents`.',
    );
  }
  const res = await client.post<{ label: string }>(`/nodes/${encodeURIComponent(id.node_id)}/label`, { label });
  if (args.global.output === 'json') {
    process.stdout.write(json(res) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(res.label ? `@${name} label set to: ${res.label}\n` : `@${name} label cleared\n`);
  return ExitCode.OK;
}








async function agentSetAvatar(args: ParsedArgs, client: Client): Promise<number> {
  const raw = args.positionals[2];
  if (!raw) throw usageError('set-avatar needs an agent', 'For example: baton agents set-avatar @bob < logo.png');
  const name = identityName(raw);
  const ids = await client.get<{ identities: IdentityView[] }>('/identities');
  const id = (ids.identities ?? []).find((i) => i.name === name);
  if (!id || !id.node_id) throw usageError(`@${name} is not bound to a node, so it has no avatar to set`, 'See `baton agents`.');
  if (flagBool(args, 'clear')) {
    await client.post(`/nodes/${encodeURIComponent(id.node_id)}/avatar`, { content_type: '', full_base64: '', thumb_base64: '' });
    process.stdout.write(`@${name} avatar cleared.\n`);
    return ExitCode.OK;
  }
  const input = readFileSync(0);
  if (!input.length) throw usageError('set-avatar reads the image (or {content_type, full_base64, thumb_base64}) from stdin', 'Pipe a png/jpeg/webp, or --clear.');
  let payload: { content_type: string; full_base64: string; thumb_base64: string };
  const first = input[0];
  if (first === 0x7b ) {

    const doc = JSON.parse(input.toString('utf8')) as { content_type?: string; full_base64?: string; thumb_base64?: string };
    if (!doc.content_type || !doc.full_base64) throw usageError('the JSON body needs content_type and full_base64', 'Send {content_type, full_base64, thumb_base64}.');
    payload = { content_type: doc.content_type, full_base64: doc.full_base64, thumb_base64: doc.thumb_base64 ?? '' };
  } else {

    const ct = sniffImageType(input);
    if (!ct) throw usageError('the image is not a png, jpeg or webp', 'SVG is not accepted.');
    payload = { content_type: ct, full_base64: input.toString('base64'), thumb_base64: '' };
  }
  const res = await client.post<{ full_bytes?: number }>(`/nodes/${encodeURIComponent(id.node_id)}/avatar`, payload);
  process.stdout.write(args.global.output === 'json' ? json(res) + '\n' : `@${name} avatar set.\n`);
  return ExitCode.OK;
}

function sniffImageType(b: Buffer): string {
  if (b.length >= 8 && b[0] === 0x89 && b[1] === 0x50 && b[2] === 0x4e && b[3] === 0x47) return 'image/png';
  if (b.length >= 3 && b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) return 'image/jpeg';
  if (b.length >= 12 && b.toString('ascii', 0, 4) === 'RIFF' && b.toString('ascii', 8, 12) === 'WEBP') return 'image/webp';
  return '';
}
