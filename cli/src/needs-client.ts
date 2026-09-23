// SPDX-License-Identifier: Apache-2.0


























const NEEDS_CLIENT = new Set([










  'status', 'token', 'capability', 'call', 'events', 'console', 'shell', 'web', 'ssh',
  'send', 'inbox', 'agents', 'network', 'integrations', 'cloud', 'clone',
  'skills', 'migrate', 'transfer', 'core', 'context',






  'access',





  'delivery',




  'operator',
]);










export const CLIENT_SUBS: Record<string, Set<string>> = {













  cloud: new Set(['', 'status', 'connect', 'disconnect', 'templates', 'diff', 'phone-connect']),













  network: new Set(['', 'show', 'set-name', 'register', 'set-avatar', 'publish', 'requests', 'add', 'deny', 'invite', 'approve-transfer', 'admission', 'visibility']),




  access: new Set(['grant', 'token', 'received']),








  operator: new Set(['invite', 'list', 'revoke']),
};







export function needsClient(command: string, sub: string): boolean {
  if (!NEEDS_CLIENT.has(command)) return false;
  const subs = CLIENT_SUBS[command];
  return !subs || subs.has(sub);
}
