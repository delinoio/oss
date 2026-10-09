// SPDX-License-Identifier: Apache-2.0
import { createPrivateKey, sign } from "node:crypto";
import { Identity } from "./beta.mjs";
const appleOrigin = "https://api.appstoreconnect.apple.com",
  googleOrigin = "https://androidpublisher.googleapis.com";
const encode = (v) =>
  Buffer.from(typeof v === "string" ? v : JSON.stringify(v)).toString(
    "base64url",
  );
export function appleToken(e, now = Math.floor(Date.now() / 1000)) {
  const h = encode({
      alg: "ES256",
      kid: e.DELIDEV_MOBILE_APPLE_KEY_ID,
      typ: "JWT",
    }),
    p = encode({
      iss: e.DELIDEV_MOBILE_APPLE_ISSUER,
      iat: now,
      exp: now + 600,
      aud: "appstoreconnect-v1",
    });
  return `${h}.${p}.${sign("sha256", Buffer.from(`${h}.${p}`), { key: createPrivateKey(e.DELIDEV_MOBILE_APPLE_PRIVATE_KEY), dsaEncoding: "ieee-p1363" }).toString("base64url")}`;
}
export function googleAssertion(account, now = Math.floor(Date.now() / 1000)) {
  const h = encode({ alg: "RS256", typ: "JWT" }),
    p = encode({
      iss: account.client_email,
      scope: "https://www.googleapis.com/auth/androidpublisher",
      aud: "https://oauth2.googleapis.com/token",
      iat: now,
      exp: now + 600,
    });
  return `${h}.${p}.${sign("RSA-SHA256", Buffer.from(`${h}.${p}`), account.private_key).toString("base64url")}`;
}
/** Never include response bodies, URLs, bearer values or raw provider errors in logs. */
async function request(
  fetcher,
  origin,
  path,
  token,
  { method = "GET", body, headers = {} } = {},
) {
  if (!path.startsWith("/") || path.startsWith("//"))
    throw new Error("Invalid provider operation");
  const response = await fetcher(origin + path, {
    method,
    redirect: "error",
    headers: {
      authorization: `Bearer ${token}`,
      ...(body ? { "content-type": "application/json" } : {}),
      ...headers,
    },
    body:
      body === undefined
        ? undefined
        : typeof body === "string"
          ? body
          : JSON.stringify(body),
  });
  if (!response.ok) {
    const error = new Error(`Provider operation failed (${response.status})`);
    error.status = response.status;
    throw error;
  }
  return response.status === 204 ? {} : response.json();
}
function sameUpload(upload, m) {
  return (
    upload?.attributes?.cfBundleShortVersionString === m.version &&
    upload.attributes.cfBundleVersion === m.iosBuild &&
    upload.attributes.platform === "IOS"
  );
}
/** App Store Connect BuildUpload retains the exact original source checksum. */
export function appleProvider(environment, bytes, checkpoint, fetcher = fetch) {
  let token, group;
  const app = environment.DELIDEV_MOBILE_APPLE_APP_ID,
    groupId = environment.DELIDEV_MOBILE_APPLE_INTERNAL_GROUP;
  const api = (path, options) =>
    request(fetcher, appleOrigin, path, token, options);
  async function files(id) {
    return (
      (
        await api(
          `/v1/buildUploads/${encodeURIComponent(id)}/buildUploadFiles?limit=200`,
        )
      ).data ?? []
    );
  }
  async function proof(m, upload) {
    if (!sameUpload(upload, m))
      throw new Error("Apple original version identity mismatch");
    const matching = (await files(upload.id)).filter(
      (f) =>
        f.attributes?.uti === "com.apple.ipa" &&
        f.attributes?.sourceFileChecksums?.file?.algorithm === "SHA_256" &&
        f.attributes.sourceFileChecksums.file.hash === m.artifacts.ios.sha256 &&
        f.attributes.fileSize === m.artifacts.ios.bytes,
    );
    if (matching.length !== 1 || upload.attributes.state !== "COMPLETE")
      return { state: "unknown" };
    const result = await api(
        `/v1/buildUploads/${encodeURIComponent(upload.id)}?include=build`,
      ),
      build = result.included?.find((v) => v.type === "builds");
    if (!build || build.attributes?.buildAudienceType !== "INTERNAL_ONLY")
      return { state: "unknown" };
    const assigned =
      (
        await api(
          `/v1/betaGroups/${encodeURIComponent(groupId)}/builds?limit=200`,
        )
      ).data ?? [];
    return {
      state: "present",
      id: upload.id,
      buildId: build.id,
      sha256: m.artifacts.ios.sha256,
      identity: Identity,
      internal: true,
      distributed: assigned.some((b) => b.id === build.id),
    };
  }
  return {
    async preflight(m) {
      if (
        m.identity !== Identity ||
        m.appleGroupType !== "INTERNAL" ||
        !/^\d+$/.test(app) ||
        !groupId
      )
        throw new Error("Invalid protected Apple target");
      token = appleToken(environment);
      const actual = (await api(`/v1/apps/${encodeURIComponent(app)}`)).data;
      if (actual?.attributes?.bundleId !== Identity)
        throw new Error("Apple app identity mismatch");
      group = (
        await api(`/v1/betaGroups/${encodeURIComponent(groupId)}?include=app`)
      ).data;
      if (
        group?.attributes?.isInternalGroup !== true ||
        group.attributes.publicLinkEnabled === true ||
        group.relationships?.app?.data?.id !== app
      )
        throw new Error("Apple internal group identity mismatch");
    },
    async inspect(m, r) {
      if (r.providerId)
        return proof(
          m,
          (await api(`/v1/buildUploads/${encodeURIComponent(r.providerId)}`))
            .data,
        );
      // Initial POST acknowledgement loss has no handle. Inspect every bounded
      // page and accept only one source-bound upload; never create a replacement.
      const matches = [];
      let path = `/v1/apps/${encodeURIComponent(app)}/buildUploads?limit=200`,
        pages = 0;
      while (path && pages++ < 20) {
        const result = await api(path);
        for (const u of result.data ?? [])
          if (sameUpload(u, m)) matches.push(u);
        const next = result.links?.next;
        if (next) {
          const url = new URL(next);
          if (url.origin !== appleOrigin)
            throw new Error("Invalid Apple pagination");
          path = url.pathname + url.search;
        } else path = "";
      }
      if (path || matches.length !== 1) return { state: "unknown" };
      return proof(m, matches[0]);
    },
    async upload(m, r) {
      // Read the complete bounded inventory before allocating an upload. Do not
      // depend on an undocumented server filter or accept a partial listing.
      let inventory = `/v1/apps/${encodeURIComponent(app)}/buildUploads?limit=200`;
      let pages = 0;
      while (inventory && pages++ < 20) {
        const result = await api(inventory);
        if (
          (result.data ?? []).some(
            (u) =>
              u.attributes?.cfBundleVersion === m.iosBuild &&
              u.attributes?.platform === "IOS",
          )
        )
          throw new Error(
            "Apple version already exists; reconcile its original candidate",
          );
        const next = result.links?.next;
        if (next) {
          const url = new URL(next);
          if (url.origin !== appleOrigin)
            throw new Error("Invalid Apple pagination");
          inventory = url.pathname + url.search;
        } else inventory = "";
      }
      if (inventory) throw new Error("Apple version inventory incomplete");
      const upload = (
        await api("/v1/buildUploads", {
          method: "POST",
          body: {
            data: {
              type: "buildUploads",
              attributes: {
                cfBundleShortVersionString: m.version,
                cfBundleVersion: m.iosBuild,
                platform: "IOS",
              },
              relationships: { app: { data: { type: "apps", id: app } } },
            },
          },
        })
      ).data;
      if (!upload?.id || !sameUpload(upload, m))
        throw new Error("Apple original upload identity missing");
      r.providerId = upload.id;
      await checkpoint({ ...r });
      const file = (
        await api("/v1/buildUploadFiles", {
          method: "POST",
          body: {
            data: {
              type: "buildUploadFiles",
              attributes: {
                assetType: "ASSET",
                fileName: "DeliDev.ipa",
                fileSize: bytes.length,
                uti: "com.apple.ipa",
              },
              relationships: {
                buildUpload: { data: { type: "buildUploads", id: upload.id } },
              },
            },
          },
        })
      ).data;
      if (!file?.id) throw new Error("Apple original file identity missing");
      r.fileId = file.id;
      await checkpoint({ ...r });
      let offset = 0;
      for (const operation of file.attributes?.uploadOperations ?? []) {
        const url = new URL(operation.url),
          length = operation.length;
        if (
          url.protocol !== "https:" ||
          url.username ||
          url.password ||
          operation.offset !== offset ||
          !Number.isSafeInteger(length) ||
          length < 1 ||
          offset + length > bytes.length ||
          operation.method !== "PUT"
        )
          throw new Error("Invalid Apple upload descriptor");
        // Apple's descriptor selects its storage origin. Never forward the API
        // bearer: only the original descriptor headers authorize this byte chunk.
        const headers = Object.fromEntries(
          (operation.requestHeaders ?? []).map((h) => [h.name, h.value]),
        );
        if (
          Object.keys(headers).some(
            (h) =>
              h.toLowerCase() === "authorization" &&
              headers[h] === `Bearer ${token}`,
          )
        )
          throw new Error("Invalid Apple upload authorization");
        const response = await fetcher(url, {
          method: "PUT",
          headers,
          body: bytes.subarray(offset, offset + length),
          redirect: "error",
        });
        if (!response.ok) throw new Error("Apple byte upload uncertain");
        offset += length;
      }
      if (offset !== bytes.length)
        throw new Error("Apple upload descriptor coverage incomplete");
      await api(`/v1/buildUploadFiles/${encodeURIComponent(file.id)}`, {
        method: "PATCH",
        body: {
          data: {
            type: "buildUploadFiles",
            id: file.id,
            attributes: {
              uploaded: true,
              sourceFileChecksums: {
                file: { algorithm: "SHA_256", hash: m.artifacts.ios.sha256 },
              },
            },
          },
        },
      });
      // Processing may take longer than this invocation. Unknown state keeps the
      // retained original upload; a future explicit resume performs inspection.
      return proof(
        m,
        (await api(`/v1/buildUploads/${encodeURIComponent(upload.id)}`)).data,
      );
    },
    async assignInternal(m, r) {
      const original = await this.inspect(m, r);
      if (original.state !== "present")
        throw new Error("Apple upload processing remains unresolved");
      await api(
        `/v1/betaGroups/${encodeURIComponent(groupId)}/relationships/builds`,
        {
          method: "POST",
          body: { data: [{ type: "builds", id: original.buildId }] },
        },
      );
    },
  };
}
export function googleProvider(
  environment,
  bytes,
  checkpoint,
  fetcher = fetch,
) {
  let token;
  const packagePath = `/androidpublisher/v3/applications/${Identity}`,
    api = (path, options) =>
      request(fetcher, googleOrigin, path, token, options);
  async function editor(r) {
    if (r.editId) return r.editId;
    const result = await api(`${packagePath}/edits`, {
      method: "POST",
      body: {},
    });
    if (!result.id) throw new Error("Google original edit identity missing");
    r.editId = result.id;
    await checkpoint({ ...r });
    return result.id;
  }
  async function inspect(m, r) {
    const id = await editor(r),
      path = `${packagePath}/edits/${encodeURIComponent(id)}`;
    let bundles;
    try {
      bundles = (await api(`${path}/bundles`)).bundles ?? [];
    } catch (error) {
      if (error.status !== 404 || !r.editId || r.previousEditId) throw error;
      // A committed/expired edit is no longer readable. A new read-only edit
      // observes published state; it never replaces the original artifact.
      r.previousEditId = r.editId;
      delete r.editId;
      await checkpoint({ ...r });
      return inspect(m, r);
    }
    const matching = bundles.filter(
      (b) => String(b.versionCode) === m.androidCode,
    );
    if (matching.length !== 1) return { state: "unknown" };
    const b = matching[0];
    if (b.sha256 !== m.artifacts.android.sha256)
      throw new Error("Google version artifact conflict");
    const track = await api(`${path}/tracks/internal`),
      distributed = (track.releases ?? []).some(
        (v) =>
          v.status === "completed" &&
          (v.versionCodes ?? []).map(String).includes(m.androidCode),
      );
    return {
      state: "present",
      id: m.androidCode,
      sha256: b.sha256,
      identity: Identity,
      internal: true,
      distributed,
    };
  }
  return {
    async preflight(m) {
      if (m.identity !== Identity || m.playTrack !== "internal")
        throw new Error("Invalid Google internal target");
      const account = JSON.parse(
        environment.DELIDEV_MOBILE_GOOGLE_SERVICE_ACCOUNT,
      );
      if (
        account.client_email !== environment.DELIDEV_MOBILE_GOOGLE_PRINCIPAL ||
        account.token_uri !== "https://oauth2.googleapis.com/token"
      )
        throw new Error("Google protected account identity mismatch");
      const response = await fetcher(account.token_uri, {
        method: "POST",
        redirect: "error",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({
          grant_type: "urn:ietf:params:oauth:grant-type:jwt-bearer",
          assertion: googleAssertion(account),
        }),
      });
      if (!response.ok) throw new Error("Google authorization failed");
      token = (await response.json()).access_token;
      if (typeof token !== "string" || !token)
        throw new Error("Google authorization missing");
    },
    inspect,
    async upload(m, r) {
      const id = await editor(r),
        path = `${packagePath}/edits/${encodeURIComponent(id)}`;
      const existing = (await api(`${path}/bundles`)).bundles ?? [];
      if (existing.some((b) => String(b.versionCode) === m.androidCode))
        throw new Error(
          "Google version exists; reconcile the original candidate",
        );
      const response = await fetcher(
        `${googleOrigin}/upload${path}/bundles?uploadType=media`,
        {
          method: "POST",
          redirect: "error",
          headers: {
            authorization: `Bearer ${token}`,
            "content-type": "application/octet-stream",
          },
          body: bytes,
        },
      );
      if (!response.ok) throw new Error("Google original upload uncertain");
      const result = await response.json();
      if (
        String(result.versionCode) !== m.androidCode ||
        result.sha256 !== m.artifacts.android.sha256
      )
        throw new Error("Google upload proof mismatch");
      return {
        state: "present",
        id: m.androidCode,
        sha256: result.sha256,
        identity: Identity,
        internal: true,
        distributed: false,
      };
    },
    async assignInternal(m, r) {
      const id = await editor(r),
        path = `${packagePath}/edits/${encodeURIComponent(id)}`;
      await api(`${path}/tracks/internal`, {
        method: "PUT",
        body: {
          track: "internal",
          releases: [
            {
              name: `${m.version} (${m.androidCode})`,
              status: "completed",
              versionCodes: [m.androidCode],
            },
          ],
        },
      });
      await api(`${path}:validate`, { method: "POST", body: {} });
      await api(`${path}:commit`, { method: "POST", body: {} });
      delete r.editId;
      await checkpoint({ ...r });
    },
  };
}
