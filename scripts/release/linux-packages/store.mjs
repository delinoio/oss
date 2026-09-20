import { mkdir, readFile, writeFile, rename } from 'node:fs/promises';
import path from 'node:path';
import { pins, sha256, safeKey, requireValue } from './model.mjs';

export const Cache = Object.freeze({ Immutable: 'public, max-age=31536000, immutable', Mutable: 'no-store' });
export class FileStore {
  constructor(root) { this.root = root; }
  async get(key) {
    try { const body = await readFile(path.join(this.root, safeKey(key))); return { body, etag: sha256(body) }; }
    catch (error) { if (error.code === 'ENOENT') return null; throw error; }
  }
  async put(key, body, { immutable = false, expectedETag } = {}) {
    const existing = await this.get(key);
    if (immutable && existing) { requireValue(existing.body.equals(body), 'IMMUTABLE_OBJECT_CONFLICT'); return; }
    if (expectedETag !== undefined) requireValue((existing?.etag ?? null) === expectedETag, 'CONCURRENT_PUBLICATION');
    const target = path.join(this.root, safeKey(key));
    await mkdir(path.dirname(target), { recursive: true });
    // FileStore is an isolated, single-writer fixture; production uses S3 conditional writes.
    const temporary = `${target}.${process.pid}.tmp`;
    await writeFile(temporary, body);
    await rename(temporary, target);
  }
}
export class R2Store {
  static async create(kind, environment = process.env) {
    requireValue(['public', 'state'].includes(kind), 'INVALID_STORE_KIND');
    const account = environment.LINUX_PACKAGES_R2_ACCOUNT_ID;
    requireValue(/^[a-f0-9]{32}$/u.test(account ?? ''), 'INVALID_R2_ACCOUNT');
    const accessKeyId = environment.LINUX_PACKAGES_R2_ACCESS_KEY_ID;
    const secretAccessKey = environment.LINUX_PACKAGES_R2_SECRET_ACCESS_KEY;
    requireValue(accessKeyId && secretAccessKey, 'MISSING_R2_CREDENTIALS');
    const sdk = await import('@aws-sdk/client-s3');
    const client = new sdk.S3Client({ region: 'auto', endpoint: `https://${account}.r2.cloudflarestorage.com`, credentials: { accessKeyId, secretAccessKey }, requestChecksumCalculation: 'WHEN_REQUIRED', responseChecksumValidation: 'WHEN_REQUIRED', maxAttempts: 4 });
    return new R2Store(client, sdk, kind === 'public' ? pins.public_bucket : pins.state_bucket);
  }
  constructor(client, sdk, bucket) { Object.assign(this, { client, sdk, bucket }); }
  async get(key) {
    try {
      const result = await this.client.send(new this.sdk.GetObjectCommand({ Bucket: this.bucket, Key: safeKey(key) }), { abortSignal: AbortSignal.timeout(120000) });
      requireValue(result.ContentLength <= 512 * 1024 * 1024, 'OBJECT_TOO_LARGE');
      return { body: Buffer.from(await result.Body.transformToByteArray()), etag: result.ETag };
    } catch (error) { if (error.$metadata?.httpStatusCode === 404) return null; throw new Error('R2_READ_FAILED'); }
  }
  async put(key, body, { immutable = false, expectedETag, cache = Cache.Mutable } = {}) {
    safeKey(key);
    if (immutable) {
      const existing = await this.get(key);
      if (existing) { requireValue(existing.body.equals(body), 'IMMUTABLE_OBJECT_CONFLICT'); return; }
    }
    try {
      await this.client.send(new this.sdk.PutObjectCommand({ Bucket: this.bucket, Key: key, Body: body, ContentLength: body.length,
        ContentType: key.endsWith('.json') ? 'application/json' : key.endsWith('.gz') ? 'application/gzip' : 'application/octet-stream',
        CacheControl: cache, Metadata: { sha256: sha256(body) },
        ...(immutable || expectedETag === null ? { IfNoneMatch: '*' } : {}),
        ...(typeof expectedETag === 'string' ? { IfMatch: expectedETag } : {}),
      }), { abortSignal: AbortSignal.timeout(120000) });
    } catch (error) {
      if (error.$metadata?.httpStatusCode === 412) {
        const current = await this.get(key);
        if (immutable && current?.body.equals(body)) return;
        throw new Error('CONCURRENT_PUBLICATION');
      }
      throw new Error('R2_WRITE_FAILED');
    }
    const written = await this.get(key);
    requireValue(written?.body.equals(body), 'R2_WRITE_VERIFICATION_FAILED');
  }
}
