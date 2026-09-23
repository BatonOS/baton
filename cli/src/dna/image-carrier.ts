


















import { createHash } from 'node:crypto';
import { BatonError, ExitCode } from '../errors.js';


const CARRIER_KEYWORD = 'baton-dna';

const PNG_SIGNATURE = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);








export function malformed(what: string): BatonError {
  return new BatonError({
    code: 'MALFORMED_IMAGE',
    message: `this image's container is not well formed: ${what}`,
    remediation:
      'Nothing was hashed. A container that cannot be walked has no content hash — ' +
      'returning one anyway would register a value derived from a guess about broken bytes.',
    exitCode: ExitCode.CONFIG,
  });
}

export function unsupported(what: string): BatonError {
  return new BatonError({
    code: 'UNSUPPORTED_IMAGE',
    message: `${what} has no DNA carrier rule yet`,
    remediation:
      'baton.image.v1 covers PNG and JPEG. Other containers are not refused because they ' +
      'cannot work — they have no carrier rule written yet, so a record made now could not ' +
      'be stamped back into the file. Ask for the format rather than working around this.',
    exitCode: ExitCode.UNSUPPORTED,
  });
}


export interface Stripped {
  base: Buffer;
  carriers: string[];
}









function stripPNG(raw: Buffer): Stripped {
  const out: Buffer[] = [PNG_SIGNATURE];
  const carriers: string[] = [];
  let i = PNG_SIGNATURE.length;

  while (i < raw.length) {
    if (i + 8 > raw.length) throw malformed('a chunk header runs past the end of the file');
    const length = raw.readUInt32BE(i);
    const type = raw.subarray(i + 4, i + 8).toString('latin1');
    const end = i + 12 + length;
    if (end > raw.length || end < i) throw malformed(`the ${type} chunk's length runs past the end of the file`);

    const data = raw.subarray(i + 8, i + 8 + length);
    const nul = data.indexOf(0);
    const isCarrier = type === 'iTXt' && nul >= 0 && data.subarray(0, nul).toString('latin1') === CARRIER_KEYWORD;

    if (isCarrier) carriers.push(carrierBodyPNG(data, nul));
    else out.push(raw.subarray(i, end));
    i = end;
  }













  return { base: Buffer.concat(out), carriers };
}










function carrierBodyPNG(data: Buffer, keywordNul: number): string {
  return data.subarray(keywordNul + 5).toString('utf8');
}




const STANDALONE = new Set([0xd8, 0xd9, 0x01, 0xd0, 0xd1, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7]);
const APP11 = 0xeb;
const SOS = 0xda;

function stripJPEG(raw: Buffer): Stripped {
  const out: Buffer[] = [raw.subarray(0, 2)];
  const carriers: string[] = [];
  let i = 2;

  for (;;) {
    if (i >= raw.length) throw malformed('the file ends before the scan begins');
    if (raw[i] !== 0xff) throw malformed('expected a marker and found something else');



    let j = i;
    while (j < raw.length && raw[j] === 0xff) j += 1;
    if (j >= raw.length) throw malformed('the file ends inside a marker');
    const marker = raw[j] as number;

    if (STANDALONE.has(marker)) {
      out.push(raw.subarray(i, j + 1));
      i = j + 1;
      continue;
    }

    if (j + 3 > raw.length) throw malformed("a segment's length runs past the end of the file");
    const length = raw.readUInt16BE(j + 1);
    if (length < 2) throw malformed('a segment declares a length shorter than its own length field');
    const end = j + 1 + length;
    if (end > raw.length) throw malformed('a segment runs past the end of the file');

    const data = raw.subarray(j + 3, end);
    const isCarrier = marker === APP11 && data.subarray(0, CARRIER_KEYWORD.length + 1).toString('latin1') === `${CARRIER_KEYWORD}\0`;

    if (isCarrier) {
      carriers.push(data.subarray(CARRIER_KEYWORD.length + 1).toString('utf8'));





      out.push(raw.subarray(i, j - 1));
    } else {
      out.push(raw.subarray(i, end));
    }
    i = end;




    if (marker === SOS) {
      out.push(raw.subarray(i));
      return { base: Buffer.concat(out), carriers };
    }
  }
}








export function stripCarrier(raw: Buffer): Stripped {
  if (raw.subarray(0, 8).equals(PNG_SIGNATURE)) return stripPNG(raw);
  if (raw.length >= 2 && raw[0] === 0xff && raw[1] === 0xd8) return stripJPEG(raw);
  if (raw.subarray(0, 4).toString('latin1') === 'RIFF' && raw.subarray(8, 12).toString('latin1') === 'WEBP') {




    throw unsupported('WebP');
  }
  throw unsupported('this container');
}



















export function readImageCarrier(raw: Buffer): { token: string; method: string; keyId: string } | null {
  const { carriers } = stripCarrier(raw);
  const body = carriers.length ? carriers[carriers.length - 1] : undefined;
  if (!body) return null;
  const m = /^(\S+) m=(\S+) k=(\S+)$/.exec(body.trim());
  if (!m || !m[1] || !m[2] || !m[3]) return null;
  return { token: m[1], method: m[2], keyId: m[3] };
}












export function imageHash(raw: Buffer): string {
  const { base } = stripCarrier(raw);
  return 'sha256:' + createHash('sha256').update(base).digest('hex');
}
