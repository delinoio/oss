import { readFileSync, readdirSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { decodeHTML } from "entities";

export const routes = ["/", "/installation", "/configuration", "/commands", "/verification", "/reports", "/privacy", "/platforms", "/troubleshooting", "/benchmarks", "/releases"];
const origin = "https://runlens.delino.io";
export function validatePage(html, route) {
  const errors = [];
  if (!/<html[^>]*\blang="en"/u.test(html)) errors.push("English document language missing");
  if (!/<h1\b/u.test(html) || !/<main\b|<article\b/u.test(html)) errors.push("semantic article missing");
  const article = html.match(/<article\b[\s\S]*?<\/article>/u)?.[0] ?? html;
  const text = decodeHTML(article.replace(/<[^>]*>/gu, " "));
  if (/\b(?:DEVHUD_|core\.hooksPath)\b|(?:^|\s)(?:crates|servers|packaging)\/[a-z]|\.github\/workflows\/|(?:^|\s)docs\/project-/u.test(text)) errors.push("repository internals in public content");
  const links = [...html.matchAll(/<a\b[^>]*\bhref="([^"]+)"/gu)].map((match) => decodeHTML(match[1]));
  for (const value of links) {
    let url;
    try { url = new URL(value, origin + route); } catch { errors.push("invalid link"); continue; }
    if (url.username || url.password || ["javascript:", "file:"].includes(url.protocol)) errors.push("unsafe link");
    if (url.origin === origin && url.pathname.endsWith(".html")) errors.push("non-clean route");
  }
  if (!links.includes("https://github.com/delinoio/oss")) errors.push("repository discovery link missing");
  for (const required of routes) {
    if (!links.some((value) => new URL(value, origin + route).pathname === required)) errors.push(`navigation missing: ${required}`);
  }
  return errors;
}
export function validateBuild(directory) {
  for (const route of routes) {
    const filename = route === "/" ? "index.html" : `${route.slice(1)}.html`;
    const failures = validatePage(readFileSync(resolve(directory, filename), "utf8"), route);
    if (failures.length) throw Error(`${route}: ${failures.join(", ")}`);
  }
  for (const filename of ["install.sh", "install.ps1"]) {
    if (!readFileSync(resolve(directory, filename), "utf8").includes("verify-blob")) throw Error("authenticated installer missing");
  }
  function inspect(directory) {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      if (entry.isDirectory()) inspect(resolve(directory, entry.name));
      else if (entry.name.endsWith(".map")) throw Error("public source map present");
    }
  }
  inspect(directory);
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  validateBuild(resolve(dirname(fileURLToPath(import.meta.url)), "../doc_build"));
  console.log("Runlens documentation routes, discovery, public content, and installers verified.");
}
