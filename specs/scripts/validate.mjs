// SPDX-License-Identifier: Apache-2.0
























import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { parse } from 'yaml';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '..');

const problems = [];
const fail = (msg) => problems.push(msg);

const spec = parse(readFileSync(resolve(root, 'openapi.yaml'), 'utf8'));


const walk = (node, path, visit) => {
  if (Array.isArray(node)) {
    node.forEach((v, i) => walk(v, `${path}[${i}]`, visit));
  } else if (node && typeof node === 'object') {
    for (const [k, v] of Object.entries(node)) {
      visit(k, v, path);
      walk(v, `${path}.${k}`, visit);
    }
  }
};

const resolveRef = (ref) => {
  if (!ref.startsWith('#/')) return undefined;
  return ref
    .slice(2)
    .split('/')
    .reduce((acc, seg) => (acc == null ? acc : acc[seg]), spec);
};

walk(spec, '$', (key, value, path) => {
  if (key === '$ref' && typeof value === 'string') {
    if (resolveRef(value) === undefined) fail(`unresolved $ref ${value} at ${path}`);
  }
});


const errorRef = '#/components/schemas/Error';
for (const [p, ops] of Object.entries(spec.paths ?? {})) {
  for (const [method, op] of Object.entries(ops)) {
    for (const [status, res] of Object.entries(op.responses ?? {})) {
      if (!/^[45]/.test(status)) continue;

      if (res.$ref) continue;
      const schema = res.content?.['application/json']?.schema;
      if (!schema) {
        fail(`${method.toUpperCase()} ${p} ${status}: error response has no JSON schema`);
      } else if (schema.$ref !== errorRef) {
        fail(`${method.toUpperCase()} ${p} ${status}: error response must use ${errorRef}`);
      }
    }
  }
}

for (const [name, res] of Object.entries(spec.components?.responses ?? {})) {
  const schema = res.content?.['application/json']?.schema;
  if (schema?.$ref !== errorRef) {
    fail(`components.responses.${name} must use ${errorRef}`);
  }
}


const errSchema = spec.components?.schemas?.Error;
for (const required of ['code', 'message', 'request_id', 'remediation']) {
  if (!errSchema?.required?.includes(required)) {
    fail(`Error schema must require '${required}'`);
  }
}


const nodeStatus = spec.components?.schemas?.NodeStatus?.enum ?? [];
if (nodeStatus.includes('healthy') || nodeStatus.includes('ok')) {
  fail(
    "NodeStatus must not contain 'healthy'/'ok': health is a judgement from " +
      'evidence that may be missing, and a status that cannot express ' +
      'uncertainty ends up rendering a failed probe as green',
  );
}
for (const required of ['pending', 'active', 'offline', 'revoked']) {
  if (!nodeStatus.includes(required)) fail(`NodeStatus must contain '${required}'`);
}


const nameSchema = JSON.parse(readFileSync(resolve(root, 'schemas/node-name.json'), 'utf8'));
const specNamePattern = spec.components?.schemas?.EnrollRequest?.properties?.display_name?.pattern;
if (specNamePattern !== nameSchema.pattern) {
  fail(
    `node name pattern disagrees: openapi.yaml has ${specNamePattern}, ` +
      `schemas/node-name.json has ${nameSchema.pattern}`,
  );
}


















const NAME_SITES = [
  ['apps/cli/src/runtime/target.ts', /const NODE_NAME = \/(.+?)\/;/],
  ['apps/control-api/internal/auth/token.go', /nodeNamePattern = regexp\.MustCompile\(`(.+?)`\)/],
  ['apps/control-api/internal/store/sqlite/inbox.go', /identityNamePattern = regexp\.MustCompile\(`(.+?)`\)/],
  ['apps/control-api/internal/httpapi/networks.go', /networkNamePattern = regexp\.MustCompile\(`(.+?)`\)/],


  ['apps/control-api/internal/httpapi/enroll.go', /Pick a name matching (\S+) and retry/],
];
let nameSitesCompared = 0;
{
  const repoRoot = resolve(root, '..', '..');
  let compared = 0;
  for (const [rel, re] of NAME_SITES) {
    let src;
    try {
      src = readFileSync(resolve(repoRoot, rel), 'utf8');
    } catch {
      fail(`node name: cannot read ${rel} — this check names the files it compares, so a moved file is a red, not a skip`);
      continue;
    }
    const m = re.exec(src);
    if (!m) {
      fail(`node name: no pattern found in ${rel}. Either it was renamed — fix this list — or the definition left, which is the thing this check exists to notice.`);
      continue;
    }
    compared += 1;
    if (m[1] !== nameSchema.pattern) {
      fail(
        `node name: ${rel} has ${m[1]}, schemas/node-name.json has ${nameSchema.pattern}. ` +
          'One definition, or a name gets accepted by one component and refused by another.',
      );
    }
  }


  if (compared === 0) {
    fail('node name: compared 0 sites — this check measured nothing and must not read as agreement');
  }
  nameSitesCompared = compared;
}


const capSchema = JSON.parse(
  readFileSync(resolve(root, 'schemas/capability-manifest.json'), 'utf8'),
);
const capRisk = capSchema.$defs?.capability?.properties?.risk?.enum ?? [];
const specRisk = spec.components?.schemas?.Capability?.properties?.risk?.enum ?? [];
if (JSON.stringify(capRisk) !== JSON.stringify(specRisk)) {
  fail(`risk levels disagree: manifest ${JSON.stringify(capRisk)} vs API ${JSON.stringify(specRisk)}`);
}










const runtimeSchema = JSON.parse(
  readFileSync(resolve(root, 'schemas/agent-runtime.json'), 'utf8'),
);
const goSpec = readFileSync(
  resolve(root, '../../apps/agent/internal/runtimespec/spec.go'),
  'utf8',
);













const goConst = (name) => {
  const m = goSpec.match(new RegExp(`\\b${name}\\s+(?:[\\w.]+\\s+)?=\\s*"([^"]+)"`));
  if (!m) fail(`spec.go no longer declares ${name}; this check cannot verify anything`);
  return m?.[1];
};
const goEnum = (...names) => names.map(goConst);

const agree = (what, schemaValue, goValue) => {
  compared += 1;
  if (JSON.stringify(schemaValue) !== JSON.stringify(goValue)) {
    fail(
      `agent-runtime.json disagrees with the Go parser on ${what}: ` +
        `schema ${JSON.stringify(schemaValue)} vs spec.go ${JSON.stringify(goValue)}`,
    );
  }
};







const at = (path) => {
  let node = runtimeSchema;
  for (const key of path.split('.')) {
    node = node?.properties?.[key];
    if (node === undefined) {
      fail(
        `agent-runtime.json has no ${path}; this check cannot verify anything. ` +
          'Either the field left the schema and this line must go with it, or the ' +
          'schema lost a field the Go parser still honours.',
      );
      return undefined;
    }
  }
  return node;
};

let compared = 0;

agree('apiVersion', runtimeSchema.properties?.apiVersion?.const, goConst('APIVersion'));
agree('kind', runtimeSchema.properties?.kind?.const, goConst('Kind'));
agree(
  'adapter.lifecycle.restart',
  at('adapter.lifecycle.restart')?.enum,
  goEnum('RestartAlways', 'RestartOnFailure', 'RestartNever'),
);
agree(
  'adapter.health.probe',
  at('adapter.health.probe')?.enum,
  goEnum('ProbeProcess', 'ProbeTCP', 'ProbeExec', 'ProbeNone'),
);


agree(
  'adapter.skills.discovery',
  at('adapter.skills.discovery')?.enum,
  goEnum('DiscoverySymlink', 'DiscoveryExternalDirs', 'DiscoveryNone'),
);



agree(
  'adapter.status.report',
  at('adapter.status.report')?.enum,
  goEnum('StatusReportFile'),
);



const metaName = runtimeSchema.$defs?.metadata?.properties?.name?.pattern;
compared += 1;
if (metaName !== nameSchema.pattern) {
  fail(
    `agent-runtime.json metadata.name pattern disagrees with schemas/node-name.json: ` +
      `${metaName} vs ${nameSchema.pattern}`,
  );
}











const secretFrom = at('execution.secrets')?.items?.properties?.from?.pattern;
compared += 1;
if (secretFrom !== '^file:/') {
  fail(
    'agent-runtime.json must constrain execution.secrets[].from to ^file:/ — ' +
      'the parser refuses every other source and requires the path to be ' +
      'absolute, and this schema is what a third party implements against; ' +
      `it says ${JSON.stringify(secretFrom)}`,
  );
}

















const orphanedDefs = (doc) => {
  const reachable = new Set();
  const collect = (node) => {
    if (Array.isArray(node)) return node.forEach(collect);
    if (!node || typeof node !== 'object') return;
    const ref = node.$ref;
    if (typeof ref === 'string' && ref.startsWith('#/$defs/')) {
      const name = ref.slice('#/$defs/'.length);
      if (!reachable.has(name)) {
        reachable.add(name);
        collect(doc.$defs?.[name]);
      }
    }
    for (const [k, v] of Object.entries(node)) {
      if (k !== '$ref' && k !== '$defs') collect(v);
    }
  };
  collect({ ...doc, $defs: undefined });
  return Object.keys(doc.$defs ?? {}).filter((n) => !reachable.has(n));
};

let defsWalked = 0;
let schemasWalked = 0;
for (const file of readdirSync(resolve(root, 'schemas')).sort()) {
  if (!file.endsWith('.json')) continue;
  const doc = JSON.parse(readFileSync(resolve(root, 'schemas', file), 'utf8'));
  schemasWalked += 1;
  defsWalked += Object.keys(doc.$defs ?? {}).length;
  const orphans = orphanedDefs(doc);
  if (orphans.length) {
    fail(
      `${file} has $defs nothing reaches: ${orphans.join(', ')}. ` +
        'A definition no $ref points at validates nothing, and a check written ' +
        'against it is measuring a fossil. Delete it, or $ref it from the live tree.',
    );
  }
}
compared += schemasWalked;

if (!problems.length) {
  console.log(
    `agent-runtime.json tracks the Go parser — ${compared} comparisons; ` +
      `${defsWalked} $defs across ${schemasWalked} schemas, all reachable`,
  );
}


if (problems.length > 0) {
  console.error('API contract check FAILED:');
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}

const pathCount = Object.keys(spec.paths ?? {}).length;
const schemaCount = Object.keys(spec.components?.schemas ?? {}).length;
console.log(
  `API contract ok — ${pathCount} paths, ${schemaCount} schemas, refs resolve, ` +
    `agent-runtime.json tracks the Go parser, node name agrees with ${nameSitesCompared} source sites`,
);
