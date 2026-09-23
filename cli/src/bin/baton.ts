#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0

































import { flagString, parse, type ParsedArgs } from '../args.js';
import { Client, defaultAdminDir } from '../api/client.js';
import { operator as operatorVerb } from '../commands/operator.js';
import { BatonError, ExitCode, exitCodeFor, renderError, usageError } from '../errors.js';
import { json } from '../output.js';
import * as inbox from '../commands/inbox.js';
import * as network from '../commands/network.js';
import * as integrations from '../commands/integrations.js';
import * as skills from '../commands/skills.js';
import * as cloud from '../commands/cloud.js';
import * as portable from '../commands/snapshot.js';
import * as cluster from '../commands/cluster.js';
import * as lifecycle from '../commands/lifecycle.js';
import * as observe from '../commands/observe.js';
import { web } from '../commands/web.js';
import { nodeCreate, setupMaster, setupStandby } from '../commands/create.js';
import { init as initVerb } from '../commands/init.js';
import { agent as agentVerb } from '../commands/agent.js';
import { internalVerb } from '../commands/internal.js';
import { internalVerbsOn } from '../internal-verbs.js';
import { panel } from '../commands/panel.js';
import { template as templateVerb } from '../commands/template.js';
import { plugin as pluginVerb } from '../commands/plugin.js';
import { kb as kbVerb } from '../commands/kb.js';
import { resource as resourceVerb } from '../commands/resource.js';
import { dna as dnaVerb } from '../commands/dna.js';
import { migrate } from '../commands/migrate.js';
import { transfer } from '../commands/transfer.js';
import { access } from '../commands/access.js';
import { deliveryVerb } from '../commands/delivery.js';
import { needsClient } from '../needs-client.js';
import { readFileSync } from 'node:fs';
import { packageVersion } from '../version.js';


const VERSION = packageVersion();
























type BuildStamp = { commit: string | null; state: 'clean' | 'dirty' | 'unknown' };

function buildStamp(): BuildStamp {
  try {
    const raw = readFileSync(new URL('../.source', import.meta.url), 'utf8').trim();
    const [commit, state] = raw.split(/\s+/);
    if (!commit) return { commit: null, state: 'unknown' };
    return { commit, state: state === 'clean' ? 'clean' : state === 'dirty' ? 'dirty' : 'unknown' };
  } catch {
    return { commit: null, state: 'unknown' };
  }
}

function renderVersion(asJSON: boolean): string {
  const b = buildStamp();
  if (asJSON) {
    return json({ version: VERSION, node: process.versions.node, build: b }) + '\n';
  }
  return b.commit === null
    ? `${VERSION} (build unknown: no dist/.source — this CLI was not built by \`pnpm build\`, or that build failed)\n`
    : `${VERSION} (${b.commit} ${b.state})\n`;
}



















const USAGE = `baton - manage your own agent network

FIRST RUN:

  1  baton doctor                     is this machine able to run it
  2  baton agent create --name <n>    an agent — and, if this machine has no
                                      control plane yet, a network for it
  3  baton panel                      open the panel and look at it

  Step 2 founds a network when there is none, and says so when it does.

Agents

  baton agent create --name <n>    create an agent; the node it runs on comes
                                   with it, and a network if this machine has none
                                   --template <file|name> the Node Template it runs
                                   --node-id <id>   move into an office that already
                                   exists (see node create) instead of opening one
                                   --secret NAME=<host file> per credential the
                                   spec declares; repeatable, never the value
                                   --owner <who>
                                   --allow-remote-shell  seed this node's shell
                                   switch ON (default off; see agent shell)
  baton agent join --name <n>      the agent, with its office, APPLIES to join a network:
                                   --network <example.com | team@<hosted-registry> | https://host:8443>
                                   (the master admits with 'baton network add --agent <n>')
  baton agents [bind|unbind]       who exists, and where each one resolves
  baton agent config show|set <n>  the agent's workspace config (set reads stdin)
  baton agent secret list|set <n> <NAME>  fill in a template's declared secrets
  baton agent shell <n> [on|off]   may an operator open a shell on this node
                                   through the control plane; no on|off prints it
                                   (off by default; --allow-remote-shell at
                                   create seeds it on)

Nodes

  baton node create <name>         open an office: a node, ready, in no network —
                                   never founds one, never joins one (a node cannot;
                                   an agent does) --owner <who> --template <file|name>
  baton node list|show|revoke      the node directory
  baton template list              the Node Templates in <data-dir>/templates/
                                   (--template <name> resolves there; --template <file> is a path)
  baton plugin install <dir|id>    put a plugin in <data-dir>/plugins/ (door: installed);
                                   --node <name> also pushes it into a node on this machine
  baton plugin list [--node <n>]   the store, or what a node's daemon made of its copy
  baton plugin remove <id>         out of the store (--force: even if a template names it);
                                   --node <name> takes it off a node, links and state included
  baton status                     containers, nodes, and whether the control plane answers
  baton start|stop|restart [node]  lifecycle; no name acts on every node
  baton rebuild [node]             run it on a newer image, keeping workspace and identity
  baton destroy <node>             remove a node, its volume, and its identity

Who a node is for


Moving work between nodes

  baton snapshot <node>            capture portable runtime state — never an identity
  baton migrate --name <n> --to <host>  move an agent to another node: same address, new node id
                                   (this build: --to localhost; --yes required)
  baton restore <node> --from <f>  put that state back; the node keeps its own identity
  baton clone <src> <new>          same state on a new node, with its own identity

Messages

  baton send @agent --text|--file  send a message to an identity, not a node
  baton inbox [@agent] [--state]   message envelopes; bodies are the recipient's
  baton inbox open <message-id>    one message body — and it marks the message read
  baton kb list|show|set|rm        the knowledge base: slug -> markdown/html doc

Watching and getting in

  baton logs <node> [-f]           container output; -f reconnects across restarts
  baton attach <node>              watch the runtime's terminal, read-only
  baton attach <node> --takeover   take control: exclusive, audited
  baton sessions                   which runtimes have a terminal, and who is watching
  baton ssh <agent>                a shell on your node, by address (agent@network)
  baton shell <node>               a real shell inside the container (admin only)
  baton console <node>             audited management commands

This machine, and the control plane

  baton init                       make this machine able to use baton
  baton setup master [name]        promote an existing node
  baton setup standby [name]       a read-only mirror of another control plane
                                   --master <url> --token <file>
  baton network requests           who has applied to join this network
  baton network add --agent <n>    admit the named agent's application (this
                                   machine's master network); deny --agent <n> refuses
  baton network show|set-name <name>|invite|approve-transfer
  baton token create --role master | list | revoke   a mirror's enrolment token
  baton access grant issue|list|revoke   endorse another network to read here
  baton access received import|list|rm   grants this network has received
  baton delivery grant issue|list|revoke endorse another network to DELIVER here
                                         (access is read; delivery is write —— two directions)
  baton access token get --grant <f> --to <A> --ca <ca>   B: get a scoped token
  baton access fetch <id> --token <f> --to <A> --ca <ca>  B: read A's resource
  baton capability list|grant|revoke
  baton call <capability> --node <name> --input '<json>'
  baton events [--verify]          the append-only log
  baton core status                which execution chain, contract and build answered
  baton core propose               carry a Proposal to the chain (body on stdin)
  baton core approve|reject <id>   a verdict on a frozen plan (--plan-digest required)
  baton context                    for a Harness: the caller's open and recent transactions,
                                   and the next Operation (no nonce, no executor evidence)
  baton provider serve             the local Provider: executes and answers by operation_id,
                                   on <data-dir>/provider/run/provider.sock, a directory
                                   mounted only into the control plane's container

Resources — what this network shares, and what an office has taken

  baton resource list|get           what this network shares (add --network <ref>
                                    to read another network you are a member of)
  baton resource publish --type <t> --name <n> --scope private|network|public
                                    hand something to the network. --scope has no
                                    default; --type is one of skill, knowledge,
                                    policy, workflow, template, other, plugin
  baton resource unpublish <id>     withdraw it (and its directory listing)
  baton resource move|tag <id>      where it sits, and what it is called by
  baton resource admit|deny <id>    review what a member offered (--reason on deny)
  baton resource discover           search the open directory (anonymous)
  baton resource fetch <id> --save  take a copy, with provenance and an anchor
                                    --binding pinned (default) | follow --credential <n>
  baton resource copy <id>          fetch and republish here, in one act
  baton resource verify             re-check every copy against where it came from
  baton resource install            RESERVED — not built (Stage 3); it refuses by name

Panel and console

  baton panel [--port 8043]        open the control panel on a local port
                                   --dist <build> or --upstream <url> for assets
                                   loopback only; reads proxied, writes only
                                   from a short approved list no flag widens,
                                   plus approve/reject of a frozen plan
  baton panel passwd               change the panel's password
                                   --reset-default --yes back to the shipped one
  baton web                        open the read-only console in a browser

Housekeeping

  baton doctor                     check preconditions
  baton uninstall [--purge-data]   remove containers
  baton version                    version and which build (also --version, -V)

Global flags:
  --master <url>            control plane (default https://127.0.0.1:8443)
  --admin-dir <path>        operator credentials
  --output table|json       output format
  --timeout <duration>      request timeout (30s)
  --yes, --dry-run, --verbose

Exit codes: 0 ok, 2 usage, 3 precondition, 4 unreachable, 5 auth,
            6 conflict, 7 partial, 8 unsupported, 10 internal
`;



async function setupVerb(args: ParsedArgs): Promise<number> {
  const what = args.positionals[1];
  if (what === 'master') return setupMaster(args);
  if (what === 'standby') return setupStandby(args);
  throw usageError(
    what ? `setup ${what} is not something to set up` : 'setup needs something to set up',
    'Two: `baton setup master` and `baton setup standby --master <url> --token <file>`.',
  );
}






function deferredClient(build: () => Client): Client {
  let real: Client | undefined;
  return new Proxy({} as Client, {
    get(_t, prop) {
      real ??= build();
      const v = (real as unknown as Record<PropertyKey, unknown>)[prop];
      return typeof v === 'function' ? (v as (...a: unknown[]) => unknown).bind(real) : v;
    },
  });
}

async function main(argv: string[]): Promise<number> {









  if (argv[0] === '--version' || argv[0] === '-V') {


    const i = argv.indexOf('--output');
    const asJSON = argv.includes('--output=json') || (i >= 0 && argv[i + 1] === 'json');
    process.stdout.write(renderVersion(asJSON));
    return ExitCode.OK;
  }

  const args: ParsedArgs = parse(argv);
  const command = args.positionals[0];

  if (!command || args.flags.get('help') === true) {
    process.stdout.write(USAGE);
    return command ? ExitCode.OK : ExitCode.CONFIG;
  }

  if (command === 'version') {
    process.stdout.write(renderVersion(args.global.output === 'json'));
    return ExitCode.OK;
  }

  const newClient = () =>
    new Client({
      baseURL: args.global.master,
      adminDir: args.global.adminDir ?? defaultAdminDir(args.global.dataDir),
      timeoutMs: args.global.timeoutMs,
    });











  let client: Client | undefined;
  if (needsClient(command, args.positionals[1] ?? '')) {
    client = args.global.dryRun ? deferredClient(newClient) : newClient();
  }

  switch (command) {
    case 'init':        return initVerb(args);
    case 'agent':       return agentVerb(args);




    case 'internal':
      if (internalVerbsOn()) return internalVerb(args);



      throw usageError(`${command} is not a command`, 'Run `baton --help` for the list.');
    case 'setup':       return setupVerb(args);
    case 'panel':       return panel(args);





    case 'connector':   return (await import('../commands/connector.js')).connectorVerb(args);
    case 'start':       return lifecycle.lifecycle(args, 'start');
    case 'stop':        return lifecycle.lifecycle(args, 'stop');
    case 'restart':     return lifecycle.lifecycle(args, 'restart');
    case 'rebuild':     return lifecycle.rebuild(args);
    case 'snapshot':    return portable.snapshot(args);
    case 'restore':     return portable.restore(args);
    case 'clone':       return portable.clone(args, client!);
    case 'migrate':     return migrate(args, client!);
    case 'destroy':     return lifecycle.destroy(args, newClient);
    case 'doctor':      return lifecycle.doctor(args);
    case 'uninstall':   return lifecycle.uninstall(args);




    case 'operator':    return operatorVerb(args, client);
    case 'status':      return cluster.status(args, client!);
    case 'node':
    case 'nodes':




















      if (args.positionals[1] === 'create') {




        if (flagString(args, 'network')) {
          throw usageError(
            '--network is reserved: a node is created in no network',
            'A node never joins a network by itself; only an agent does: move an agent in, then `baton agent join ' +
              '--name <n> --network <network>` — an application the network\'s master decides whether to admit. ' +
              'The container engine\'s network is set by BATON_DRIVER_NETWORK (default `baton`), and a runtime spec\'s ' +
              'execution.driver_network overrides it; --network always means a BATON network.',
          );
        }



        if (flagString(args, 'enrollment-token-file')) {
          throw usageError(
            'node create takes no --enrollment-token-file: a node cannot join a network',
            'An agent joins, with the office it holds: `baton agent create --name <n> --node-id <id> …` ' +
              'then `baton agent join --name <n> --network <network>`. There is no token for a person to ' +
              'carry: the network mints one for the node it admitted, and hands it over itself.',
          );
        }
        return nodeCreate(args);
      }
      return cluster.node(args, newClient);
    case 'list':        return cluster.node({ ...args, positionals: ['node', 'list'] }, newClient);
    case 'template':    return templateVerb(args);
    case 'plugin':      return pluginVerb(args);
    case 'token':       return cluster.token(args, client!);
    case 'capability':  return cluster.capability(args, client!);
    case 'call':        return cluster.call(args, client!);
    case 'core':        return (await import('../commands/core.js')).coreVerb(args, client!);
    case 'provider':    return (await import('../commands/provider.js')).providerVerb(args);
    case 'context':     return (await import('../commands/context.js')).contextVerb(args, client!);
    case 'kb':          return kbVerb(args);
    case 'resource':    return resourceVerb(args, newClient);




    case 'dna':         return dnaVerb(args, newClient);
    case 'events':      return cluster.events(args, client!);




    case 'send':        return inbox.send(args, client!);
    case 'inbox':       return inbox.inbox(args, client!);
    case 'agents':      return inbox.agents(args, client!);
    case 'network':     return network.network(args, client!);
    case 'integrations': return integrations.integrations(args, client!);
    case 'skills':      return skills.skills(args, client!);
    case 'cloud':       return cloud.cloud(args, client!);
    case 'failover':    return cluster.failover(args);
    case 'transfer':    return transfer(args, client!);
    case 'access':      return access(args, client!);



    case 'delivery':    return deliveryVerb(args, client!);




    case 'attach':      return observe.attach(args, newClient);
    case 'shell':       return observe.shell(args, client!);
    case 'ssh':         return (await import('../commands/ssh.js')).ssh(args, client!);
    case 'console':     return observe.consoleCmd(args, client!);
    case 'logs':        return observe.logs(args);
    case 'sessions':    return observe.sessions(args);
    case 'web':         return web(args, client!);

    default:
      throw usageError(
        `${command} is not a command`,
        'Run `baton --help` for the list.',
      );
  }
}

const argv = process.argv.slice(2);
const wantsJSON = argv.includes('--output=json') ||
  (argv.includes('--output') && argv[argv.indexOf('--output') + 1] === 'json');
const verbose = argv.includes('--verbose') || argv.includes('-v');

















function exit(code: number): void {
  process.exitCode = code;
  process.stdout.on('error', () => process.exit(code));
  process.stdout.write('', () => process.exit(code));
}

main(argv)
  .then(exit)
  .catch((err: unknown) => {
    if (wantsJSON) {
      const body =
        err instanceof BatonError
          ? err.toJSON()
          : {
              code: 'INTERNAL',
              message: err instanceof Error ? err.message : String(err),
              request_id: '',
              remediation: 'Re-run with --verbose for the stack trace.',
            };
      process.stderr.write(json(body) + '\n');
    } else {
      process.stderr.write(renderError(err, verbose) + '\n');
    }
    exit(exitCodeFor(err));
  });
