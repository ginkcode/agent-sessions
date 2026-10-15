/**
 * Images attached to session messages: which attachments are shown as
 * pictures, and a cache of the ones loaded.
 */

import type { BlobResponse, FileRef } from './types';

export type ImageFormat = 'png' | 'jpeg' | 'gif' | 'webp';

const MIME: Record<ImageFormat, string> = {
  png: 'image/png',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
};

/**
 * Whether an attachment may be an image to show: a PNG, JPEG, GIF or WebP
 * by its type, or by its name when it has no type. SVG stays an attachment.
 */
export function isImageFile(file: FileRef): boolean {
  const mime = file.mime?.toLowerCase().split(';')[0].trim();
  if (mime) return mime === 'image/jpg' || Object.values(MIME).includes(mime);
  return /\.(png|jpe?g|gif|webp)$/i.test(file.name || file.path || '');
}

/**
 * The format of an image in base64 by its first bytes, or null when it is
 * none of those shown. The bytes decide, not the type the session claims.
 */
export function sniffImageFormat(base64: string): ImageFormat | null {
  let head: string;
  try {
    head = atob(base64.slice(0, 24));
  } catch {
    return null;
  }
  if (head.startsWith('\x89PNG\r\n\x1a\n')) return 'png';
  if (head.startsWith('\xff\xd8\xff')) return 'jpeg';
  if (head.startsWith('GIF87a') || head.startsWith('GIF89a')) return 'gif';
  if (head.startsWith('RIFF') && head.slice(8, 12) === 'WEBP') return 'webp';
  return null;
}

/** A loaded image. */
export interface LoadedImage {
  /** A data: URL. */
  url: string;
  /** The bytes in base64. */
  data: string;
  format: ImageFormat;
}

/** The image in a blob, or an error saying why it can't be shown. */
export function blobImage(blob: BlobResponse): LoadedImage {
  const format = blob.isBinary ? sniffImageFormat(blob.data) : null;
  if (!format) throw new Error('The attachment is not a PNG, JPEG, GIF or WebP image.');
  return { url: `data:${MIME[format]};base64,${blob.data}`, data: blob.data, format };
}

/** Bytes in base64 as a blob of the given type. */
export function base64Blob(base64: string, type: string): Blob {
  const bin = atob(base64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new Blob([bytes], { type });
}

/**
 * Loaded images by key, the newest kept up to a total size, so scrolling
 * back to a message doesn't fetch its images again. Loads of the same key
 * share one request.
 */
export class ImageCache {
  private images = new Map<string, LoadedImage>();
  private pending = new Map<string, Promise<LoadedImage>>();
  private size = 0;
  private readonly maxSize: number;

  constructor(maxSize = 96 << 20) {
    this.maxSize = maxSize;
  }

  get(key: string): LoadedImage | undefined {
    const image = this.images.get(key);
    if (image) {
      // Most recently used last.
      this.images.delete(key);
      this.images.set(key, image);
    }
    return image;
  }

  load(key: string, fetch: () => Promise<BlobResponse>): Promise<LoadedImage> {
    const cached = this.get(key);
    if (cached) return Promise.resolve(cached);
    let request = this.pending.get(key);
    if (!request) {
      request = fetch()
        .then((blob) => {
          const image = blobImage(blob);
          this.put(key, image);
          return image;
        })
        .finally(() => this.pending.delete(key));
      this.pending.set(key, request);
    }
    return request;
  }

  clear(): void {
    this.images.clear();
    this.size = 0;
  }

  private put(key: string, image: LoadedImage): void {
    if (image.data.length > this.maxSize) return;
    this.images.set(key, image);
    this.size += image.data.length;
    for (const [old, dropped] of this.images) {
      if (this.size <= this.maxSize) break;
      this.images.delete(old);
      this.size -= dropped.data.length;
    }
  }
}

// Canvas limits: WebKit refuses a canvas over 16M pixels, and every engine
// one wider or taller than 16384.
const MAX_CANVAS_PIXELS = 16_777_216;
const MAX_CANVAS_SIDE = 16_384;

/** The scale to draw a width x height image at: wanted, or less to fit a canvas. */
export function rasterScale(width: number, height: number, wanted: number): number {
  const byArea = Math.sqrt(MAX_CANVAS_PIXELS / (width * height));
  const bySide = MAX_CANVAS_SIDE / Math.max(width, height);
  return Math.min(wanted, byArea, bySide);
}

/**
 * The image at url as a PNG, width x height at scale, or smaller to fit a
 * canvas.
 */
export async function rasterPng(url: string, width: number, height: number, scale = 1): Promise<Blob> {
  const img = new Image();
  img.src = url;
  await img.decode();
  const k = rasterScale(width, height, scale);
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.floor(width * k));
  canvas.height = Math.max(1, Math.floor(height * k));
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('no canvas to draw the image on');
  ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
  return await new Promise<Blob>((resolve, reject) =>
    canvas.toBlob((blob) => (blob ? resolve(blob) : reject(new Error('the image could not be encoded'))), 'image/png')
  );
}
