// SPDX-License-Identifier: Apache-2.0
import { createSign } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const packageName = "io.delino.delitools";
const base = `https://androidpublisher.googleapis.com/androidpublisher/v3/applications/${packageName}/edits`;
export async function publishAndroid(
  artifact,
  buildNumber,
  credentials,
  request = fetch,
) {
  if (!/^[1-9][0-9]*$/.test(buildNumber))
    throw new Error("Invalid build number");
  const encode = (value) =>
    Buffer.from(JSON.stringify(value)).toString("base64url");
  const now = Math.floor(Date.now() / 1000);
  const unsigned = `${encode({ alg: "RS256", typ: "JWT" })}.${encode({ iss: credentials.client_email, scope: "https://www.googleapis.com/auth/androidpublisher", aud: "https://oauth2.googleapis.com/token", iat: now, exp: now + 3600 })}`;
  const signature = createSign("RSA-SHA256")
    .update(unsigned)
    .sign(credentials.private_key, "base64url");
  const tokenResponse = await request("https://oauth2.googleapis.com/token", {
    method: "POST",
    body: new URLSearchParams({
      grant_type: "urn:ietf:params:oauth:grant-type:jwt-bearer",
      assertion: `${unsigned}.${signature}`,
    }),
  });
  if (!tokenResponse.ok)
    throw new Error(`Google authentication failed (${tokenResponse.status})`);
  const { access_token: token } = await tokenResponse.json();
  if (!token) throw new Error("Google authentication returned no access token");
  const call = async (url, method, body, type = "application/json") => {
    const response = await request(url, {
      method,
      headers: { Authorization: `Bearer ${token}`, "Content-Type": type },
      body,
    });
    // Never log provider response bodies: they may contain account information.
    if (!response.ok)
      throw new Error(`Google Play ${method} failed (${response.status})`);
    return response.json();
  };
  const edit = await call(base, "POST", "{}");
  if (!edit.id || !/^[a-zA-Z0-9_-]+$/.test(edit.id))
    throw new Error("Invalid Google Play edit ID");
  let committed = false;
  try {
    const uploaded = await call(
      `https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/${packageName}/edits/${edit.id}/bundles?uploadType=media`,
      "POST",
      artifact,
      "application/octet-stream",
    );
    if (String(uploaded.versionCode) !== buildNumber)
      throw new Error(
        "Uploaded Android versionCode does not match build number",
      );
    await call(
      `${base}/${edit.id}/tracks/internal`,
      "PUT",
      JSON.stringify({
        track: "internal",
        releases: [{ versionCodes: [buildNumber], status: "completed" }],
      }),
    );
    await call(`${base}/${edit.id}:commit`, "POST", "{}");
    committed = true;
    console.log(
      JSON.stringify({
        event: "delitools_android_published",
        track: "internal",
        buildNumber,
      }),
    );
  } finally {
    if (!committed)
      await request(`${base}/${edit.id}`, {
        method: "DELETE",
        headers: { Authorization: `Bearer ${token}` },
      }).catch(() => {});
  }
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    await publishAndroid(
      readFileSync(process.argv[2]),
      process.env.DELITOOLS_BUILD_NUMBER,
      JSON.parse(process.env.DELITOOLS_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON),
    );
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
