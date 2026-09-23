// SPDX-License-Identifier: Apache-2.0











import { PLUGINS_ROOT, PLUGINS_TREE_SUBPATH } from '../runtime/plugins.js';
import { stringify } from 'yaml';
import type { ComponentRole } from '../runtime/target.js';
import { defaultImageRef } from '../runtime/image-pins.js';

export interface RenderOptions {
  role: ComponentRole;








  seccompProfile: string;

  withAgentRole?: boolean;
  name: string;
  version: string;

  registry?: string;

  port?: number;







  bindAddress?: string;
  advertiseURL?: string;
  masterURL?: string;

  labels?: string;






  operationId?: string;









  allowRemoteShell?: boolean;

  tlsSANs?: string[];
  logLevel?: string;

  network: string;







  runtime?: RuntimeManifest;













  lxcfs?: boolean;












  dataVolumePath?: string;
}


export interface RuntimeManifest {
  name: string;

  image?: string;

  specPath: string;
  workspaceVolume?: string;
  workspaceMountPath?: string;

  workspaceReadOnly?: boolean;










  skillsMountPath?: string;









  runtimeUser?: string;
  cpu?: string;
  memory?: string;

  template?: string;













  driverNetwork?: string;

  secretMounts?: { source: string; target: string }[];








  installPrograms?: string[];





  commandPath?: string;








  egress?: string;
}


const MEMORY: Record<ComponentRole, string> = {
  master: '384m',
  standby: '224m',
  agent: '192m',
};

const CPUS: Record<ComponentRole, number> = { master: 0.75, standby: 0.4, agent: 0.4 };

function image(opts: RenderOptions): string {





  if (opts.role === 'agent' && opts.runtime?.image) return opts.runtime.image;



  const ref = defaultImageRef(opts.role === 'agent' ? 'agent' : 'control-api', opts.version);
  const prefix = opts.registry ? `${opts.registry.replace(/\/+$/, '')}/` : '';
  return `${prefix}${ref}`;
}


const SPEC_MOUNT = '/etc/baton/runtime.yaml';


export const LXCFS_ROOT = '/var/lib/lxcfs';





















































export const LXCFS_PATHS = [
  'proc/cpuinfo',
  'proc/meminfo',
  'proc/stat',
  'proc/uptime',
  'proc/loadavg',
  'proc/diskstats',
  'proc/swaps',
  'sys/devices/system/cpu',
] as const;





















const AGENT_CAPS = ['CHOWN', 'DAC_OVERRIDE', 'FOWNER', 'FSETID', 'KILL', 'SETGID', 'SETUID'];





























function hardening(role: ComponentRole, seccompProfile: string) {
  const agent = role === 'agent';
  return {
    restart: 'unless-stopped',



    user: agent ? '0:0' : '65532:65532',
    read_only: !agent,
    cap_drop: ['ALL'],
    ...(agent ? { cap_add: AGENT_CAPS } : {}),





    security_opt: ['no-new-privileges:true', `seccomp=${seccompProfile}`],



    pids_limit: agent ? 1024 : 256,
    ulimits: {
      nproc: agent ? { soft: 2048, hard: 4096 } : { soft: 512, hard: 1024 },
      nofile: agent ? { soft: 65536, hard: 65536 } : { soft: 8192, hard: 8192 },
    },


    tmpfs: [agent ? '/tmp:rw,nosuid,size=256m' : '/tmp:rw,noexec,nosuid,size=32m'],
    mem_limit: MEMORY[role],
    cpus: CPUS[role],
    logging: {
      driver: 'json-file',
      options: { 'max-size': '10m', 'max-file': '3' },
    },
  };
}








export function dataVolumeName(role: string, name: string): string {
  return `baton-${name}_baton-${role}-${name}-data`;
}














export const OPERATION_ID_LABEL = "dev.baton.operation-id";

export function renderCompose(opts: RenderOptions): string {
  const container = `baton-${opts.role}-${opts.name}`;
  const volume = `${container}-data`;

  const rt = opts.role === 'agent' ? opts.runtime : undefined;












  const volumes: (string | Record<string, unknown>)[] = [
    opts.dataVolumePath ? `${opts.dataVolumePath}:/var/lib/baton` : `${volume}:/var/lib/baton`,
  ];
  const extraVolumes: Record<string, null> = {};
  if (rt) {







    volumes.push(
      opts.dataVolumePath
        ? `${opts.dataVolumePath}/${PLUGINS_TREE_SUBPATH}:${PLUGINS_ROOT}`
        : { type: 'volume', source: volume, target: PLUGINS_ROOT, volume: { subpath: PLUGINS_TREE_SUBPATH } },
    );


    volumes.push(`${rt.specPath}:${SPEC_MOUNT}:ro`);

    if (rt.workspaceVolume && rt.workspaceMountPath) {
      volumes.push(`${rt.workspaceVolume}:${rt.workspaceMountPath}${rt.workspaceReadOnly ? ':ro' : ''}`);
      extraVolumes[rt.workspaceVolume] = null;
    }



    for (const s of rt.secretMounts ?? []) {
      volumes.push(`${s.source}:${s.target}:ro`);
    }
  }













  if (opts.lxcfs) {
    for (const p of LXCFS_PATHS) volumes.push(`${LXCFS_ROOT}/${p}:/${p}:ro`);
  }

  const common = {
    container_name: container,
    image: image(opts),
    ...hardening(opts.role, opts.seccompProfile),





    ...(rt?.memory ? { mem_limit: Number(rt.memory) } : {}),
    ...(rt?.cpu ? { cpus: Number(rt.cpu) } : {}),
    volumes,
    ...(opts.operationId ? { labels: { [OPERATION_ID_LABEL]: opts.operationId } } : {}),
  };

  const service =
    opts.role === 'agent'
      ? {
          ...common,
          environment: {
            BATON_NODE_NAME: opts.name,
            BATON_MASTER_URL: opts.masterURL ?? '',
            BATON_DATA_DIR: '/var/lib/baton',
            BATON_LOG_LEVEL: opts.logLevel ?? 'info',




            ...(opts.labels ? { BATON_LABELS: opts.labels } : {}),



            ...(opts.allowRemoteShell ? { BATON_ALLOW_REMOTE_SHELL: '1' } : {}),
            ...(rt ? { BATON_RUNTIME_SPEC: SPEC_MOUNT } : {}),
          },


        }
      : {
          ...common,
          environment: {
            BATON_NAME: opts.name,



            BATON_WITH_AGENT_ROLE: opts.withAgentRole ? 'true' : 'false',
            BATON_MODE: opts.role === 'standby' ? 'mirror' : 'primary',
            BATON_EDITION: 'personal',
            BATON_DATA_DIR: '/var/lib/baton',
            BATON_LISTEN_ADDRESS: '0.0.0.0:8443',
            BATON_ADVERTISE_URL: opts.advertiseURL ?? `https://127.0.0.1:${opts.port ?? 8443}`,
            BATON_TLS_SANS: [opts.name, container, ...(opts.tlsSANs ?? [])].join(','),
            BATON_LOG_LEVEL: opts.logLevel ?? 'info',
            ...(opts.role === 'standby'
              ? {
                  BATON_MASTER_URL: opts.masterURL ?? '',
                  BATON_SNAPSHOT_INTERVAL_SEC: '60',













                  BATON_ENROLLMENT_TOKEN: '${BATON_STANDBY_TOKEN:-}',
                }
              : {}),
          },










          ports: [`${opts.bindAddress ?? '127.0.0.1'}:${opts.port ?? 8443}:8443`],
        };

  const project = {
    name: `baton-${opts.name}`,



















    ...(rt?.workspaceMountPath ? { 'x-baton-workspace': rt.workspaceMountPath } : {}),
    services: { [opts.role]: service },




    volumes: opts.dataVolumePath
      ? { ...extraVolumes }
      : { [volume]: opts.operationId ? { labels: { [OPERATION_ID_LABEL]: opts.operationId } } : null, ...extraVolumes },


    networks: { default: { name: opts.network, external: true } },
  };

  return (
    '# SPDX-License-Identifier: Apache-2.0\n' +
    `# Generated by baton create ${opts.role} ${opts.name}.\n` +
    '# Safe to read, safe to keep. Regenerated on the next create.\n' +
    stringify(project, { lineWidth: 0 })
  );
}
