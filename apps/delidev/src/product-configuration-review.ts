// SPDX-License-Identifier: Apache-2.0
const identityFields = new Set(["id", "source_id", "target_id", "server_id", "device_id", "machine_id", "project_id", "repository_id", "provider_id", "account_id", "agent_id", "model_id", "template_id", "primary_repository", "repositories", "ids"]);
const opaqueFields = new Set(["options", "contents", "prompt", "messages", "input", "output", "native", "native_data"]);
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Product review projection only. Keep original JSON numeric literals and all
 * user text byte-for-byte; the untouched preview remains mutation authority. */
export function productConfigurationReview(raw: string, reference: (id: string) => string): string {
  const tokens = raw.match(/"(?:\\.|[^"\\])*"|[{}\[\],:]|[^\s{}\[\],:]+|\s+/g) ?? [];
  const stack: Array<{ key: string; opaque: boolean; array: boolean }> = [];
  let key = "";
  return tokens.map((token, index) => {
    if (token === "{" || token === "[") {
      stack.push({ key, opaque: Boolean(stack.at(-1)?.opaque || opaqueFields.has(key)), array: token === "[" });
      key = ""; return token;
    }
    if (token === "}" || token === "]") { stack.pop(); key = ""; return token; }
    if (!token.startsWith('"')) return token;
    const value = JSON.parse(token) as string;
    let next = index + 1;
    while (next < tokens.length && /^\s+$/.test(tokens[next])) next++;
    if (tokens[next] === ":") { key = value; return token; }
    const field = stack.at(-1)?.array ? stack.at(-1)!.key : key;
    return !stack.at(-1)?.opaque && identityFields.has(field) && uuid.test(value) ? JSON.stringify(reference(value)) : token;
  }).join("");
}
