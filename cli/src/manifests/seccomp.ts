// SPDX-License-Identifier: Apache-2.0


































import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

export const SECCOMP_PROFILE = {
  defaultAction: 'SCMP_ACT_ALLOW',
  architectures: ['SCMP_ARCH_X86_64', 'SCMP_ARCH_X86', 'SCMP_ARCH_X32', 'SCMP_ARCH_AARCH64', 'SCMP_ARCH_ARM'],
  syscalls: [
    {
      names: ['ptrace', 'process_vm_readv', 'process_vm_writev'],
      action: 'SCMP_ACT_ERRNO',
      errnoRet: 1,
    },
  ],
} as const;


export function seccompProfilePath(dataDir: string): string {
  return join(dataDir, 'seccomp', 'noptrace.json');
}











export function materializeSeccompProfile(dataDir: string): string {
  const path = seccompProfilePath(dataDir);
  mkdirSync(join(dataDir, 'seccomp'), { recursive: true });
  writeFileSync(path, JSON.stringify(SECCOMP_PROFILE, null, 2) + '\n', { mode: 0o644 });
  return path;
}
