// SPDX-License-Identifier: Apache-2.0
import { fromMarkdown } from "mdast-util-from-markdown";
import { Fragment, type ReactNode } from "react";
import { copy } from "./localization";
import { object, text, type Document } from "./documents";

export function InertPRMarkdown({ body }: { body: string }) {
  const render = (node: ReturnType<typeof fromMarkdown> | Document, key: number): ReactNode => {
    const value = node as unknown as Document, children = Array.isArray(value.children) ? value.children.map((child, index) => render(object(child), index)) : undefined;
    const source = () => { const position = object(value.position), start = object(position.start), end = object(position.end); return body.slice(Number(start.offset), Number(end.offset)); };
    switch (value.type) {
      case "root": return <Fragment key={key}>{children}</Fragment>;
      case "text": return <Fragment key={key}>{text(value.value)}</Fragment>;
      case "paragraph": return <p key={key}>{children}</p>;
      case "heading": return <div role="heading" aria-level={Math.min(6, Number(value.depth) + 1)} key={key}>{children}</div>;
      case "strong": return <strong key={key}>{children}</strong>;
      case "emphasis": return <em key={key}>{children}</em>;
      case "inlineCode": return <code key={key}>{text(value.value)}</code>;
      case "code": return <pre key={key}><code>{text(value.value)}</code></pre>;
      case "list": return value.ordered ? <ol start={Number(value.start) || 1} key={key}>{children}</ol> : <ul key={key}>{children}</ul>;
      case "listItem": return <li key={key}>{children}</li>;
      case "break": return <br key={key} />;
      default: return <span className="pr-inert-source" key={key}>{source()}</span>;
    }
  };
  return <div className="pr-description">{body ? render(fromMarkdown(body), 0) : <p>{copy("github-items.extra.c8c2e1d98310")}</p>}<details><summary>{copy("pr-workspace.originalBody")}</summary><pre>{body}</pre></details></div>;
}
export type PatchLine = { source: string; old?: number; next?: number; kind: "addition" | "deletion" | "context" | "source" };
export function patchSections(patch: string): { title: string; lines: PatchLine[]; fallback: boolean }[] {
  const sections: { title: string; lines: PatchLine[]; fallback: boolean }[] = [];
  let section: typeof sections[number] | undefined, old: number | undefined, next: number | undefined;
  for (const source of patch.split("\n")) {
    if (source.startsWith("diff --git ") || !section) { section = { title: source.startsWith("diff --git ") ? source.slice(11) : copy("pr-workspace.diff"), lines: [], fallback: false }; sections.push(section); old = next = undefined; }
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(source);
    if (hunk) { old = Number(hunk[1]); next = Number(hunk[2]); }
    if (source.startsWith("Binary files ") || source.startsWith("GIT binary patch") || source.startsWith("diff --cc ") || source.startsWith("diff --combined ") || source.startsWith("@@") && !hunk) section.fallback = true;
    if (!hunk && old !== undefined && next !== undefined && /^[ +\-]/.test(source)) {
      const kind = source[0] === "+" ? "addition" : source[0] === "-" ? "deletion" : "context";
      section.lines.push({ source, kind, old: kind === "addition" ? undefined : old++, next: kind === "deletion" ? undefined : next++ });
    } else section.lines.push({ source, kind: "source" });
  }
  return sections;
}
export function PRDiff({ value }: { value: Document }) {
  const diff = object(value.diff);
  return <>{patchSections(text(diff.patch)).map((section, index) => <section className="pr-diff-file" key={index}><h3>{section.title}</h3>{section.fallback ? <pre>{section.lines.map(line => line.source).join("\n")}</pre> : <div>{section.lines.map((line, number) => <div className={`pr-patch-line pr-patch-${line.kind}`} key={number}><span aria-label={copy("pr-workspace.oldLine")}>{line.old}</span><span aria-label={copy("pr-workspace.newLine")}>{line.next}</span><code>{line.source}</code></div>)}</div>}</section>)}<details><summary>{copy("pr-workspace.snapshot")}</summary><dl><dt>{copy("pr-workspace.baseSHA")}</dt><dd>{text(diff.base_sha)}</dd><dt>{copy("pr-workspace.headSHA")}</dt><dd>{text(diff.head_sha)}</dd><dt>SHA-256</dt><dd>{text(diff.digest)}</dd></dl><pre>{text(diff.patch)}</pre></details></>;
}
