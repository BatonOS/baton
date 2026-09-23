#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0




































'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { execFile } = require('node:child_process');

const PLUGIN_ID = 'baton.slack';
const MESSAGE_TYPE = 'channel/message';
const POST_TYPE = 'channel/post';
const SLACK_TEXT_LIMIT = 40000;
const HEARTBEAT_EVERY_MS = 20_000;
const HEARTBEAT_STALE_MS = 90_000;
const NEXT_WAIT = '10s';
const BACKOFF_MAX_MS = 30_000;
const USER_CACHE_MS = 24 * 3600 * 1000;





const CONTEXT_KEEP = 20;
const CONTEXT_MAX_AGE_MS = 24 * 3600 * 1000;



const CONTEXT_THREAD_LIMIT = 200;
const CONTEXT_MAX_BYTES = 200 * 1024;




function log(event, fields) {
  const parts = [`[${PLUGIN_ID}] ${event}`];
  for (const [k, v] of Object.entries(fields || {})) parts.push(`${k}=${JSON.stringify(v)}`);
  process.stderr.write(parts.join(' ') + '\n');
}


function writeAtomic(file, data) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const tmp = `${file}.tmp-${process.pid}`;
  fs.writeFileSync(tmp, data);
  fs.renameSync(tmp, file);
}
function readJSON(file) {
  try { return JSON.parse(fs.readFileSync(file, 'utf8')); } catch { return null; }
}
function exists(file) { try { fs.statSync(file); return true; } catch { return false; } }


function paths() {
  const home = process.env.HOME;
  if (!home) throw new Error('HOME is not set: a facility service is given one by the daemon');



  const ws = process.env.BATON_WORKSPACE || null;
  return {
    home,
    parcel: ws ? path.join(ws, '.baton', 'portable', 'ext', PLUGIN_ID, 'parcel.yaml') : null,
    threads: path.join(home, 'threads'),
    seen: path.join(home, 'seen'),
    users: path.join(home, 'users'),
    context: path.join(home, 'context'),
    state: path.join(home, 'state'),
    heartbeat: path.join(home, 'state', 'heartbeat'),
    counters: path.join(home, 'state', 'counters.json'),
  };
}








function readParcel(file) {
  const cfg = { api_base: 'https://slack.com/api' };
  if (!file || !exists(file)) return cfg;
  const text = fs.readFileSync(file, 'utf8');
  for (const raw of text.split('\n')) {
    const line = raw.replace(/\s+$/, '');
    if (line === '' || line.startsWith('#')) continue;
    if (/^\s/.test(line)) throw new Error(`${file}: indented line — this plugin's config is a flat mapping of scalars, nesting is not read`);
    const m = /^([a-z_]+):\s*(.*)$/.exec(line);
    if (!m) throw new Error(`${file}: cannot read line ${JSON.stringify(line)} — expected key: value`);
    const [, key, value] = m;
    if (key === 'agent') {
      throw new Error(`${file}: \`agent\` is gone — this relay runs inside the office it serves and delivers to that office's own identity; remove the key`);
    }
    if (key === 'api_base') {
      let v = value.trim();
      if (v === '' || v === '""' || v === "''" || v === 'null' || v === '~') v = '';
      if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) v = v.slice(1, -1);
      if (v !== '') cfg[key] = v;
    } else {
      throw new Error(`${file}: unknown key ${JSON.stringify(key)} — this plugin reads api_base only`);
    }
  }
  return cfg;
}





function readSecret(name) {
  const file = process.env[`${name}_FILE`];
  if (!file) return { value: null, why: `${name}_FILE is not set (service.secrets in the manifest)` };
  try {
    const v = fs.readFileSync(file, 'utf8').trim();
    if (v === '') return { value: null, why: `${file} is empty — put the ${name} in it` };
    return { value: v, why: null };
  } catch (e) {
    return { value: null, why: `${file}: ${e.code || e.message}` };
  }
}


class Readout {
  constructor(p) {
    this.p = p;
    this.status = null;
    this.counters = Object.assign({
      opens: 0, received: 0, delivered: 0, duplicates: 0,
      dropped_unaddressed: 0, dropped_self: 0, dropped_other: 0,
      posted: 0, post_failed: 0, refused_outbound: 0, refused_inbound: 0,
    }, readJSON(p.counters) || {});
    this.flushTimer = null;
  }






  setStatus(word) {
    if (word === this.status) return;
    this.status = word;
    log('status', { relay_status: word });
  }
  bump(name, by = 1) {
    this.counters[name] = (this.counters[name] || 0) + by;
    this.scheduleFlush();
  }
  set(name, value) {
    if (this.counters[name] === value) return;
    this.counters[name] = value;
    this.scheduleFlush();
  }
  scheduleFlush() {
    if (this.flushTimer) return;
    this.flushTimer = setTimeout(() => { this.flushTimer = null; this.flush(); }, 200);
  }
  flush() {
    try { writeAtomic(this.p.counters, JSON.stringify(this.counters, null, 2) + '\n'); } catch (e) { log('counters.write_failed', { error: e.code || e.message }); }
  }
  heartbeat() {
    try {
      fs.mkdirSync(this.p.state, { recursive: true });
      const now = new Date();
      if (exists(this.p.heartbeat)) fs.utimesSync(this.p.heartbeat, now, now);
      else fs.writeFileSync(this.p.heartbeat, '');
    } catch (e) { log('heartbeat.write_failed', { error: e.code || e.message }); }
  }
}


function run(cmd, args, input) {
  return new Promise((resolve) => {
    const child = execFile(cmd, args, { maxBuffer: 4 * 1024 * 1024 }, (err, stdout, stderr) => {
      resolve({ code: err ? (typeof err.code === 'number' ? err.code : 1) : 0, stdout, stderr, spawnError: err && typeof err.code === 'string' ? err.code : null });
    });
    if (input != null) { child.stdin.end(input); } else { child.stdin.end(); }
  });
}

class Mailbox {
  constructor() { this.outboxDir = null; this.sentDir = null; this.identity = null; }




  async learn() {
    const r = await run('baton-inbox', ['contract']);
    if (r.code !== 0) throw new Error(`baton-inbox contract exited ${r.code}${r.spawnError ? ' (' + r.spawnError + ')' : ''}`);
    const c = JSON.parse(r.stdout);
    if (!c.outbox_dir) throw new Error('baton-inbox contract has no outbox_dir');




    if (!c.identity) throw new Error(`baton-inbox contract gives no identity to deliver to (${c.identity_source || 'no reason given'})`);
    this.outboxDir = c.outbox_dir;
    this.sentDir = path.join(c.outbox_dir, 'sent');
    this.identity = c.identity;
    return c;
  }
  async send(to, type, payload, replyTo) {
    const args = ['send', '--to', to, '--type', type, '--file', '-', '--json'];
    if (replyTo) args.push('--reply-to', replyTo);
    const r = await run('baton-inbox', args, payload);
    if (r.code !== 0) return { ok: false, code: r.code, stderr: (r.stderr || '').split('\n')[0] };
    return { ok: true, local_id: JSON.parse(r.stdout).local_id };
  }


  async next() {
    const r = await run('baton-inbox', ['next', '--wait', NEXT_WAIT, '--json']);
    if (r.code === 4) return { empty: true };
    if (r.code !== 0) return { error: `baton-inbox next exited ${r.code}: ${(r.stderr || '').split('\n')[0]}` };
    return { message: JSON.parse(r.stdout) };
  }



  localIdFor(messageId) {
    if (!this.sentDir || !messageId) return null;
    let names;
    try { names = fs.readdirSync(this.sentDir); } catch { return null; }
    const suffix = `.${messageId}.json`;
    const hit = names.find((n) => n.endsWith(suffix));
    return hit ? hit.slice(0, hit.length - suffix.length) : null;
  }
  refusedCount() {
    if (!this.sentDir) return 0;
    try { return fs.readdirSync(this.sentDir).filter((n) => n.endsWith('.refused')).length; } catch { return 0; }
  }
}


class Slack {
  constructor(apiBase) { this.apiBase = apiBase.replace(/\/+$/, ''); }


  async call(method, token, body) {
    for (let attempt = 0; ; attempt++) {
      let res;
      try {
        res = await fetch(`${this.apiBase}/${method}`, {
          method: 'POST',
          headers: { 'authorization': `Bearer ${token}`, 'content-type': 'application/json; charset=utf-8' },
          body: JSON.stringify(body || {}),
        });
      } catch (e) {
        return { ok: false, error: `transport: ${e.code || (e.cause && e.cause.code) || e.message}` };
      }
      if (res.status === 429 && attempt < 3) {
        const wait = Number(res.headers.get('retry-after') || '1');
        await sleep(Math.min(30, Math.max(1, wait)) * 1000);
        continue;
      }
      let data;
      try { data = await res.json(); } catch { return { ok: false, error: `http_${res.status}_not_json` }; }
      if (typeof data !== 'object' || data === null) return { ok: false, error: `http_${res.status}_not_object` };
      if (!('ok' in data)) data.ok = res.ok;
      return data;
    }
  }
}

function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }
function truncate(text) {
  if (text.length <= SLACK_TEXT_LIMIT) return text;
  const mark = `\n… [truncated by ${PLUGIN_ID}: ${text.length} chars]`;
  return text.slice(0, SLACK_TEXT_LIMIT - mark.length) + mark;
}


class Adapter {
  constructor() {
    this.p = paths();
    this.readout = new Readout(this.p);
    this.mail = new Mailbox();
    this.cfg = null;
    this.slack = null;
    this.botToken = null;
    this.appToken = null;
    this.botUserId = null;
    this.teamId = null;
    this.ws = null;
    this.backoff = 0;
    this.stopping = false;
    this.connecting = false;
    this.heartbeatTimer = null;
    this.reconnectTimer = null;





    this.inbound = Promise.resolve();
  }

  async start() {
    for (const d of [this.p.threads, this.p.seen, this.p.users, this.p.context, this.p.state]) fs.mkdirSync(d, { recursive: true });
    this.readout.flush();
    process.on('SIGTERM', () => this.stop('SIGTERM'));
    process.on('SIGINT', () => this.stop('SIGINT'));
    this.connect();
    this.outboundLoop();
  }

  stop(why) {
    if (this.stopping) return;
    this.stopping = true;
    log('stopping', { why });
    if (this.heartbeatTimer) clearInterval(this.heartbeatTimer);
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    try { if (this.ws) this.ws.close(1000, 'shutting down'); } catch {  }
    this.readout.flush();
    setTimeout(() => process.exit(0), 300).unref();
  }






  async preflight() {
    try { this.cfg = readParcel(this.p.parcel); } catch (e) {
      this.readout.setStatus('needs-input'); log('config.unreadable', { error: e.message }); return false;
    }
    const bot = readSecret('SLACK_BOT_TOKEN'), app = readSecret('SLACK_APP_TOKEN');
    if (!bot.value || !app.value) {
      this.readout.setStatus('needs-input');
      log('token.missing', { bot: bot.why || 'present', app: app.why || 'present' });
      return false;
    }
    this.botToken = bot.value; this.appToken = app.value;
    this.slack = new Slack(this.cfg.api_base);
    if (!this.mail.identity) {
      try { const c = await this.mail.learn(); log('contract', { identity: c.identity, outbox_dir: c.outbox_dir }); }
      catch (e) { this.readout.setStatus('needs-input'); log('contract.failed', { error: e.message }); return false; }
    }
    return true;
  }

  scheduleReconnect(why) {
    if (this.stopping || this.reconnectTimer) return;
    const base = Math.min(BACKOFF_MAX_MS, 1000 * 2 ** this.backoff);
    const wait = base + Math.floor(Math.random() * 500);
    this.backoff = Math.min(this.backoff + 1, 5);
    log('reconnect.scheduled', { why, in_ms: wait });
    this.reconnectTimer = setTimeout(() => { this.reconnectTimer = null; this.connect(); }, wait);
  }

  retryLater(ms, why) {
    if (this.stopping || this.reconnectTimer) return;
    this.reconnectTimer = setTimeout(() => { this.reconnectTimer = null; this.connect(); }, ms);
    log('retry.scheduled', { why, in_ms: ms });
  }

  async connect() {
    if (this.stopping || this.connecting) return;
    this.connecting = true;
    try {
      if (!(await this.preflight())) { this.connecting = false; this.retryLater(30_000, 'preflight'); return; }
      const auth = await this.slack.call('auth.test', this.botToken, {});
      if (!auth.ok) {
        this.readout.setStatus(auth.error === 'invalid_auth' || auth.error === 'not_authed' ? 'needs-input' : 'errored');
        log('auth.test.failed', { error: auth.error });
        this.connecting = false; this.scheduleReconnect('auth.test'); return;
      }
      this.botUserId = auth.user_id; this.teamId = auth.team_id;
      const open = await this.slack.call('apps.connections.open', this.appToken, {});
      if (!open.ok || !open.url) {
        this.readout.setStatus(open.error === 'invalid_auth' || open.error === 'not_authed' ? 'needs-input' : 'errored');
        log('connections.open.failed', { error: open.error || 'no url' });
        this.connecting = false; this.scheduleReconnect('connections.open'); return;
      }
      this.openSocket(open.url);
    } catch (e) {
      this.readout.setStatus('errored'); log('connect.failed', { error: e.code || e.message });
      this.connecting = false; this.scheduleReconnect('exception');
    }
  }

  openSocket(url) {
    const ws = new WebSocket(url);
    const previous = this.ws;
    ws.addEventListener('open', () => { log('socket.open', {}); });





    ws.addEventListener('message', (ev) => {
      const d = ev.data;
      let text;
      if (typeof d === 'string') text = d;
      else if (d && typeof d.byteLength === 'number') text = Buffer.from(d).toString('utf8');
      else if (d == null) {













        process.stderr.write(
          'baton.slack: a frame arrived with data=' + (d === null ? 'null' : 'undefined') + '.\n' +
          '  This connection is unusable: the frame cannot be read, so it is never acked, and Slack\n' +
          '  redelivers it forever — the relay stays up and nothing arrives. Exiting instead.\n' +
          '  Cause, in every case seen so far: the WebSocket implementation in this runtime does not\n' +
          '  decompress permessage-deflate, which Slack Socket Mode negotiates. Node 20 with\n' +
          '  --experimental-websocket does this; node 22 and later do not. That version number is\n' +
          '  here to help you, not to decide anything — this check reads the frame, never the runtime.\n' +
          '  Fix: give this office a runtime whose WebSocket handles compressed frames.\n',
        );
        log('frame.empty.fatal', { data: d === null ? 'null' : 'undefined' });
        process.exit(3);
      }
      else text = String(d);
      this.onFrame(ws, text).catch((e) => log('frame.failed', { error: e.message }));
    });
    ws.addEventListener('close', (ev) => {
      if (this.ws !== ws) { log('socket.closed.superseded', { code: ev.code }); return; }
      this.ws = null; this.stopHeartbeat();
      if (!this.stopping) { this.readout.setStatus('errored'); log('socket.closed', { code: ev.code, reason: ev.reason || '' }); this.scheduleReconnect('close'); }
    });
    ws.addEventListener('error', () => {

      if (this.ws === ws || this.ws === null) log('socket.error', {});
    });
    this.ws = ws;
    this.connecting = false;


    if (previous && previous !== ws) { try { previous.close(1000, 'refreshed'); } catch {  } }
  }

  startHeartbeat() {
    this.readout.heartbeat();
    if (this.heartbeatTimer) return;
    this.heartbeatTimer = setInterval(() => this.readout.heartbeat(), HEARTBEAT_EVERY_MS);
  }
  stopHeartbeat() {
    if (this.heartbeatTimer) { clearInterval(this.heartbeatTimer); this.heartbeatTimer = null; }
  }

  async onFrame(ws, text) {
    let env;
    try { env = JSON.parse(text); } catch { log('frame.not_json', {}); return; }


    if (!env || typeof env !== 'object') {
      log('frame.not_object', { got: env === null ? 'null' : typeof env, head: String(text).slice(0, 80) });
      return;
    }
    switch (env.type) {
      case 'hello':
        this.backoff = 0;
        this.readout.bump('opens');
        this.readout.setStatus('idle');
        this.startHeartbeat();
        log('hello', { app_id: env.connection_info && env.connection_info.app_id, opens: this.readout.counters.opens });
        return;
      case 'disconnect':
        log('disconnect.requested', { reason: env.reason });
        this.stopHeartbeat();
        if (!this.stopping) this.connect();
        return;
      case 'events_api': {


        const job = this.inbound.then(() => this.onEvent(env.payload || {}, env.retry_attempt || 0));
        this.inbound = job.catch(() => {});
        try { await job; }
        catch (e) { log('event.failed', { error: e.message }); }
        finally { if (env.envelope_id) { try { ws.send(JSON.stringify({ envelope_id: env.envelope_id })); } catch {  } } }
        return;
      }
      default:
        if (env.envelope_id) { try { ws.send(JSON.stringify({ envelope_id: env.envelope_id })); } catch {  } }
        log('frame.ignored', { type: env.type });
    }
  }


  async onEvent(payload, retryAttempt) {
    if (payload.type !== 'event_callback' || !payload.event) return;
    const ev = payload.event;
    if (ev.type !== 'message' && ev.type !== 'app_mention') return;
    this.readout.bump('received');
    this.readout.setStatus('working');
    let outcome = 'idle';
    try {
      if (ev.subtype || ev.bot_id) { this.readout.bump('dropped_other'); return; }
      if (ev.user && ev.user === this.botUserId) { this.readout.bump('dropped_self'); return; }
      const channel = ev.channel, ts = ev.ts;
      if (!channel || !ts) { this.readout.bump('dropped_other'); return; }



      const seenKey = path.join(this.p.seen, `${channel}_${ts}`);
      if (exists(seenKey)) { this.readout.bump('duplicates'); log('duplicate', { channel, ts, retry_attempt: retryAttempt }); return; }
      const text = typeof ev.text === 'string' ? ev.text : '';
      const isDM = ev.channel_type === 'im' || (typeof channel === 'string' && channel.startsWith('D'));
      const mention = ev.type === 'app_mention' || (this.botUserId ? text.includes(`<@${this.botUserId}>`) : false);
      const threadRoot = ev.thread_ts || null;
      const knownThread = threadRoot ? exists(path.join(this.p.threads, 'known', `${channel}_${threadRoot}`)) : false;


      if (!isDM) this.contextAppend(channel, threadRoot, { ts, user: ev.user || null, text, thread_ts: threadRoot });



      if (!isDM && !mention && !knownThread) {
        writeAtomic(seenKey, '');
        this.readout.bump('dropped_unaddressed');
        return;
      }
      const author = await this.userInfo(ev.user);
      const participants = [author];
      for (const m of text.matchAll(/<@([A-Z0-9]+)(?:\|[^>]*)?>/g)) {
        if (m[1] === this.botUserId || participants.some((x) => x.id === m[1])) continue;
        participants.push(await this.userInfo(m[1]));
      }
      const body = {
        origin: {
          provider: 'slack', team: this.teamId, channel, channel_kind: isDM ? 'dm' : 'channel',
          user: ev.user || null, user_name: author.name, ts, thread_ts: threadRoot, mention,
          relayed_by: PLUGIN_ID,
        },
        text,
        participants: participants.map((u) => ({ id: u.id, name: u.name, mention: `<@${u.id}>` })),


        context: isDM ? [] : await this.contextFor(channel, threadRoot, ts),
      };
      const sent = await this.mail.send(this.mail.identity, MESSAGE_TYPE, JSON.stringify(body), null);
      if (!sent.ok) {
        outcome = 'errored';
        log('send.failed', { channel, ts, exit: sent.code, first_line: sent.stderr });
        return;
      }
      writeAtomic(seenKey, '');
      writeAtomic(path.join(this.p.threads, `${sent.local_id}.json`), JSON.stringify({
        local_id: sent.local_id, channel, ts, thread_ts: threadRoot, channel_kind: isDM ? 'dm' : 'channel', user: ev.user || null, relayed_at: new Date().toISOString(),
      }, null, 2) + '\n');


      writeAtomic(path.join(this.p.threads, 'known', `${channel}_${threadRoot || ts}`), '');
      this.readout.bump('delivered');
      log('delivered', { channel, ts, mention, kind: body.origin.channel_kind, local_id: sent.local_id, to: this.mail.identity });
    } finally {
      if (this.ws) this.readout.setStatus(outcome);
    }
  }

  async userInfo(id) {
    if (!id) return { id: null, name: null };
    const cache = path.join(this.p.users, `${id}.json`);
    const c = readJSON(cache);
    if (c && Date.now() - Date.parse(c.fetched_at) < USER_CACHE_MS) return { id, name: c.name };
    const r = await this.slack.call('users.info', this.botToken, { user: id });
    let name = null;
    if (r.ok && r.user) name = r.user.name || (r.user.profile && r.user.profile.display_name) || null;
    else if (!c) log('users.info.failed', { user: id, error: r.error });
    writeAtomic(cache, JSON.stringify({ id, name, real_name: r.ok && r.user ? r.user.real_name || null : null, fetched_at: new Date().toISOString() }, null, 2) + '\n');
    return { id, name };
  }







  contextFile(channel, threadRoot) { return path.join(this.p.context, threadRoot ? `${channel}_${threadRoot}.jsonl` : `${channel}.jsonl`); }
  contextAppend(channel, threadRoot, entry) {
    const f = this.contextFile(channel, threadRoot);
    let lines = [];
    try { lines = fs.readFileSync(f, 'utf8').split('\n').filter(Boolean); } catch {  }


    lines.push(JSON.stringify(Object.assign({ heard_at: Date.now() }, entry)));
    if (lines.length > CONTEXT_KEEP) lines = lines.slice(lines.length - CONTEXT_KEEP);
    writeAtomic(f, lines.join('\n') + '\n');
  }
  contextReadLocal(channel, threadRoot, excludeTs) {
    const cutoff = Date.now() - CONTEXT_MAX_AGE_MS;
    const read = (f) => { try { return fs.readFileSync(f, 'utf8').split('\n').filter(Boolean).map((l) => JSON.parse(l)); } catch { return []; } };
    const out = read(this.contextFile(channel, null));
    if (threadRoot) out.push(...read(this.contextFile(channel, threadRoot)));
    return out.filter((e) => e.ts !== excludeTs && (e.heard_at || 0) >= cutoff)
      .sort((a, b) => Number(a.ts) - Number(b.ts))
      .map(({ heard_at, ...e }) => e);
  }


  async contextFor(channel, threadRoot, excludeTs) {
    let entries = null;
    if (threadRoot) {
      const r = await this.slack.call('conversations.replies', this.botToken, { channel, ts: threadRoot, limit: CONTEXT_THREAD_LIMIT });
      if (r.ok && Array.isArray(r.messages)) {
        entries = r.messages.filter((m) => !m.subtype).map((m) => ({ ts: m.ts, user: m.user || m.bot_id || null, text: typeof m.text === 'string' ? m.text : '', thread_ts: m.thread_ts && m.thread_ts !== m.ts ? m.thread_ts : null, self: m.user === this.botUserId || undefined }));

        entries = this.contextReadLocal(channel, null, null).filter((e) => Number(e.ts) < Number(threadRoot)).concat(entries);
      } else {
        log('context.thread_fetch_failed', { channel, thread_ts: threadRoot, error: r.error || 'no messages' });
      }
    }
    if (!entries) entries = this.contextReadLocal(channel, threadRoot, excludeTs);
    entries = entries.filter((e) => e.ts !== excludeTs);
    while (entries.length && JSON.stringify(entries).length > CONTEXT_MAX_BYTES) entries.shift();
    return entries;
  }





  async outboundLoop() {
    while (!this.stopping) {
      if (!this.mail.sentDir) { await sleep(2000); continue; }
      this.readout.set('refused_inbound', this.mail.refusedCount());
      const r = await this.mail.next();
      if (r.empty) continue;
      if (r.error) { log('next.failed', { error: r.error }); await sleep(5000); continue; }
      const m = r.message;
      try { await this.post(m); } catch (e) { this.readout.bump('post_failed'); log('post.exception', { message_id: m.message_id, error: e.message }); }
    }
  }

  async post(m) {
    const facts = { message_id: m.message_id, sender: m.sender, type: m.type || '', reply_to: m.reply_to || '', thread_id: m.thread_id || '', bytes: m.payload_size };
    const payload = m.payload ? Buffer.from(m.payload, 'base64').toString('utf8') : '';
    if (!this.slack || !this.botToken) { this.readout.bump('post_failed'); log('post.no_connection', facts); return; }
    let target = null;
    if (m.reply_to || m.thread_id) {
      const local = this.mail.localIdFor(m.reply_to) || this.mail.localIdFor(m.thread_id);
      const t = local ? readJSON(path.join(this.p.threads, `${local}.json`)) : null;









      if (t) target = { channel: t.channel, thread_ts: t.channel_kind === 'dm' ? undefined : (t.thread_ts || t.ts), text: truncate(payload) };
    }
    if (!target && m.type === POST_TYPE) {
      let p = null;
      try { p = JSON.parse(payload); } catch {  }
      if (p && typeof p.channel === 'string' && typeof p.text === 'string') target = { channel: p.channel, text: truncate(p.text) };
    }
    if (!target) { this.readout.bump('refused_outbound'); log('post.refused', Object.assign({ why: m.reply_to || m.thread_id ? 'reply to a message this node did not relay' : 'neither a reply nor a channel/post' }, facts)); return; }
    this.readout.setStatus('working');
    const r = await this.slack.call('chat.postMessage', this.botToken, target);
    if (r.ok) {
      this.readout.bump('posted'); log('posted', Object.assign({ channel: target.channel, thread_ts: target.thread_ts || '', slack_ts: r.ts }, facts));
      if (!String(target.channel).startsWith('D')) this.contextAppend(target.channel, target.thread_ts || null, { ts: r.ts, user: this.botUserId, text: target.text, thread_ts: target.thread_ts || null, self: true });






      if (!target.thread_ts && !String(target.channel).startsWith('D')) writeAtomic(path.join(this.p.threads, 'known', `${target.channel}_${r.ts}`), '');
    }
    else { this.readout.bump('post_failed'); log('post.failed', Object.assign({ channel: target.channel, error: r.error }, facts)); }
    if (this.ws) this.readout.setStatus('idle');
    if (m.attachments && m.attachments.length) log('attachments.ignored', { message_id: m.message_id, count: m.attachments.length });
  }
}






function probe(file) {
  try {
    const age = Date.now() - fs.statSync(file).mtimeMs;
    if (age <= HEARTBEAT_STALE_MS) return 0;
    process.stderr.write(`${PLUGIN_ID}: heartbeat is ${Math.round(age / 1000)}s old\n`);
    return 1;
  } catch (e) {
    process.stderr.write(`${PLUGIN_ID}: no heartbeat at ${file} (${e.code || e.message})\n`);
    return 1;
  }
}


const [, , verb = 'run', arg] = process.argv;
if (verb === 'probe') {
  const file = arg || (process.env.HOME ? path.join(process.env.HOME, 'state', 'heartbeat') : null);
  if (!file) { process.stderr.write('usage: baton-slack probe [heartbeat-file]  (or set HOME)\n'); process.exit(2); }
  process.exit(probe(file));
} else if (verb === 'run') {
  new Adapter().start().catch((e) => { log('fatal', { error: e.message }); process.exit(1); });
} else {
  process.stderr.write('usage: baton-slack [run | probe [heartbeat-file]]\n');
  process.exit(2);
}
