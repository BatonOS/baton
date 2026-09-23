

















import { createHash } from 'node:crypto';
import { BatonError, ExitCode, preconditionError } from '../errors.js';


const PACKAGE_TYPES = new Set(['skill', 'template', 'workflow']);
const DOCUMENT_TYPES = new Set(['knowledge', 'policy', 'other']);










const IMAGE_TYPES = new Set(['image']);

export type HashMethod = 'baton.zip.v1' | 'baton.text.v1' | 'baton.image.v1';








export function methodFor(type: string): HashMethod {
  if (PACKAGE_TYPES.has(type)) return 'baton.zip.v1';
  if (DOCUMENT_TYPES.has(type)) return 'baton.text.v1';
  if (IMAGE_TYPES.has(type)) return 'baton.image.v1';
  throw new Error(
    `no DNA hashing channel for resource type "${type}" — ` +
      `packages are ${[...PACKAGE_TYPES].join('/')}, documents are ${[...DOCUMENT_TYPES].join('/')}, ` +
      `images are ${[...IMAGE_TYPES].join('/')}`,
  );
}


















function carrierRe(keyword: string): RegExp {
  return new RegExp(
    `(?:^|(?<=[\\r\\n]))<!-- ${keyword}: [^\\r\\n]*-->[ \\t]*(?=[\\r\\n]|$)`,
    'g',
  );
}

const CARRIER_RE = carrierRe('baton-dna');












const CODE_RE = carrierRe('baton-code');
























export function carrierLine(token: string, method: HashMethod, keyId: string): string {
  return `<!-- baton-dna: ${token} m=${method} k=${keyId} -->`;
}









export function readCarrier(text: string): { token: string; method: string; keyId: string } | null {
  const matches = [...text.matchAll(CARRIER_RE)];
  if (matches.length === 0) return null;
  const line = matches[matches.length - 1]?.[0] ?? '';
  const m = /dna:(\S+) m=(\S+) k=(\S+)/.exec(line);
  if (!m || !m[1] || !m[2] || !m[3]) return null;
  return { token: `dna:${m[1]}`, method: m[2], keyId: m[3] };
}



























function refuseNonText(buf: Buffer): void {
  const nul = buf.indexOf(0);
  if (nul !== -1) throw notText(`a NUL byte at offset ${nul}`);
  try {
    new TextDecoder('utf-8', { fatal: true }).decode(buf);
  } catch {
    throw notText('bytes that are not valid UTF-8');
  }
}

function notText(what: string): BatonError {
  return new BatonError({
    code: 'NOT_A_TEXT_DOCUMENT',
    message: `the document channel (baton.text.v1) was given ${what}`,
    remediation:
      'This channel normalises line endings, so it can only hash text. Register binary content ' +
      'as a package (a directory, --type skill/template/workflow), which hashes bytes verbatim. ' +
      'Nothing was registered and the file was not stamped.',
    exitCode: ExitCode.CONFIG,
  });
}

export function documentHash(input: Buffer | string): string {
  let buf = Buffer.isBuffer(input) ? input : Buffer.from(input, 'utf8');
  refuseNonText(buf);



  if (buf.length >= 3 && buf[0] === 0xef && buf[1] === 0xbb && buf[2] === 0xbf) {
    buf = buf.subarray(3);
  }
  let text = buf.toString('utf8');



  text = text.replace(CARRIER_RE, '');


  text = text.replace(/\r\n/g, '\n').replace(/\r/g, '\n');




  text = text.replace(/[\n \t]+$/, '') + '\n';

  return 'sha256:' + createHash('sha256').update(Buffer.from(text, 'utf8')).digest('hex');
}


export interface PackageEntry {
  path: string;
  content: Buffer;
}








export function packageHash(entries: PackageEntry[]): string {
  const leaves = entries



    .filter((e) => !e.path.endsWith('/'))


    .filter((e) => e.path !== '.baton/dna.json')
    .sort((a, b) => Buffer.compare(Buffer.from(a.path, 'utf8'), Buffer.from(b.path, 'utf8')))


    .map((e) => Buffer.concat([Buffer.from(e.path, 'utf8'), Buffer.from([0x1f]), e.content]));

  return 'sha256:' + merkleRoot(leaves).toString('hex');
}














function merkleRoot(leaves: Buffer[]): Buffer {
  if (leaves.length === 0) return createHash('sha256').update(Buffer.alloc(0)).digest();
  if (leaves.length === 1) {
    return createHash('sha256').update(Buffer.concat([Buffer.from([0x00]), leaves[0]!])).digest();
  }
  let k = 1;
  while (k * 2 < leaves.length) k *= 2;
  const left = merkleRoot(leaves.slice(0, k));
  const right = merkleRoot(leaves.slice(k));
  return createHash('sha256').update(Buffer.concat([Buffer.from([0x01]), left, right])).digest();
}


export const __rfc6962 = { merkleRoot };


























export function skillContentDigest(raw: Buffer | string): string {
  const text = (Buffer.isBuffer(raw) ? raw.toString('utf8') : raw).replace(CODE_RE, '');
  return documentHash(text);
}


export function readSkillCode(raw: Buffer | string): string | null {
  const text = Buffer.isBuffer(raw) ? raw.toString('utf8') : raw;
  const all = [...text.matchAll(CODE_RE)];
  const line = all.length ? (all[all.length - 1]?.[0] ?? '') : '';
  const m = /<!-- baton-code: (.*?)-->/.exec(line);
  return m && m[1] ? m[1].trim() : null;
}






























export function injectSkillCode(raw: Buffer | string, code: string): string {
  const text = Buffer.isBuffer(raw) ? raw.toString('utf8') : raw;
  if (/[\r\n]/.test(code)) {
    throw preconditionError(
      'a skill-code block cannot contain a line break',
      'The block is one line by construction; a newline inside it would split it into ' +
        'something the carrier pattern reads as two different lines, one of which is not a block.',
    );
  }
  const before = skillContentDigest(text);
  const body = text.replace(CODE_RE, '');
  const out = `${body.endsWith('\n') || body === '' ? body : `${body}\n`}<!-- baton-code: ${code} -->\n`;

  const after = skillContentDigest(out);
  if (after !== before) {
    throw preconditionError(
      `injecting the skill code moved content_digest (${before} -> ${after})`,
      'Nothing was written. The code about to be minted binds the digest of this file with the ' +
        'block removed, so a digest that moves means the code would describe a file that does ' +
        'not exist. This is a defect in the injector, not in the skill.',
    );
  }
  return out;
}
