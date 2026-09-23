// SPDX-License-Identifier: Apache-2.0




























export const INTERNAL_VERBS_ENV = 'BATON_INTERNAL_VERBS';










export function internalVerbsOn(env: NodeJS.ProcessEnv = process.env): boolean {
  return env[INTERNAL_VERBS_ENV] === '1';
}
