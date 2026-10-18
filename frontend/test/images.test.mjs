import test from 'node:test';
import assert from 'node:assert/strict';
import { ImageCache, base64Blob, blobImage, isImageFile, rasterScale, sniffImageFormat } from '../src/lib/images.ts';

test('rasterScale keeps a PNG within the canvas limits', () => {
  assert.equal(rasterScale(800, 600, 2), 2);
  // 4000 x 4000 at 2x would be 64M pixels; WebKit takes 16M.
  assert.equal(rasterScale(4000, 4000, 2), 1.024);
  // A very wide diagram is held to 16384 pixels across.
  assert.equal(rasterScale(20000, 100, 2), 16384 / 20000);
});

const PNG_B64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==';

test('isImageFile shows PNG, JPEG, GIF and WebP attachments only', () => {
  assert.equal(isImageFile({ mime: 'image/png' }), true);
  assert.equal(isImageFile({ mime: 'IMAGE/JPEG; q=1' }), true);
  assert.equal(isImageFile({ mime: 'image/jpg' }), true);
  assert.equal(isImageFile({ mime: 'image/webp', name: 'x.txt' }), true);
  assert.equal(isImageFile({ mime: 'image/svg+xml' }), false);
  assert.equal(isImageFile({ mime: 'application/pdf', name: 'a.png' }), false);
  assert.equal(isImageFile({ name: 'Shot.GIF' }), true);
  assert.equal(isImageFile({ path: '/tmp/a.jpeg' }), true);
  assert.equal(isImageFile({ name: 'a.svg' }), false);
  assert.equal(isImageFile({}), false);
});

test('sniffImageFormat goes by the first bytes', () => {
  const b64 = (s) => Buffer.from(s, 'latin1').toString('base64');
  assert.equal(sniffImageFormat(PNG_B64), 'png');
  assert.equal(sniffImageFormat(b64('\xff\xd8\xff\xe0 rest of it')), 'jpeg');
  assert.equal(sniffImageFormat(b64('GIF87a rest')), 'gif');
  assert.equal(sniffImageFormat(b64('RIFF\x10\x00\x00\x00WEBPVP8 ')), 'webp');
  assert.equal(sniffImageFormat(b64('<svg></svg>')), null);
  assert.equal(sniffImageFormat('not base64!'), null);
  assert.equal(sniffImageFormat(''), null);
});

test('blobImage makes a data URL from a binary blob and refuses others', () => {
  const image = blobImage({ data: PNG_B64, mime: 'application/octet-stream', isBinary: true });
  assert.deepEqual(image, { url: `data:image/png;base64,${PNG_B64}`, data: PNG_B64, format: 'png' });
  assert.throws(() => blobImage({ data: PNG_B64, mime: 'text/plain', isBinary: false }), /not a PNG/);
  assert.throws(() => blobImage({ data: 'aGVsbG8=', mime: 'application/octet-stream', isBinary: true }), /not a PNG/);
});

test('base64Blob holds the decoded bytes', async () => {
  const blob = base64Blob(PNG_B64, 'image/png');
  assert.equal(blob.type, 'image/png');
  assert.deepEqual(Buffer.from(await blob.arrayBuffer()), Buffer.from(PNG_B64, 'base64'));
});

test('ImageCache shares a request per key and drops the least recently used', async () => {
  const blob = { data: PNG_B64, mime: 'application/octet-stream', isBinary: true };
  const cache = new ImageCache(PNG_B64.length * 2);
  let fetches = 0;
  const fetch = async () => {
    fetches++;
    return blob;
  };
  const [a, b] = await Promise.all([cache.load('a', fetch), cache.load('a', fetch)]);
  assert.equal(fetches, 1);
  assert.equal(a, b);
  assert.equal(cache.get('a'), a);

  await cache.load('b', fetch);
  cache.get('a'); // a is now the most recent
  await cache.load('c', fetch);
  assert.equal(fetches, 3);
  assert.ok(cache.get('a'));
  assert.equal(cache.get('b'), undefined);
  assert.ok(cache.get('c'));

  // A failed load isn't kept, so it can be tried again.
  await assert.rejects(cache.load('d', async () => ({ ...blob, data: 'aGVsbG8=' })), /not a PNG/);
  await cache.load('d', fetch);
  assert.ok(cache.get('d'));

  cache.clear();
  assert.equal(cache.get('d'), undefined);
});
