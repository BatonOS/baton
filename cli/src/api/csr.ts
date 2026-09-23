








































import { generateKeyPairSync, sign, type KeyObject } from 'node:crypto';


function tlv(tag: number, value: Uint8Array): Uint8Array {
  const len = value.length;
  let header: number[];
  if (len < 0x80) {
    header = [tag, len];
  } else {

    const bytes: number[] = [];
    for (let n = len; n > 0; n = Math.floor(n / 256)) bytes.unshift(n % 256);
    header = [tag, 0x80 | bytes.length, ...bytes];
  }
  const out = new Uint8Array(header.length + len);
  out.set(header, 0);
  out.set(value, header.length);
  return out;
}

function concat(parts: Uint8Array[]): Uint8Array {
  const total = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(total);
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

const SEQUENCE = 0x30;
const SET = 0x31;
const INTEGER = 0x02;
const BIT_STRING = 0x03;
const OID = 0x06;
const UTF8_STRING = 0x0c;
const CONTEXT_0_CONSTRUCTED = 0xa0;
const OCTET_STRING = 0x04;

const CONTEXT_2_PRIMITIVE = 0x82;









const OID_COMMON_NAME = new Uint8Array([0x55, 0x04, 0x03]);
const OID_EXTENSION_REQUEST = new Uint8Array([
  0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x09, 0x0e,
]);
const OID_SUBJECT_ALT_NAME = new Uint8Array([0x55, 0x1d, 0x11]);
const OID_ECDSA_SHA256 = new Uint8Array([
  0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x02,
]);

function commonName(cn: string): Uint8Array {
  const attr = tlv(SEQUENCE, concat([
    tlv(OID, OID_COMMON_NAME),
    tlv(UTF8_STRING, new TextEncoder().encode(cn)),
  ]));

  return tlv(SEQUENCE, tlv(SET, attr));
}












function extensionRequestSan(dnsName: string): Uint8Array {
  const generalNames = tlv(SEQUENCE, tlv(CONTEXT_2_PRIMITIVE, new TextEncoder().encode(dnsName)));
  const extension = tlv(SEQUENCE, concat([
    tlv(OID, OID_SUBJECT_ALT_NAME),
    tlv(OCTET_STRING, generalNames),
  ]));
  return tlv(SEQUENCE, concat([
    tlv(OID, OID_EXTENSION_REQUEST),
    tlv(SET, tlv(SEQUENCE, extension)),
  ]));
}

function pem(label: string, der: Uint8Array): string {
  const b64 = Buffer.from(der).toString('base64');
  const lines = b64.match(/.{1,64}/g) ?? [];
  return `-----BEGIN ${label}-----\n${lines.join('\n')}\n-----END ${label}-----\n`;
}

export interface KeyAndRequest {

  privateKeyPem: string;

  csrPem: string;
}














export function generateKeyAndCsr(commonNameValue: string, sanDns?: string): KeyAndRequest {
  const { privateKey, publicKey } = generateKeyPairSync('ec', {
    namedCurve: 'prime256v1',
  });


  const spki = new Uint8Array(publicKey.export({ type: 'spki', format: 'der' }));

  const info = tlv(SEQUENCE, concat([
    tlv(INTEGER, new Uint8Array([0])),
    commonName(commonNameValue),
    spki,


    tlv(CONTEXT_0_CONSTRUCTED, sanDns ? extensionRequestSan(sanDns) : new Uint8Array()),
  ]));

  const signature = signDer(info, privateKey);

  const csr = tlv(SEQUENCE, concat([
    info,









    tlv(SEQUENCE, tlv(OID, OID_ECDSA_SHA256)),


    tlv(BIT_STRING, concat([new Uint8Array([0]), signature])),
  ]));

  return {
    privateKeyPem: privateKey.export({ type: 'pkcs8', format: 'pem' }).toString(),
    csrPem: pem('CERTIFICATE REQUEST', csr),
  };
}

function signDer(tbs: Uint8Array, key: KeyObject): Uint8Array {




  return new Uint8Array(sign('sha256', tbs, { key, dsaEncoding: 'der' }));
}
