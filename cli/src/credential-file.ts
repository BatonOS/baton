import {
  closeSync,
  constants,
  fchmodSync,
  ftruncateSync,
  fstatSync,
  lstatSync,
  openSync,
  readFileSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { dirname, resolve } from 'node:path';
import { BatonError, ExitCode, preconditionError } from './errors.js';







































export function readCredentialFile(path: string, what: string): string {
  const full = resolve(path);









  let dirMode: number;
  try {
    dirMode = statSync(dirname(full)).mode;
  } catch (err) {
    throw preconditionError(
      `cannot inspect the directory holding the ${what}`,
      `${(err as Error).message}. Nothing was sent.`,
    );
  }
  const groupOrOtherWrite = dirMode & 0o022;
  const sticky = dirMode & 0o1000;
  if (groupOrOtherWrite && !sticky) {
    throw new BatonError({
      code: 'CREDENTIAL_DIR_TOO_OPEN',
      message:
        `${dirname(full)} is writable by others (mode ${(dirMode & 0o7777).toString(8)}), ` +
        `so the ${what} in it can be replaced`,
      remediation:
        `The file's own permissions do not help here: anyone who can write this directory can ` +
        `delete your token and leave their own in its place, and this command would then present ` +
        `their identity as yours. Run \`chmod 700 ${dirname(full)}\`, or keep the credential ` +
        `somewhere only you can write. Nothing was sent.`,
      exitCode: ExitCode.CONFIG,
    });
  }
















  const noFollow = constants.O_NOFOLLOW ?? 0;
  if (!noFollow && lstatSync(full, { throwIfNoEntry: false })?.isSymbolicLink()) {
    throw symlinkRefusal(full, 'read');
  }

  let fd: number;
  try {
    fd = openSync(full, constants.O_RDONLY | noFollow);
  } catch (err) {
    const e = err as NodeJS.ErrnoException;








    if (e.code === 'ELOOP') {
      throw symlinkRefusal(full, 'read');
    }
    throw preconditionError(`no ${what} at ${full}`, `${e.message}. Nothing was sent.`);
  }

  try {
    const st = fstatSync(fd);
    requireOwnedByMe(full, st.uid, what, 'read');
    const mode = st.mode;
    const exposed = mode & 0o077;
    if (exposed !== 0) {
      const who: string[] = [];
      if (mode & 0o070) who.push('the group');
      if (mode & 0o007) who.push('everyone else');
      throw new BatonError({
        code: 'CREDENTIAL_TOO_OPEN',
        message: `${full} is readable by ${who.join(' and ')} (mode ${(mode & 0o777).toString(8)})`,
        remediation:
          `A bearer token is authentication by possession, so a copy is as good as the original. ` +
          `Run \`chmod 600 ${full}\` and try again. Nothing was sent — and if this file has been ` +
          `exposed, the credential in it should be reissued rather than just tightened.`,
        exitCode: ExitCode.CONFIG,
      });
    }
    const value = readFileSync(fd, 'utf8').trim();
    if (!value) {



      throw preconditionError(`the ${what} at ${full} is empty`, 'Nothing was sent.');
    }
    return value;
  } finally {
    closeSync(fd);
  }
}






























function requireOwnedByMe(full: string, uid: number, what: string, direction: 'read' | 'written'): void {
  const me = process.geteuid?.();
  if (me === undefined || uid === me) return;
  throw new BatonError({
    code: 'CREDENTIAL_NOT_OWNED_BY_YOU',
    message: `${full} is owned by uid ${uid}, not by you (uid ${me})`,
    remediation:
      `A mode of 0600 says nobody but the owner can read it — it does not say the owner is you. ` +
      `Someone else's file at this path is someone else's credential, and in a shared directory ` +
      `it can be put there before yours. Use a path you own, or \`chown\` this one. ` +
      `Nothing was ${direction === 'read' ? 'read' : 'written'}.`,
    exitCode: ExitCode.CONFIG,
  });
}

function symlinkRefusal(full: string, direction: 'read' | 'written'): BatonError {
  const did = direction === 'read' ? 'read' : 'written';
  return new BatonError({
    code: 'CREDENTIAL_IS_A_SYMLINK',
    message: `${full} is a symbolic link`,
    remediation:
      `A credential is ${did} to the file itself, never through a link that somebody else may ` +
      `repoint between one run and the next — the link's own permissions say nothing about where ` +
      `it currently points. Give the path of the file. Nothing was ${did}.`,
    exitCode: ExitCode.CONFIG,
  });
}



















export function writeCredentialFile(path: string, value: string, what: string): number {
  const full = resolve(path);
  const noFollow = constants.O_NOFOLLOW ?? 0;
  if (!noFollow && lstatSync(full, { throwIfNoEntry: false })?.isSymbolicLink()) {
    throw symlinkRefusal(full, 'written');
  }
  let fd: number;
  try {




    fd = openSync(full, constants.O_WRONLY | constants.O_CREAT | noFollow, 0o600);
  } catch (err) {
    const e = err as NodeJS.ErrnoException;
    if (e.code === 'ELOOP') throw symlinkRefusal(full, 'written');
    throw preconditionError(
      `could not write the ${what} to ${full}`,
      `${e.message}. Nothing was written.`,
    );
  }
  try {



    requireOwnedByMe(full, fstatSync(fd).uid, what, 'written');
    ftruncateSync(fd, 0);
    writeFileSync(fd, value);


    fchmodSync(fd, 0o600);
    return fstatSync(fd).mode & 0o777;
  } finally {
    closeSync(fd);
  }
}
