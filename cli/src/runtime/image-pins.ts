// SPDX-License-Identifier: Apache-2.0




















export const DEFAULT_IMAGES = {
  agent: 'batonos/agent:0.1.0-rc.1@sha256:ba87c68bceb9c42455da36bb8cf493c191a0ecf7ee6becf6e6eb8ce7f47e4c98',
  'control-api': 'batonos/control-api:0.1.0-rc.1@sha256:2c5eb2d1e2a07bc3b55d64afaef25ab1b76d5615464298648c34dde4404ce251',
} as const;


export function tagOf(ref: string): string {
  const noDigest = ref.split('@')[0]!;
  const colon = noDigest.lastIndexOf(':');
  return colon < 0 ? '' : noDigest.slice(colon + 1);
}





export const DEFAULT_IMAGE_TAG = tagOf(DEFAULT_IMAGES.agent);






export function defaultImageRef(role: keyof typeof DEFAULT_IMAGES, version: string): string {
  const pinned = DEFAULT_IMAGES[role];
  if (version === tagOf(pinned)) return pinned;
  return `${pinned.split(':')[0]}:${version}`;
}
