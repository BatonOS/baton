// SPDX-License-Identifier: Apache-2.0






















import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { stringify } from 'yaml';
import { usageError } from '../errors.js';
import { BATON_DIR } from './workspace.js';


export const EXT_DIR = 'ext';


export const PARCEL_FILE = 'parcel.yaml';





















export const REPORT_FILE = 'report.json';

export interface Extension {
  provider: string;

  config: Record<string, unknown>;
}









export function parseExtensions(raw: unknown, label: string): Extension[] {
  if (raw === undefined) return [];
  if (!Array.isArray(raw)) {
    throw usageError(`${label}: extensions must be a list`, 'extensions: is a list of { provider, config } entries; each names its provider as a field, and BATON places the config without reading it.');
  }

  const out: Extension[] = [];
  const seen = new Set<string>();
  raw.forEach((entry, i) => {
    const at = `${label}: extensions[${i}]`;
    if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
      throw usageError(`${at} is not a mapping`, 'Each entry is { provider: <name>, config: { … } }.');
    }
    const e = entry as Record<string, unknown>;
    const known = new Set(['provider', 'config']);
    const unknown = Object.keys(e).filter((k) => !known.has(k));
    if (unknown.length > 0) {
      throw usageError(
        `${at} has fields this build does not understand: ${unknown.join(', ')}`,
        'An extension entry has provider and config — nothing else. Anything a provider needs goes inside config, which BATON does not read.',
      );
    }

    const provider = e.provider;
    if (typeof provider !== 'string' || provider.trim() === '') {
      throw usageError(`${at} has no provider`, 'provider: names who will do the decorating, e.g. baton.oss or baton.cloud.');
    }



    if (!/^[a-z0-9][a-z0-9._-]*$/.test(provider)) {
      throw usageError(
        `${at}: provider ${provider} is not a usable name`,
        'A provider name is lowercase letters, digits, dot, dash or underscore, and it becomes a directory name.',
      );
    }
    if (seen.has(provider)) {


      throw usageError(`${at}: ${provider} is declared twice`, 'One entry per provider; put everything it needs in that entry’s config.');
    }
    seen.add(provider);

    const config = e.config;
    if (config !== undefined && (typeof config !== 'object' || config === null || Array.isArray(config))) {
      throw usageError(`${at}: config must be a mapping`, 'config: is the provider’s own document. BATON writes it out and does not read it.');
    }
    out.push({ provider, config: (config as Record<string, unknown>) ?? {} });
  });
  return out;
}



export function extensionDir(provider: string): string {
  return join(BATON_DIR, 'portable', EXT_DIR, provider);
}














export function stageParcels(dir: string, extensions: Extension[]): string[] {
  const written: string[] = [];
  for (const ext of extensions) {
    const rel = extensionDir(ext.provider);
    mkdirSync(join(dir, rel), { recursive: true });


    writeFileSync(join(dir, rel, PARCEL_FILE), stringify(ext.config));
    written.push(join(rel, PARCEL_FILE));
  }
  return written;
}



















export type DecorationState = 'undeclared' | 'not-placed' | 'parcel-placed' | 'reported';













export function decorationStateOf(workspaceRoot: string, provider: string, declared: Extension[]): DecorationState {
  if (!declared.some((e) => e.provider === provider)) return 'undeclared';
  if (existsSync(join(workspaceRoot, extensionDir(provider), REPORT_FILE))) return 'reported';








  return existsSync(join(workspaceRoot, extensionDir(provider), PARCEL_FILE)) ? 'parcel-placed' : 'not-placed';
}










export function describeDecoration(provider: string, state: DecorationState): string {
  switch (state) {
    case 'undeclared':
      return `${provider}: not declared`;
    case 'not-placed':
      return `${provider}: declared, but nothing is on disk — the parcel BATON placed is gone, or none was ever staged`;
    case 'parcel-placed':
      return `${provider}: parcel placed — installed: unknown, only ${provider} can say`;
    case 'reported':





      return `${provider}: has reported — what it said is in ${EXT_DIR}/${provider}/${REPORT_FILE}, which BATON does not read`;
  }
}
