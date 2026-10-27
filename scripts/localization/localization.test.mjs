import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  generateKeyPair,
  SignJWT,
  exportJWK,
  createLocalJWKSet,
  jwtVerify,
} from "jose";
import {
  authorizationUrl,
  callbackListener,
  ChatGPT,
  completedText,
  validateCallback,
  verifyIdentity,
} from "./chatgpt.mjs";
import {
  fingerprint,
  generate,
  inspect,
  loadProject,
  localizeLinks,
  validateTranslation,
  writeJson,
} from "./catalog.mjs";

async function temporary(t) {
  const directory = await mkdtemp(join(tmpdir(), "delino-localization-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  return directory;
}

async function project(t) {
  const root = await temporary(t);
  await writeJson(join(root, "i18n/locales.json"), {
    sourceLang: "en",
    locales: [
      { lang: "en", label: "English" },
      { lang: "ko", label: "Korean" },
    ],
  });
  await writeJson(join(root, "i18n/messages/en.json"), {
    greeting: "Hello {name}",
  });
  await writeJson(join(root, "i18n/messages/ko.json"), {
    greeting: "Translated {name}",
  });
  await writeJson(join(root, "i18n/state.json"), {
    ko: { "messages/greeting": fingerprint("Hello {name}") },
  });
  return loadProject(root);
}

test("unchanged translations make no inference requests; changed sources are incremental", async (t) => {
  let data = await project(t);
  assert.deepEqual((await inspect(data)).issues, []);
  let calls = 0;
  const translate = async (_locale, input) => {
    calls++;
    return Object.fromEntries(
      Object.entries(input).map(([key, value]) => [key, `Translated ${value}`]),
    );
  };
  assert.equal(await generate(data, translate), 0);
  await writeJson(join(data.root, "i18n/messages/en.json"), {
    greeting: "Hello {name}",
    added: "New source",
  });
  data = await loadProject(data.root);
  assert.equal((await inspect(data)).pending.length, 1);
  assert.equal(await generate(data, translate), 1);
  assert.equal(calls, 1);
  const output = JSON.parse(
    await readFile(join(data.root, "i18n/messages/ko.json"), "utf8"),
  );
  assert.equal(output.greeting, "Translated {name}");
  assert.equal(output.added, "Translated New source");
  assert.deepEqual((await inspect(await loadProject(data.root))).issues, []);
});

test("missing, invalid and obsolete translations fail offline validation", async (t) => {
  const data = await project(t);
  await writeJson(join(data.root, "i18n/messages/ko.json"), {
    greeting: "Missing placeholder",
    obsolete: "Old",
  });
  const result = await inspect(data);
  assert.equal(result.pending.length, 1);
  assert.ok(
    result.issues.some((issue) => issue.includes("obsolete translation")),
  );
  await assert.rejects(
    generate(data, () => assert.fail("must not call inference")),
    /obsolete translation/u,
  );
});

test("new locales use registry entries and reject traversal or duplicate languages", async (t) => {
  const data = await project(t);
  const path = join(data.root, "i18n/locales.json");
  const registry = JSON.parse(await readFile(path, "utf8"));
  registry.locales.push({ lang: "pt-BR", label: "Portuguese" });
  await writeJson(path, registry);
  let next = await loadProject(data.root);
  assert.equal(
    await generate(next, async (_locale, input) => input, "pt-BR"),
    1,
  );
  assert.deepEqual((await inspect(await loadProject(data.root))).issues, []);
  registry.locales.push({ lang: "../../secret", label: "Invalid" });
  await writeJson(path, registry);
  await assert.rejects(loadProject(data.root), /Invalid or duplicate/u);
  registry.locales.pop();
  registry.locales.push(registry.locales[0]);
  await writeJson(path, registry);
  await assert.rejects(loadProject(data.root), /Invalid or duplicate/u);
});

test("failed inference or invalid output does not write translations or fingerprints", async (t) => {
  const data = await project(t);
  await writeJson(join(data.root, "i18n/messages/en.json"), {
    greeting: "Changed {name}",
  });
  const next = await loadProject(data.root);
  const statePath = join(data.root, "i18n/state.json");
  const before = await readFile(statePath, "utf8");
  await assert.rejects(
    generate(next, async () => ({ "messages/greeting": "No placeholder" })),
    /placeholders/u,
  );
  await assert.rejects(
    generate(next, async () => ({ unrelated: "Bad" })),
    /keys/u,
  );
  await assert.rejects(
    generate(next, async () => {
      throw new Error("Usage limit");
    }),
    /Usage limit/u,
  );
  assert.equal(await readFile(statePath, "utf8"), before);
});

test("Markdown protects code, placeholders, links, heading structure and YAML semantics", () => {
  const source =
    "---\npageType: home\nhero:\n  text: Hello\n  actions:\n    - theme: brand\n      text: Guide\n      link: /guide/\n---\n# Title\n\n[Guide](/guide/) `code` {name}\n\n```js\nconst a = 1;\n```\n";
  const target = source
    .replace("Hello", "Translated")
    .replace("# Title", "# Translated");
  validateTranslation(source, target, true);
  for (const [from, to] of [
    ["`code`", "`changed`"],
    ["a = 1", "a = 2"],
    ["pageType: home", "pageType: doc"],
    ["{name}", "{other}"],
    ["](/guide/)", "](/elsewhere/)"],
    ["# Title", "## Title"],
  ]) {
    assert.throws(
      () => validateTranslation(source, source.replace(from, to), true),
      /protected/u,
    );
  }
  const localized = localizeLinks(target, "ko", "en");
  assert.ok(localized.includes("](/ko/guide/)"));
  assert.ok(localized.includes("link: /ko/guide/"));
  validateTranslation(localizeLinks(source, "ko", "en"), localized, true);
  assert.equal(
    localizeLinks(
      "[External](https://example.com) [CDN](//example.com)",
      "ko",
      "en",
    ),
    "[External](https://example.com) [CDN](//example.com)",
  );
  assert.equal(
    localizeLinks(
      "`[Literal](/path/)`\n\n```md\n[Literal](/path/)\n```\n",
      "ko",
      "en",
    ),
    "`[Literal](/path/)`\n\n```md\n[Literal](/path/)\n```\n",
  );
});

test("generation discovers new Markdown pages and detects missing translated pages", async (t) => {
  const data = await project(t);
  const source = "# Guide\n\nRead [Services](/services/).\n";
  const { mkdir } = await import("node:fs/promises");
  await mkdir(join(data.root, "docs/en/guide"), { recursive: true });
  await writeFile(join(data.root, "docs/en/guide/start.md"), source);
  let next = await loadProject(data.root);
  assert.equal((await inspect(next)).pending.length, 1);
  await generate(next, async (_locale, input) =>
    Object.fromEntries(
      Object.entries(input).map(([key, value]) => [
        key,
        value.replace("# Guide", "# Translated"),
      ]),
    ),
  );
  const outputPath = join(data.root, "docs/ko/guide/start.md");
  assert.match(await readFile(outputPath, "utf8"), /\/ko\/services\//u);
  assert.deepEqual((await inspect(await loadProject(data.root))).issues, []);
  await rm(outputPath);
  assert.equal((await inspect(await loadProject(data.root))).pending.length, 1);
});

test("authorization uses stable host, PKCE and issued registration on return", () => {
  const transaction = {
    state: "random-state",
    nonce: "random-nonce",
    verifier: "random-verifier",
  };
  let url = new URL(
    authorizationUrl(
      {},
      "urn:uuid:host",
      "http://127.0.0.1:1234/auth/callback",
      transaction,
    ),
  );
  assert.equal(url.searchParams.get("client_id"), "dynamic_agent_client");
  assert.equal(url.searchParams.get("ext_agent_host_id"), "urn:uuid:host");
  assert.equal(url.searchParams.get("code_challenge_method"), "S256");
  assert.notEqual(url.searchParams.get("code_challenge"), transaction.verifier);
  assert.equal(url.searchParams.get("nonce"), transaction.nonce);
  url = new URL(
    authorizationUrl(
      { clientId: "issued", idToken: "hint" },
      "urn:uuid:host",
      "http://127.0.0.1:4321/auth/callback",
      transaction,
    ),
  );
  assert.equal(url.searchParams.get("client_id"), "issued");
  assert.equal(url.searchParams.has("agent_name_hint"), false);
  assert.equal(url.searchParams.get("id_token_hint"), "hint");
});

test("callback rejects mismatched state, denial, missing code and changed registration", () => {
  const callback = (query) =>
    new URL(`http://127.0.0.1/auth/callback?${query}`);
  assert.throws(
    () => validateCallback(callback("state=bad&code=c&client_id=issued"), "s"),
    /state/u,
  );
  assert.throws(
    () => validateCallback(callback("state=s&error=access_denied"), "s"),
    /declined/u,
  );
  assert.throws(
    () => validateCallback(callback("state=s&code=c"), "s"),
    /client ID/u,
  );
  assert.throws(
    () => validateCallback(callback("state=s&client_id=issued"), "s"),
    /code/u,
  );
  assert.throws(
    () =>
      validateCallback(
        callback("state=s&code=c&client_id=other"),
        "s",
        "issued",
      ),
    /client ID/u,
  );
  assert.deepEqual(
    validateCallback(callback("state=s&code=c"), "s", "issued"),
    { code: "c", clientId: "issued" },
  );
});

test("loopback callback receives consent once and rejects unrelated paths", async (t) => {
  const listener = await callbackListener("s", undefined, 1000);
  t.after(() => listener.close());
  assert.equal(
    (await fetch(listener.redirectUri.replace("/auth/callback", "/other")))
      .status,
    404,
  );
  assert.equal(
    (await fetch(`${listener.redirectUri}?state=s&code=c&client_id=issued`))
      .status,
    200,
  );
  assert.deepEqual(await listener.result, { code: "c", clientId: "issued" });
});

test("login validates identity before activation and reuses registered host across restarts", async (t) => {
  const directory = await temporary(t);
  let nonce;
  let host;
  const client = new ChatGPT({
    directory,
    fetcher: async () =>
      Response.json({
        access_token: "access",
        refresh_token: "refresh",
        id_token: "signed",
        token_type: "Bearer",
        expires_in: 3600,
        scope: "chatgpt.tokens.use.direct",
      }),
    verify: async (_token, id, actualNonce) => {
      assert.equal(id, "issued");
      assert.equal(actualNonce, nonce);
      return { sub: "subject" };
    },
  });
  await client.run(async (auth) => {
    await auth.login(undefined, {
      browser: async (link) => {
        const url = new URL(link);
        nonce = url.searchParams.get("nonce");
        await fetch(
          `${url.searchParams.get("redirect_uri")}?state=${url.searchParams.get("state")}&code=c&client_id=issued`,
        );
      },
    });
    host = auth.state.hostId;
    assert.equal(await auth.accessToken(), "access");
    if (process.platform !== "win32")
      assert.equal(
        (await stat(join(directory, "auth.json"))).mode & 0o777,
        0o600,
      );
  });
  await new ChatGPT({ directory }).run(async (auth) =>
    assert.equal(auth.state.hostId, host),
  );
});

test("token validation rejects missing identity, wrong account and identity-only permission", async (t) => {
  const client = new ChatGPT({
    directory: await temporary(t),
    verify: async () => ({ sub: "other" }),
  });
  const tokens = {
    access_token: "a",
    token_type: "Bearer",
    expires_in: 3600,
    scope: "openid",
  };
  await assert.rejects(
    client.applyTokens({ clientId: "issued" }, tokens, "nonce"),
    /ID token/u,
  );
  await assert.rejects(
    client.applyTokens(
      { clientId: "issued", subject: "original" },
      { ...tokens, id_token: "signed" },
      "nonce",
    ),
    /account/u,
  );
  await client.run(async (auth) => {
    auth.state.active = "id";
    auth.state.profiles.id = { accessToken: "a", scopes: ["openid"] };
    await assert.rejects(auth.accessToken(), /authorize ChatGPT plan/u);
  });
});

test("ID token signature, issuer, audience, expiration, nonce and subject are verified", async () => {
  const { privateKey, publicKey } = await generateKeyPair("RS256");
  const key = {
    ...(await exportJWK(publicKey)),
    kid: "test-key",
    alg: "RS256",
  };
  const keys = createLocalJWKSet({ keys: [key] });
  const sign = async (overrides = {}) =>
    new SignJWT({
      sub: "subject",
      nonce: "nonce",
      iss: "https://auth.openai.com",
      aud: "issued",
      iat: Math.floor(Date.now() / 1000),
      exp: Math.floor(Date.now() / 1000) + 3600,
      ...overrides,
    })
      .setProtectedHeader({ alg: "RS256", kid: "test-key" })
      .sign(privateKey);
  const token = await sign();
  assert.equal(
    (await verifyIdentity(token, "issued", "nonce", keys)).sub,
    "subject",
  );
  for (const change of [
    { iss: "wrong" },
    { aud: "wrong" },
    { exp: 0 },
    { nonce: "wrong" },
    { sub: undefined },
  ])
    await assert.rejects(
      verifyIdentity(await sign(change), "issued", "nonce", keys),
    );
  const { publicKey: other } = await generateKeyPair("RS256");
  await assert.rejects(
    verifyIdentity(
      token,
      "issued",
      "nonce",
      createLocalJWKSet({
        keys: [{ ...(await exportJWK(other)), kid: "test-key", alg: "RS256" }],
      }),
    ),
  );
});

test("refresh rotates tokens atomically and lock excludes concurrent operations", async (t) => {
  const directory = await temporary(t);
  const client = new ChatGPT({
    directory,
    fetcher: async (_url, options) => {
      assert.equal(options.body.get("client_id"), "issued");
      assert.equal(options.body.get("refresh_token"), "old-refresh");
      return Response.json({
        access_token: "new-access",
        refresh_token: "new-refresh",
        token_type: "Bearer",
        expires_in: 3600,
      });
    },
  });
  await client.run(async (auth) => {
    auth.state.active = "id";
    auth.state.profiles.id = {
      clientId: "issued",
      subject: "s",
      accessToken: "old-access",
      refreshToken: "old-refresh",
      expiresAt: 0,
      scopes: ["chatgpt.tokens.use.direct"],
    };
    await assert.rejects(
      new ChatGPT({ directory }).run(() => {}),
      /Another translation/u,
    );
    assert.equal(await auth.accessToken(), "new-access");
    const saved = JSON.parse(
      await readFile(join(directory, "auth.json"), "utf8"),
    );
    assert.equal(saved.profiles.id.refreshToken, "new-refresh");
    assert.ok(saved.profiles.id.expiresAt > Date.now());
  });
});

function events(items, split = 3) {
  const body = items
    .map((item) => `data: ${JSON.stringify(item)}\r\n\r\n`)
    .join("");
  const bytes = new TextEncoder().encode(body);
  return new Response(
    new ReadableStream({
      start(controller) {
        for (let index = 0; index < bytes.length; index += split)
          controller.enqueue(bytes.slice(index, index + split));
        controller.close();
      },
    }),
  );
}
const completed = (text) => ({
  type: "response.completed",
  response: {
    status: "completed",
    output: [{ type: "message", content: [{ type: "output_text", text }] }],
  },
});

test("stream accepts completed output across arbitrary UTF-8 and CRLF boundaries", async () => {
  assert.equal(
    await completedText(
      events([
        { type: "response.output_text.delta", delta: "Partial" },
        completed("Translation \uD55C\uAE00"),
      ]),
    ),
    "Translation \uD55C\uAE00",
  );
});

test("stream falls back to output items when the completed event has empty output", async () => {
  assert.equal(
    await completedText(
      events([
        {
          type: "response.output_item.done",
          item: {
            type: "message",
            content: [{ type: "output_text", text: "Item \uD55C\uAE00" }],
          },
        },
        {
          type: "response.completed",
          response: { status: "completed", output: [] },
        },
      ]),
    ),
    "Item \uD55C\uAE00",
  );
});

test("stream rejects truncation, errors after deltas, incomplete responses and refusals", async () => {
  await assert.rejects(
    completedText(
      events([{ type: "response.output_text.delta", delta: "Partial" }]),
    ),
    /without response.completed/u,
  );
  for (const type of ["response.failed", "response.incomplete", "error"])
    await assert.rejects(completedText(events([{ type }])), /incomplete/u);
  await assert.rejects(completedText(events([completed("")])), /empty/u);
  await assert.rejects(
    completedText(
      events([
        {
          type: "response.completed",
          response: {
            status: "completed",
            output: [{ content: [{ type: "refusal" }] }],
          },
        },
      ]),
    ),
    /declined/u,
  );
});

test("inference sends only public Responses requests with plan storage settings", async (t) => {
  const client = new ChatGPT({
    directory: await temporary(t),
    fetcher: async (url, options) => {
      assert.equal(url, "https://api.openai.com/v1/responses");
      const body = JSON.parse(options.body);
      assert.equal(body.store, false);
      assert.equal(body.stream, true);
      assert.deepEqual(body.input, [
        { role: "user", content: [{ type: "input_text", text: "Input" }] },
      ]);
      assert.equal(options.headers.Authorization, "Bearer access");
      return events([completed('{"message":"Translated"}')]);
    },
  });
  await client.run(async (auth) => {
    auth.state.active = "id";
    auth.state.profiles.id = {
      accessToken: "access",
      scopes: ["chatgpt.tokens.use.direct"],
      expiresAt: Date.now() + 3600_000,
    };
    assert.equal(
      await auth.translate("account-model", "Input", "Instructions"),
      '{"message":"Translated"}',
    );
  });
});

test(
  "parallel requests reload auth and rotate an expired session only once",
  { timeout: 5000 },
  async (t) => {
    const directory = await temporary(t);
    await new ChatGPT({ directory }).run(async (auth) => {
      auth.state.active = "id";
      auth.state.profiles.id = {
        clientId: "issued",
        accessToken: "old-access",
        refreshToken: "old-refresh",
        expiresAt: 0,
        scopes: ["chatgpt.tokens.use.direct"],
      };
      await auth.save();
    });
    let refreshes = 0;
    let requests = 0;
    const bothStarted = Promise.withResolvers();
    const fetcher = async (url, options) => {
      if (url.endsWith("/oauth/token")) {
        refreshes++;
        assert.equal(options.body.get("refresh_token"), "old-refresh");
        return Response.json({
          access_token: "new-access",
          refresh_token: "new-refresh",
          token_type: "Bearer",
          expires_in: 3600,
        });
      }
      assert.equal(options.headers.Authorization, "Bearer new-access");
      if (++requests === 2) bothStarted.resolve();
      await bothStarted.promise;
      return events([completed("Translated")]);
    };
    const clients = [
      new ChatGPT({ directory, fetcher }),
      new ChatGPT({ directory, fetcher }),
    ];
    assert.deepEqual(
      await Promise.all(
        clients.map((client) =>
          client.translate("model", "input", "instructions"),
        ),
      ),
      ["Translated", "Translated"],
    );
    assert.equal(refreshes, 1);
    assert.equal(requests, 2);
    const state = JSON.parse(
      await readFile(join(directory, "auth.json"), "utf8"),
    );
    assert.equal(state.profiles.id.refreshToken, "new-refresh");
    // An existing inference client must observe subsequent account changes.
    await new ChatGPT({ directory }).run(async (auth) => {
      auth.state.profiles.id.accessToken = "changed-access";
      await auth.save();
    });
    assert.equal(await clients[0].accessToken(), "changed-access");
    assert.equal(clients[0].state, undefined);
  },
);

async function batchProject(t) {
  const data = await project(t);
  // Three catalog batches exercise bounded scheduling and shared-file writes.
  await writeJson(join(data.root, "i18n/messages/en.json"), {
    greeting: "Hello {name}",
    ...Object.fromEntries(
      Array.from({ length: 81 }, (_, index) => [
        `added${index}`,
        `Text ${index}`,
      ]),
    ),
  });
  return loadProject(data.root);
}

test(
  "generation overlaps two requests and serializes catalog and state writes",
  { timeout: 5000 },
  async (t) => {
    const data = await batchProject(t);
    const secondStarted = Promise.withResolvers();
    let requests = 0;
    let active = 0;
    let maximum = 0;
    const generation = generate(data, async (_locale, input) => {
      const number = ++requests;
      maximum = Math.max(maximum, ++active);
      if (number === 1) await secondStarted.promise;
      if (number === 2) secondStarted.resolve();
      active--;
      return Object.fromEntries(
        Object.entries(input).map(([key, value]) => [
          key,
          `Translated ${value}`,
        ]),
      );
    });
    assert.equal(await generation, 81);
    assert.equal(maximum, 2);
    assert.equal(requests, 3);
    assert.deepEqual((await inspect(await loadProject(data.root))).issues, []);
    const messages = JSON.parse(
      await readFile(join(data.root, "i18n/messages/ko.json"), "utf8"),
    );
    assert.equal(messages.greeting, "Translated {name}");
    assert.equal(messages.added0, "Translated Text 0");
    assert.equal(messages.added80, "Translated Text 80");
  },
);

test("failed parallel batches preserve successful peers and resume remaining work", async (t) => {
  const data = await batchProject(t);
  let calls = 0;
  await assert.rejects(
    generate(data, async (_locale, input) => {
      if (++calls === 1) throw new Error("Usage limit");
      return input;
    }),
    /Usage limit/u,
  );
  assert.equal(calls, 2);
  let next = await loadProject(data.root);
  assert.equal((await inspect(next)).pending.length, 41);
  assert.equal(await generate(next, async (_locale, input) => input), 41);
  next = await loadProject(data.root);
  assert.deepEqual((await inspect(next)).issues, []);
});

test(
  "overlapping commands for one app are rejected while separate apps can translate",
  { timeout: 5000 },
  async (t) => {
    const first = await batchProject(t);
    const other = await batchProject(t);
    const started = Promise.withResolvers();
    const release = Promise.withResolvers();
    const running = generate(first, async (_locale, input) => {
      started.resolve();
      await release.promise;
      return input;
    });
    try {
      await started.promise;
      await assert.rejects(
        generate(
          await loadProject(first.root),
          async (_locale, input) => input,
        ),
        /already running/u,
      );
      assert.equal(await generate(other, async (_locale, input) => input), 81);
    } finally {
      release.resolve();
      await running;
    }
    assert.deepEqual((await inspect(await loadProject(first.root))).issues, []);
  },
);
