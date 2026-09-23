// SPDX-License-Identifier: Apache-2.0






























import { chownSync, existsSync, mkdirSync, rmSync, statfsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';


export interface VolumePaths {

  img: string;

  mount: string;
}









export function volumePaths(dataDir: string, node: string): VolumePaths {
  const dir = join(dataDir, 'volumes');
  return { img: join(dir, `${node}.img`), mount: join(dir, node) };
}


export interface VolumeResult {
  ok: boolean;

  step?: string;
  detail?: string;
}

function sh(step: string, argv: [string, ...string[]]): VolumeResult {
  const res = spawnSync(argv[0], argv.slice(1), { encoding: 'utf8' });
  if (res.status === 0) return { ok: true };
  const detail = `${res.stderr || res.stdout || ''}`.trim().split('\n').slice(0, 3).join('; ');
  return { ok: false, step, detail: detail || `exited ${res.status ?? 'without a status'}` };
}









export function canMount(): boolean {
  return typeof process.getuid === 'function' && process.getuid() === 0;
}









export function createVolume(paths: VolumePaths, sizeBytes: string, ownerUID?: number): VolumeResult {
  mkdirSync(join(paths.mount, '..'), { recursive: true });
  mkdirSync(paths.mount, { recursive: true });

  if (isMounted(paths.mount)) return { ok: true };

  const fresh = !existsSync(paths.img);
  if (fresh) {


    const made = sh('truncate', ['truncate', '-s', sizeBytes, paths.img]);
    if (!made.ok) return made;
    const formatted = sh('mkfs.ext4', ['mkfs.ext4', '-q', '-F', paths.img]);
    if (!formatted.ok) {


      rmSync(paths.img, { force: true });
      return formatted;
    }
  }
  const mounted = sh('mount', ['mount', '-o', 'loop', paths.img, paths.mount]);
  if (!mounted.ok) return mounted;

















  if (ownerUID !== undefined) {
    try {
      chownSync(paths.mount, ownerUID, ownerUID);
    } catch (err) {
      return { ok: false, step: 'chown', detail: `${(err as Error).message}` };
    }
  }
  return { ok: true };
}












export function removeVolume(paths: VolumePaths): VolumeResult {
  const released = unmountVolume(paths);
  if (!released.ok) return released;
  rmSync(paths.img, { force: true });
  rmSync(paths.mount, { recursive: true, force: true });
  return { ok: true };
}










export function unmountVolume(paths: VolumePaths): VolumeResult {
  if (!isMounted(paths.mount)) return { ok: true };
  return sh('umount', ['umount', paths.mount]);
}


export function isMounted(path: string): boolean {
  const res = spawnSync('mountpoint', ['-q', path], { encoding: 'utf8' });
  return res.status === 0;
}










export interface StorageFaces {

  volume:
    | { configured: false }

    | { configured: true; path: string; sizeBytes: number; freeBytes: number }

    | { configured: true; path: string; unknown: string };

  rootfs: { shared: true; quota: false };
}










export function storageFaces(dataDir: string, node: string): StorageFaces {
  const paths = volumePaths(dataDir, node);
  const rootfs = { shared: true, quota: false } as const;




  if (!existsSync(paths.img)) return { volume: { configured: false }, rootfs };

  try {
    const s = statfsSync(paths.mount);
    return {
      volume: {
        configured: true,
        path: paths.img,
        sizeBytes: Number(s.blocks) * Number(s.bsize),
        freeBytes: Number(s.bavail) * Number(s.bsize),
      },
      rootfs,
    };
  } catch (err) {
    return { volume: { configured: true, path: paths.img, unknown: `${(err as Error).message}` }, rootfs };
  }
}








export function loopDeviceCount(): number | undefined {
  const res = spawnSync('losetup', ['-a'], { encoding: 'utf8' });
  if (res.status !== 0) return undefined;
  return res.stdout.split('\n').filter((l) => l.trim().length > 0).length;
}
