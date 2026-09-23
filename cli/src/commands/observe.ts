// SPDX-License-Identifier: Apache-2.0
















import { randomUUID } from 'node:crypto';
import { createInterface } from 'node:readline';
import { Client } from '../api/client.js';
import { BatonError, ExitCode, preconditionError } from '../errors.js';
import { json, table } from '../output.js';
import { componentsOf, resolveTarget, type ComponentRole, type Target } from '../runtime/target.js';
import {
  containerNames,
  execInteractive,
  execPlan,
  execSucceeds,
  execCapture,
  streamLogs,
} from '../runtime/engine.js';
import { flagBool, flagString, type ParsedArgs } from '../args.js';


const RUNTIME_SOCKET = 'runtime';
const RUNTIME_SESSION = 'runtime';

interface NodeRuntimeView {
  session?: string;
  state?: string;
  enterable?: boolean;
  name?: string;
}













export async function attach(args: ParsedArgs, newClient: () => Client): Promise<number> {
  const node = args.positionals[1];
  if (!node) {
    throw new BatonError({
      code: 'INVALID_ARGUMENT',
      message: 'attach needs a node name',
      remediation: 'Try `baton attach agent01`. `baton status` lists what is running.',
      exitCode: ExitCode.CONFIG,
    });
  }

  const target = resolveTarget(node, {
    role: flagString(args, 'role') as ComponentRole | undefined,
    component: flagString(args, 'component'),
  });
  const takeover = flagBool(args, 'takeover');

  if (args.global.dryRun) {
    const plan = {
      node: target.node,
      container: target.container,
      role: target.role,
      session: RUNTIME_SESSION,
      mode: takeover ? 'interactive (exclusive, audited)' : 'read-only',
      command: execPlan(target.container, attachCommand(takeover), { tty: true }),
    };
    process.stdout.write(
      args.global.output === 'json' ? json(plan) + '\n' : describePlan(plan) + '\n',
    );
    return ExitCode.OK;
  }



  if (!sessionExists(target.container)) {
    throw new BatonError({
      code: 'NO_SESSION',
      message: `${node} has no runtime terminal to attach to`,
      remediation:
        `Read its output with \`baton logs ${node} -f\`. A runtime only has a terminal ` +
        'when its image declares session: tty — a headless runtime writes to the log ' +
        'instead. If this node should be enterable, check `baton node show ' +
        `${node}\` for its runtime session mode.`,
      exitCode: ExitCode.UNSUPPORTED,
    });
  }

  if (!takeover) {












    process.stderr.write(
      `\x1b[36m== ${node} — read-only. Keystrokes are not delivered, including tmux's.\n` +
        `   Ctrl-C leaves; the runtime keeps working.\n` +
        `   To take control instead: Ctrl-C, then baton attach ${node} --takeover ==\x1b[0m\n`,
    );
    return runAttach(target, false);
  }

  return takeoverSession(args, newClient, target, node);
}








async function takeoverSession(
  args: ParsedArgs,
  newClient: () => Client,
  target: Target,
  node: string,
): Promise<number> {
  const client = newClient();
  const sessionID = randomUUID();
  const force = flagBool(args, 'force');

  let lease: { lock: { renew_within_sec: number; holder: string } };
  try {
    lease = await client.post(`/nodes/${node}/takeover/acquire`, {
      session_id: sessionID,
      force,
      reason: flagString(args, 'reason') ?? '',
    });
  } catch (err) {


    throw err;
  }

  const renewMs = Math.max(5, lease.lock.renew_within_sec) * 1000;
  const renewer = setInterval(() => {
    client.post(`/nodes/${node}/takeover/renew`, { session_id: sessionID }).catch((err) => {
      process.stderr.write(
        `\nwarning: could not renew the takeover lease: ${
          err instanceof Error ? err.message : String(err)
        }\n` + 'Someone else may take control. Finish up and re-attach.\n',
      );
    });
  }, renewMs);
  renewer.unref?.();

  process.stderr.write(
    `\x1b[33m== ${node} — you have control. Others are locked out while you are here.\n` +
      '   Ctrl-b d detaches; the runtime keeps working. ==\x1b[0m\n',
  );

  try {
    return await runAttach(target, true);
  } finally {
    clearInterval(renewer);


    await client
      .post(`/nodes/${node}/takeover/release`, { session_id: sessionID })
      .catch(() => undefined);
  }
}


function attachCommand(takeover: boolean): string[] {
  const argv = ['tmux', '-L', RUNTIME_SOCKET, 'attach-session', '-t', RUNTIME_SESSION];



  if (!takeover) argv.push('-r');
  return argv;
}

function runAttach(target: Target, takeover: boolean): Promise<number> {
  return execInteractive(target.container, attachCommand(takeover), { tty: true });
}


function sessionExists(container: string): boolean {
  return (
    execSucceeds(container, ['tmux', '-L', RUNTIME_SOCKET, 'has-session', '-t', RUNTIME_SESSION])
  );
}






function describePlan(plan: Record<string, unknown>): string {
  return [
    `node      ${plan.node}`,
    `session   ${plan.session}`,
    `mode      ${plan.mode}`,
    `runs      ${(plan.command as string[]).join(' ')}`,
  ].join('\n');
}









export async function shell(args: ParsedArgs, client: Client): Promise<number> {
  const node = args.positionals[1];
  if (!node) {
    throw new BatonError({
      code: 'INVALID_ARGUMENT',
      message: 'shell needs a node name',
      remediation: 'Try `baton shell agent01`.',
      exitCode: ExitCode.CONFIG,
    });
  }

  const target = resolveTarget(node, {
    role: flagString(args, 'role') as ComponentRole | undefined,
    component: flagString(args, 'component'),
  });

  if (args.global.dryRun) {
    process.stdout.write(
      json({
        container: target.container,
        command: execPlan(target.container, ['/bin/sh'], { tty: true }),
        audited: true,
        risk: 'high',
      }) + '\n',
    );
    return ExitCode.OK;
  }

  const reason = flagString(args, 'reason') ?? '';


  try {
    await client.post(`/nodes/${target.node}/console`, {
      command: 'status',
      reason: `shell session opened${reason ? `: ${reason}` : ''}`,
    });
  } catch (err) {
    if (!args.global.yes) {
      throw new BatonError({
        code: 'AUDIT_UNAVAILABLE',
        message: 'the audit trail is unreachable, so this shell was not opened',
        remediation:
          'Fix the control plane first, or pass --yes to open an unaudited shell ' +
          'and record it yourself. An unlogged privileged session is a gap in the trail.',
        exitCode: ExitCode.PRECONDITION,
        cause: err,
      });
    }
    process.stderr.write(
      'warning: opening an UNAUDITED shell; the control plane could not record it.\n',
    );
  }

  if (!execSucceeds(target.container, ['test', '-x', '/bin/sh'])) {


    throw preconditionError(
      `${target.node} has no shell (its image is the distroless variant)`,
      'Use `baton logs` to read output, or `baton console` for audited management commands.',
    );
  }

  process.stderr.write(
    `\x1b[33mopening an audited admin shell on ${target.node}\x1b[0m\n`,
  );
  return execInteractive(target.container, ['/bin/sh'], { tty: true });
}

interface ConsoleResponse {
  result: string;
  output?: string;
  data?: Record<string, unknown>;
  audit_event_id: string;
  duration_ms: number;
}









export async function consoleCmd(args: ParsedArgs, client: Client): Promise<number> {
  const node = args.positionals[1];
  if (!node) {
    throw new BatonError({
      code: 'INVALID_ARGUMENT',
      message: 'console needs a node name',
      remediation: 'Try `baton console agent01`.',
      exitCode: ExitCode.CONFIG,
    });
  }

  const once = flagString(args, 'command');
  const reason = flagString(args, 'reason') ?? '';

  if (once) {
    const res = await client.post<ConsoleResponse>(`/nodes/${node}/console`, {
      command: once,
      args: consoleArgs(args),
      reason,
    });
    process.stdout.write(
      args.global.output === 'json' ? json(res) + '\n' : formatConsole(res) + '\n',
    );
    return res.result === 'ok' ? ExitCode.OK : ExitCode.PARTIAL;
  }

  return repl(node, client);
}












function consoleArgs(args: ParsedArgs): Record<string, string> {
  const out: Record<string, string> = {};
  for (const pair of args.repeated.get('arg') ?? []) {
    const eq = pair.indexOf('=');
    if (eq <= 0) {
      throw new BatonError({
        code: 'INVALID_ARGUMENT',
        message: `--arg needs key=value, got \`${pair}\``,
        remediation: 'Try `--arg skill=e2e-review`.',
        exitCode: ExitCode.CONFIG,
      });
    }
    out[pair.slice(0, eq)] = pair.slice(eq + 1);
  }
  return out;
}

function formatConsole(res: ConsoleResponse): string {
  const lines = [`${res.result}${res.output ? `: ${res.output}` : ''}`];
  if (res.data && Object.keys(res.data).length > 0) {
    for (const [k, v] of Object.entries(res.data)) {
      lines.push(`  ${k}: ${Array.isArray(v) ? v.join(', ') : String(v)}`);
    }
  }
  lines.push(`  audit: ${res.audit_event_id} (${res.duration_ms}ms)`);
  return lines.join('\n');
}







const HELP = `commands: status, capabilities, tasks, skills.show, pause, resume, restart,
          runtime.pause, runtime.resume, runtime.restart
          help, exit

skills.show takes an argument: --arg skill=<name> (or \`skills.show <name>\` here).

Every command is a separate API call and a separate audit record.
This console cannot run arbitrary commands — that is \`baton shell\`.`;

async function repl(node: string, client: Client): Promise<number> {
  process.stdout.write(
    `BATON console — ${node}\nEvery command is audited. Type help for the list, exit to leave.\n\n`,
  );

  const rl = createInterface({ input: process.stdin, output: process.stdout, prompt: `${node}> ` });
  rl.prompt();

  return new Promise((resolve) => {
    rl.on('line', (line) => {
      const command = line.trim();
      if (!command) return rl.prompt();

      if (command === 'exit' || command === 'quit') {
        rl.close();
        return;
      }
      if (command === 'help' || command === '?') {
        process.stdout.write(HELP + '\n');
        return rl.prompt();
      }





      const [verb, ...rest] = command.split(/\s+/);
      const body: { command: string; args?: Record<string, string>; reason: string } = {
        command: verb as string,
        reason: 'interactive console',
      };
      if (verb === 'skills.show' && rest.length > 0) body.args = { skill: rest.join(' ') };

      client
        .post<ConsoleResponse>(`/nodes/${node}/console`, body)
        .then((res) => process.stdout.write(formatConsole(res) + '\n'))
        .catch((err: unknown) => {
          process.stdout.write(
            err instanceof BatonError ? `${err.code}: ${err.message}\n` : `${String(err)}\n`,
          );
        })
        .finally(() => rl.prompt());
    });

    rl.on('close', () => {
      process.stdout.write('\n');
      resolve(ExitCode.OK);
    });
  });
}


export async function logs(args: ParsedArgs): Promise<number> {
  const node = args.positionals[1];
  if (!node) {
    throw new BatonError({
      code: 'INVALID_ARGUMENT',
      message: 'logs needs a node name',
      remediation: 'Try `baton logs agent01`.',
      exitCode: ExitCode.CONFIG,
    });
  }
  const target = resolveTarget(node, {
    role: flagString(args, 'role') as ComponentRole | undefined,
    component: flagString(args, 'component'),
  });

  const tail = flagString(args, 'tail') ?? '200';
  const follow = flagBool(args, 'follow');

  if (!follow) return streamLogs(target.container, { tail, follow: false });








  for (;;) {
    const code = await follows(target.container, tail);
    if (code === 0 || code === 130) return code;

    process.stderr.write(
      `\n\x1b[33m-- ${target.node}: log stream ended; reconnecting in 2s ` +
        '(Ctrl-C to stop) --\x1b[0m\n',
    );
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }
}

function follows(container: string, tail: string): Promise<number> {
  return streamLogs(container, { tail, follow: true });
}

interface SessionRow {
  node: string;
  role: string;
  container: string;
  session: string;
  watchers: number;
}








export async function sessions(args: ParsedArgs): Promise<number> {
  if (flagBool(args, 'prune')) {
    process.stdout.write(
      'nothing to prune: runtime terminals live inside their agent-nodes and end ' +
        'with them.\nTo end one, stop or restart the node.\n',
    );
    return ExitCode.OK;
  }

  const rows: SessionRow[] = [];
  for (const container of runningAgentContainers()) {
    const watchers = sessionWatchers(container);
    if (watchers === null) continue;
    const node = container.replace(/^baton-agent-/, '');
    rows.push({ node, role: 'agent', container, session: RUNTIME_SESSION, watchers });
  }

  if (args.global.output === 'json') {
    process.stdout.write(json(rows) + '\n');
    return ExitCode.OK;
  }
  process.stdout.write(
    table(
      rows,
      [
        { header: 'node', get: (s) => s.node },
        { header: 'session', get: (s) => s.session },
        { header: 'watchers', get: (s) => String(s.watchers), right: true },
      ],
      'no runtime terminals. A runtime has one when its image declares session: tty.',
    ) + '\n',
  );
  return ExitCode.OK;
}

function runningAgentContainers(): string[] {
  return containerNames('baton-agent-');
}


function sessionWatchers(container: string): number | null {
  const res = execCapture(container, [
    'tmux', '-L', RUNTIME_SOCKET, 'list-sessions', '-F', '#{session_attached}',
  ]);
  if (!res.ok) return null;
  const first = res.stdout.trim().split('\n')[0] ?? '';
  return Number.parseInt(first, 10) || 0;
}

export { componentsOf };
