





















import { PNG } from 'pngjs';
import jpeg from 'jpeg-js';
import { malformed, unsupported } from './image-carrier.js';


export interface Raster {
  width: number;
  height: number;

  data: Buffer;
}

const PNG_SIGNATURE = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);










export function decode(raw: Buffer): Raster {
  if (raw.subarray(0, 8).equals(PNG_SIGNATURE)) {
    try {
      const png = PNG.sync.read(raw);
      return { width: png.width, height: png.height, data: Buffer.from(png.data) };
    } catch (cause) {
      throw malformed(`this PNG could not be decoded (${(cause as Error).message})`);
    }
  }
  if (raw.length >= 2 && raw[0] === 0xff && raw[1] === 0xd8) {
    try {



      const img = jpeg.decode(raw, { useTArray: true });
      return { width: img.width, height: img.height, data: Buffer.from(img.data) };
    } catch (cause) {
      throw malformed(`this JPEG could not be decoded (${(cause as Error).message})`);
    }
  }
  throw unsupported('this container');
}


























export function toGrey(img: Raster): Uint8Array {
  const out = new Uint8Array(img.width * img.height);
  for (let i = 0, p = 0; p < out.length; i += 4, p += 1) {
    const r = img.data[i] as number;
    const g = img.data[i + 1] as number;
    const b = img.data[i + 2] as number;
    out[p] = (r * 19595 + g * 38470 + b * 7471 + 0x8000) >> 16;
  }
  return out;
}



























const BICUBIC_A = -0.5;
const BICUBIC_SUPPORT = 2.0;

function bicubicKernel(t: number): number {
  const x = Math.abs(t);
  if (x < 1) return ((BICUBIC_A + 2) * x - (BICUBIC_A + 3)) * x * x + 1;
  if (x < 2) return (((x - 5) * x + 8) * x - 4) * BICUBIC_A;
  return 0;
}


function filterWeights(inSize: number, outSize: number): { start: number; weights: number[] }[] {
  const scale = inSize / outSize;



  const filterScale = Math.max(1, scale);
  const support = BICUBIC_SUPPORT * filterScale;
  const out: { start: number; weights: number[] }[] = [];
  for (let i = 0; i < outSize; i += 1) {
    const center = (i + 0.5) * scale;
    const start = Math.max(0, Math.floor(center - support + 0.5));
    const stop = Math.min(inSize, Math.floor(center + support + 0.5));
    const weights: number[] = [];
    let total = 0;
    for (let x = start; x < stop; x += 1) {
      const w = bicubicKernel((x - center + 0.5) / filterScale);
      weights.push(w);
      total += w;
    }
    if (total !== 0) for (let k = 0; k < weights.length; k += 1) weights[k] = (weights[k] as number) / total;
    out.push({ start, weights });
  }
  return out;
}

const clamp8 = (v: number): number => {
  const r = Math.round(v);
  return r < 0 ? 0 : r > 255 ? 255 : r;
};

export function resizeGrey(
  src: Uint8Array,
  width: number,
  height: number,
  outW: number,
  outH: number,
): Uint8Array {
  const h = filterWeights(width, outW);
  const v = filterWeights(height, outH);













  const mid = new Uint8Array(outW * height);
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < outW; x += 1) {
      const { start, weights } = h[x] as { start: number; weights: number[] };
      let acc = 0;
      for (let k = 0; k < weights.length; k += 1) acc += (src[y * width + start + k] as number) * (weights[k] as number);
      mid[y * outW + x] = clamp8(acc);
    }
  }
  const out = new Uint8Array(outW * outH);
  for (let y = 0; y < outH; y += 1) {
    const { start, weights } = v[y] as { start: number; weights: number[] };
    for (let x = 0; x < outW; x += 1) {
      let acc = 0;
      for (let k = 0; k < weights.length; k += 1) acc += (mid[(start + k) * outW + x] as number) * (weights[k] as number);
      out[y * outW + x] = clamp8(acc);
    }
  }
  return out;
}
