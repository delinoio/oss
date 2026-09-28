import { setTimeout as delay } from 'node:timers/promises';
import { log, origin } from './model.mjs';

const maxAttempts = 3;

// The public custom domain can briefly fail or serve a prior mutable response
// after an R2 write. Retry only this readback; completion still requires exact
// bytes. Remove the retries if the public verification path gains a stronger
// delivery guarantee.
export async function verifyPublicObject(key, expected, { fetcher = fetch, pause = delay, context = {} } = {}) {
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    let code;
    let status;
    let retryable = true;
    try {
      const response = await fetcher(`${origin}/${key}`, {
        redirect: 'error', signal: AbortSignal.timeout(120000), cache: 'no-store',
      });
      if (!response.ok) {
        code = 'PUBLIC_READBACK_HTTP_FAILED';
        status = response.status;
        retryable = response.status === 408 || response.status === 429 || response.status >= 500;
      } else if (!Buffer.from(await response.arrayBuffer()).equals(expected)) {
        code = 'PUBLIC_READBACK_MISMATCH';
      } else {
        return;
      }
    } catch {
      // Fetch and response-body failures can be transient. Keep their raw errors out
      // of release logs because platform error messages may contain request details.
      code = 'PUBLIC_READBACK_TRANSPORT_FAILED';
    }
    const fields = { ...context, key, attempt, code, ...(status === undefined ? {} : { status }) };
    if (!retryable || attempt === maxAttempts) {
      log('public-readback-failed', fields);
      throw new Error(code);
    }
    log('public-readback-retry', fields);
    await pause(1000 * attempt);
  }
}
