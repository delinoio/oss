import { createHash, randomBytes, randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { createServer } from "node:http";
import {
  chmod,
  mkdir,
  readFile,
  rename,
  rm,
  writeFile,
} from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { createRemoteJWKSet, jwtVerify } from "jose";

const ISSUER = "https://auth.openai.com";
const RESOURCE = "https://api.openai.com/v1";
const USAGE = "https://chatgpt.com/settings/usage";
const SCOPE = "chatgpt.tokens.use.direct";
const jwks = createRemoteJWKSet(new URL(`${ISSUER}/.well-known/jwks.json`));

export async function verifyIdentity(token, clientId, nonce, keys = jwks) {
  const { payload } = await jwtVerify(token, keys, {
    issuer: ISSUER,
    audience: clientId,
    requiredClaims: ["sub", "exp", "iat"],
    clockTolerance: 5,
  });
  if (nonce !== undefined && payload.nonce !== nonce) {
    throw new Error("ChatGPT ID token nonce did not match.");
  }
  return payload;
}

export async function atomicJson(path, data) {
  const temporary = `${path}.${randomUUID()}.tmp`;
  try {
    await writeFile(temporary, `${JSON.stringify(data, null, 2)}\n`, {
      mode: 0o600,
      flag: "wx",
    });
    await rename(temporary, path);
    await chmod(path, 0o600);
  } finally {
    await rm(temporary, { force: true });
  }
}

export function authorizationUrl(profile, hostId, redirectUri, transaction) {
  const url = new URL(`${ISSUER}/api/accounts/authorize`);
  url.search = new URLSearchParams({
    client_id: profile.clientId ?? "dynamic_agent_client",
    ext_agent_host_id: hostId,
    redirect_uri: redirectUri,
    response_type: "code",
    scope: `openid profile email offline_access resource.invoke ${SCOPE}`,
    resource: RESOURCE,
    state: transaction.state,
    nonce: transaction.nonce,
    code_challenge_method: "S256",
    code_challenge: createHash("sha256")
      .update(transaction.verifier)
      .digest("base64url"),
    ...(!profile.clientId ? { agent_name_hint: "Delino Translations" } : {}),
    ...(profile.idToken ? { id_token_hint: profile.idToken } : {}),
  }).toString();
  return url.href;
}

export function validateCallback(url, state, clientId) {
  if (url.searchParams.get("state") !== state)
    throw new Error("Sign-in state did not match.");
  if (url.searchParams.has("error"))
    throw new Error("ChatGPT sign-in was declined or failed.");
  const issued = url.searchParams.get("client_id") ?? clientId;
  if (
    !issued ||
    issued === "dynamic_agent_client" ||
    (clientId && issued !== clientId)
  ) {
    throw new Error("ChatGPT registration client ID did not match.");
  }
  const code = url.searchParams.get("code");
  if (!code) throw new Error("ChatGPT sign-in returned no authorization code.");
  return { code, clientId: issued };
}

export async function callbackListener(state, clientId, timeoutMs = 180_000) {
  let finish;
  let fail;
  const result = new Promise((resolve, reject) => {
    finish = resolve;
    fail = reject;
  });
  // Attach a handler immediately: the browser callback can precede the caller's await.
  result.catch(() => {});
  const server = createServer((request, response) => {
    const url = new URL(request.url, "http://127.0.0.1");
    if (request.method !== "GET" || url.pathname !== "/auth/callback") {
      response.writeHead(404).end();
      return;
    }
    try {
      const callback = validateCallback(url, state, clientId);
      response.writeHead(200, {
        "Content-Type": "text/plain",
        "Cache-Control": "no-store",
      });
      response.end("ChatGPT authorization received. Return to the terminal.");
      finish(callback);
    } catch (error) {
      response.writeHead(400, {
        "Content-Type": "text/plain",
        "Cache-Control": "no-store",
      });
      response.end("Sign-in could not be verified. Return to the terminal.");
      fail(error);
    }
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const timer = setTimeout(
    () => fail(new Error("ChatGPT sign-in timed out. Run i18n:login again.")),
    timeoutMs,
  );
  return {
    redirectUri: `http://127.0.0.1:${server.address().port}/auth/callback`,
    result,
    close() {
      clearTimeout(timer);
      server.closeAllConnections();
      server.close();
    },
  };
}

function openBrowser(url) {
  const [command, args] =
    process.platform === "darwin"
      ? ["open", [url]]
      : process.platform === "win32"
        ? ["rundll32", ["url.dll,FileProtocolHandler", url]]
        : ["xdg-open", [url]];
  return new Promise((resolve, reject) => {
    execFile(command, args, (error) =>
      error
        ? reject(
            new Error(
              "Could not open the system browser. Run i18n:login on a desktop session.",
            ),
          )
        : resolve(),
    );
  });
}

export class ChatGPT {
  constructor({
    directory = join(homedir(), ".config", "delino-translations"),
    fetcher = fetch,
    verify = verifyIdentity,
  } = {}) {
    this.directory = directory;
    this.fetcher = fetcher;
    this.verify = verify;
  }

  async run(action, { wait = false } = {}) {
    await mkdir(this.directory, { recursive: true, mode: 0o700 });
    await chmod(this.directory, 0o700);
    const lock = join(this.directory, "operation.lock");
    const deadline = Date.now() + 30_000;
    for (;;) {
      try {
        await mkdir(lock);
        break;
      } catch (error) {
        if (error.code !== "EEXIST") throw error;
        if (!wait || Date.now() >= deadline)
          throw new Error(
            "Another translation authentication operation is running. If it stopped unexpectedly, remove ~/.config/delino-translations/operation.lock and retry.",
          );
        await delay(50);
      }
    }
    try {
      try {
        this.state = JSON.parse(
          await readFile(join(this.directory, "auth.json"), "utf8"),
        );
      } catch (error) {
        if (error.code !== "ENOENT")
          throw new Error("Could not read translation authentication state.");
        this.state = {
          hostId: `urn:uuid:${randomUUID()}`,
          profiles: {},
          active: null,
        };
        await this.save();
      }
      return await action(this);
    } finally {
      this.state = undefined;
      await rm(lock, { recursive: true, force: true });
    }
  }

  save() {
    return atomicJson(join(this.directory, "auth.json"), this.state);
  }

  async request(url, options = {}) {
    let response;
    try {
      response = await this.fetcher(url, {
        ...options,
        signal: AbortSignal.timeout(120_000),
      });
    } catch {
      throw new Error(
        "ChatGPT network request failed or timed out. Retry the command.",
      );
    }
    if (!response.ok) {
      if (response.status === 429)
        throw new Error(`ChatGPT usage limit reached. Manage usage: ${USAGE}`);
      if (response.status === 401 || response.status === 403)
        throw new Error(
          "ChatGPT authorization is unavailable. Run i18n:login and enable ChatGPT plan usage.",
        );
      throw new Error(
        `ChatGPT request failed (HTTP ${response.status}). Retry the command.`,
      );
    }
    return response;
  }

  async token(form) {
    return (
      await this.request(`${ISSUER}/api/accounts/oauth/token`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ ...form, resource: RESOURCE }),
      })
    ).json();
  }

  async applyTokens(profile, tokens, nonce) {
    if (
      typeof tokens.access_token !== "string" ||
      !tokens.access_token ||
      tokens.token_type?.toLowerCase() !== "bearer" ||
      !Number.isFinite(tokens.expires_in) ||
      tokens.expires_in <= 0
    ) {
      throw new Error("ChatGPT returned an invalid token response.");
    }
    const identity = tokens.id_token
      ? await this.verify(tokens.id_token, profile.clientId, nonce)
      : null;
    if (nonce !== undefined && !identity)
      throw new Error("ChatGPT returned no ID token.");
    if (
      identity &&
      (typeof identity.sub !== "string" ||
        !identity.sub ||
        (profile.subject && profile.subject !== identity.sub))
    ) {
      throw new Error(
        "The ChatGPT account did not match the saved registration.",
      );
    }
    const next = {
      ...profile,
      subject: identity?.sub ?? profile.subject,
      email: identity?.email ?? profile.email,
      idToken: tokens.id_token ?? profile.idToken,
      accessToken: tokens.access_token,
      refreshToken: tokens.refresh_token ?? profile.refreshToken,
      scopes:
        typeof tokens.scope === "string"
          ? tokens.scope.split(/\s+/u)
          : nonce === undefined
            ? profile.scopes
            : [],
      expiresAt: Date.now() + tokens.expires_in * 1000,
    };
    return next;
  }

  async login(profileId, { browser = openBrowser } = {}) {
    const id =
      profileId === "--new"
        ? randomUUID()
        : (profileId ?? this.state.active ?? randomUUID());
    if (profileId && profileId !== "--new" && !this.state.profiles[id])
      throw new Error("Unknown ChatGPT profile.");
    const profile = this.state.profiles[id] ?? {};
    const random = () => randomBytes(32).toString("base64url");
    const transaction = {
      state: random(),
      nonce: random(),
      verifier: random(),
    };
    const listener = await callbackListener(
      transaction.state,
      profile.clientId,
    );
    try {
      console.log(
        "Continue with ChatGPT in your browser. Authorize ChatGPT plan usage for Delino Translations.",
      );
      await browser(
        authorizationUrl(
          profile,
          this.state.hostId,
          listener.redirectUri,
          transaction,
        ),
      );
      const callback = await listener.result;
      // Retain the issued registration even if the single-use authorization code expires.
      const registered = { ...profile, clientId: callback.clientId };
      this.state.profiles[id] = registered;
      await this.save();
      const tokens = await this.token({
        grant_type: "authorization_code",
        client_id: callback.clientId,
        code: callback.code,
        code_verifier: transaction.verifier,
        redirect_uri: listener.redirectUri,
      });
      const next = await this.applyTokens(
        registered,
        tokens,
        transaction.nonce,
      );
      this.state.profiles[id] = next;
      this.state.active = id;
      await this.save();
      if (!next.scopes.includes(SCOPE))
        throw new Error(
          "Signed in, but ChatGPT plan usage was not granted. Run i18n:login and enable it.",
        );
      console.log(`ChatGPT plan usage is enabled. Manage usage: ${USAGE}`);
    } finally {
      listener.close();
    }
  }

  async accessToken() {
    // Inference does not hold the auth lock. Reload under the lock for every
    // request so concurrent processes cannot reuse a rotated refresh token.
    if (!this.state)
      return new ChatGPT({
        directory: this.directory,
        fetcher: this.fetcher,
        verify: this.verify,
      }).run((client) => client.accessToken(), { wait: true });
    const id = this.state.active;
    let profile = this.state.profiles[id];
    if (!profile?.accessToken || !profile.scopes?.includes(SCOPE))
      throw new Error("Run i18n:login and authorize ChatGPT plan usage first.");
    if (profile.expiresAt <= Date.now() + 60_000) {
      if (!profile.refreshToken)
        throw new Error("ChatGPT session expired. Run i18n:login again.");
      profile = await this.applyTokens(
        profile,
        await this.token({
          grant_type: "refresh_token",
          client_id: profile.clientId,
          refresh_token: profile.refreshToken,
        }),
      );
      this.state.profiles[id] = profile;
      await this.save();
    }
    if (!profile.scopes.includes(SCOPE))
      throw new Error(
        "ChatGPT plan usage permission is no longer enabled. Run i18n:login.",
      );
    return profile.accessToken;
  }

  async models() {
    const response = await this.request(`${RESOURCE}/models`, {
      headers: { Authorization: `Bearer ${await this.accessToken()}` },
    });
    const data = await response.json();
    const models = data.models?.filter(
      (model) => model.visibility === "list" && typeof model.slug === "string",
    );
    if (!models?.length)
      throw new Error("No ChatGPT models are available for this account.");
    return models;
  }

  async translate(model, input, instructions) {
    const response = await this.request(`${RESOURCE}/responses`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${await this.accessToken()}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model,
        // The Responses endpoint rejects a bare string; it requires a message list.
        input: [
          { role: "user", content: [{ type: "input_text", text: input }] },
        ],
        instructions,
        store: false,
        stream: true,
      }),
    });
    return completedText(response);
  }

  async logout() {
    const profile = this.state.profiles[this.state.active];
    if (!profile) return;
    let revoked = !profile.refreshToken;
    try {
      if (profile.refreshToken) {
        const discovery = await (
          await this.request(`${ISSUER}/.well-known/openid-configuration`)
        ).json();
        const endpoint = new URL(discovery.revocation_endpoint);
        if (endpoint.origin !== ISSUER)
          throw new Error("Unexpected revocation endpoint.");
        for (let attempt = 0; ; attempt++) {
          try {
            await this.request(endpoint, {
              method: "POST",
              headers: { "Content-Type": "application/x-www-form-urlencoded" },
              body: new URLSearchParams({
                token: profile.refreshToken,
                token_type_hint: "refresh_token",
                client_id: profile.clientId,
              }),
            });
            break;
          } catch (error) {
            if (attempt === 2) throw error;
            await new Promise((resolve) =>
              setTimeout(resolve, 250 * 2 ** attempt),
            );
          }
        }
        revoked = true;
      }
    } finally {
      this.state.profiles[this.state.active] = {
        clientId: profile.clientId,
        subject: profile.subject,
        email: profile.email,
      };
      await this.save();
      if (!revoked)
        console.error(
          `Signed out locally. Remote revocation was not confirmed. Disconnect the app in ChatGPT Settings: ${USAGE}`,
        );
    }
  }
}

export async function completedText(response) {
  let buffer = "";
  // Workaround: the live endpoint sends an empty `output` in `response.completed`
  // and delivers message items only through `response.output_item.done`. Scope:
  // this function only. Remove the fallback if completed events carry the output.
  const items = [];
  const decoder = new TextDecoder();
  for await (const chunk of response.body) {
    buffer = (buffer + decoder.decode(chunk, { stream: true })).replace(
      /\r\n/gu,
      "\n",
    );
    let boundary;
    while ((boundary = buffer.indexOf("\n\n")) !== -1) {
      const frame = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const data = frame
        .split("\n")
        .filter((line) => line.startsWith("data:"))
        .map((line) => line.slice(5).trimStart())
        .join("\n");
      if (!data || data === "[DONE]") continue;
      const event = JSON.parse(data);
      if (
        ["response.failed", "response.incomplete", "error"].includes(event.type)
      )
        throw new Error(
          `ChatGPT inference failed or was incomplete. Manage usage: ${USAGE}`,
        );
      if (event.type === "response.output_item.done") items.push(event.item);
      if (event.type === "response.completed") {
        if (event.response.status !== "completed")
          throw new Error("ChatGPT response did not complete.");
        const output = event.response.output?.length
          ? event.response.output
          : items;
        const content = output.flatMap((item) => item.content ?? []);
        if (content.some((item) => item.type === "refusal"))
          throw new Error("ChatGPT declined the translation.");
        const text = content
          .filter((item) => item.type === "output_text")
          .map((item) => item.text)
          .join("");
        if (!text.trim())
          throw new Error("ChatGPT returned an empty translation.");
        return text;
      }
    }
  }
  throw new Error(
    "ChatGPT stream ended without response.completed. Retry the command.",
  );
}
