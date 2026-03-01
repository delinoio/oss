#!/usr/bin/env node

"use strict";

const { execFileSync } = require("node:child_process");

const DEFAULT_ACTORS = [
  "codex",
  "codex[bot]",
  "openai-codex",
  "openai-codex[bot]",
];

function printHelp() {
  console.log(`Usage: collect_codex_feedback.js <pr_number> [options]

Collect Codex-authored feedback from a pull request.

Options:
  --repo <owner/repo>          Repository override
  --actor <login>              Exact actor login to match (repeatable)
  --actor-regex <pattern>      Case-insensitive regex matcher (default: codex)
  --no-default-actors          Disable default actor list
  --format <markdown|json>     Output format (default: markdown)
  --max-body-length <number>   Max body chars after compaction (default: 360)
  --fail-if-empty              Exit 1 if no matching feedback items found
  -h, --help                   Show this help message`);
}

function parseArgs(argv) {
  const options = {
    repo: null,
    actors: [],
    actorRegex: "codex",
    noDefaultActors: false,
    format: "markdown",
    maxBodyLength: 360,
    failIfEmpty: false,
    prNumber: null,
    help: false,
  };

  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "-h" || arg === "--help") {
      options.help = true;
      return options;
    }
    if (arg === "--repo") {
      i += 1;
      if (i >= argv.length) {
        throw new Error("--repo requires a value");
      }
      options.repo = argv[i];
      continue;
    }
    if (arg === "--actor") {
      i += 1;
      if (i >= argv.length) {
        throw new Error("--actor requires a value");
      }
      options.actors.push(argv[i]);
      continue;
    }
    if (arg === "--actor-regex") {
      i += 1;
      if (i >= argv.length) {
        throw new Error("--actor-regex requires a value");
      }
      options.actorRegex = argv[i];
      continue;
    }
    if (arg === "--no-default-actors") {
      options.noDefaultActors = true;
      continue;
    }
    if (arg === "--format") {
      i += 1;
      if (i >= argv.length) {
        throw new Error("--format requires a value");
      }
      const value = String(argv[i]);
      if (value !== "markdown" && value !== "json") {
        throw new Error(`Unsupported --format value: ${value}`);
      }
      options.format = value;
      continue;
    }
    if (arg === "--max-body-length") {
      i += 1;
      if (i >= argv.length) {
        throw new Error("--max-body-length requires a value");
      }
      const value = Number.parseInt(argv[i], 10);
      if (!Number.isFinite(value) || value < 0) {
        throw new Error(`Invalid --max-body-length value: ${argv[i]}`);
      }
      options.maxBodyLength = value;
      continue;
    }
    if (arg === "--fail-if-empty") {
      options.failIfEmpty = true;
      continue;
    }
    if (arg.startsWith("-")) {
      throw new Error(`Unknown option: ${arg}`);
    }
    if (options.prNumber !== null) {
      throw new Error(`Unexpected argument: ${arg}`);
    }
    const parsed = Number.parseInt(arg, 10);
    if (!Number.isFinite(parsed)) {
      throw new Error(`Invalid PR number: ${arg}`);
    }
    options.prNumber = parsed;
  }

  return options;
}

function runGh(args) {
  try {
    return execFileSync("gh", args, { encoding: "utf8" });
  } catch (error) {
    const stderr = String(error.stderr || "").trim();
    const stdout = String(error.stdout || "").trim();
    const detail = stderr || stdout || error.message;
    throw new Error(`gh command failed: gh ${args.join(" ")} (${detail})`);
  }
}

function resolveRepo(repo) {
  if (repo) {
    return repo;
  }
  const out = runGh(["repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"]).trim();
  if (!out.includes("/")) {
    throw new Error(`could not resolve repository from gh output: ${JSON.stringify(out)}`);
  }
  return out;
}

function fetchEndpoint(path) {
  const out = runGh([
    "api",
    "-H",
    "Accept: application/vnd.github+json",
    path,
    "--method",
    "GET",
    "--field",
    "per_page=100",
  ]);
  const payload = JSON.parse(out);
  if (!Array.isArray(payload)) {
    throw new Error(`unexpected payload for endpoint ${JSON.stringify(path)}; expected a JSON array`);
  }
  return payload;
}

function compactText(text, maxBodyLength) {
  if (!text) {
    return "";
  }
  const collapsed = String(text).trim().split(/\s+/u).join(" ");
  if (collapsed.length <= maxBodyLength) {
    return collapsed;
  }
  const keep = maxBodyLength - 3;
  if (keep <= 0) {
    return "...";
  }
  return `${collapsed.slice(0, keep)}...`;
}

function actorMatches(login, actorSet, actorPattern) {
  const lowered = String(login).toLowerCase();
  if (actorSet.has(lowered)) {
    return true;
  }
  if (actorPattern && actorPattern.test(String(login))) {
    return true;
  }
  return false;
}

function formatMarkdown(summary) {
  const lines = [];
  lines.push("# Codex Feedback Digest");
  lines.push("");
  lines.push(`- Repository: \`${summary.repo}\``);
  lines.push(`- Pull request: \`${summary.pr_number}\``);
  lines.push(`- Generated at (UTC): \`${summary.generated_at_utc}\``);
  lines.push("");

  lines.push(`## Review Summaries (${summary.review_summaries.length})`);
  if (summary.review_summaries.length > 0) {
    for (const item of summary.review_summaries) {
      lines.push(`- [${item.state}] @${item.author} (${item.submitted_at || "unknown time"}) ${item.url}`);
      if (item.body) {
        lines.push(`  - ${item.body}`);
      }
    }
  } else {
    lines.push("- No Codex-authored review summaries found.");
  }

  lines.push("");
  lines.push(`## Inline Comments (${summary.inline_comments.length})`);
  if (summary.inline_comments.length > 0) {
    for (const item of summary.inline_comments) {
      let location = item.path;
      if (item.line !== null && item.line !== undefined) {
        location = `${location}:${item.line}`;
      }
      lines.push(`- @${item.author} on \`${location}\` (${item.created_at || "unknown time"}) ${item.url}`);
      if (item.body) {
        lines.push(`  - ${item.body}`);
      }
    }
  } else {
    lines.push("- No Codex-authored inline comments found.");
  }

  lines.push("");
  lines.push(`## Discussion Comments (${summary.discussion_comments.length})`);
  if (summary.discussion_comments.length > 0) {
    for (const item of summary.discussion_comments) {
      lines.push(`- @${item.author} (${item.created_at || "unknown time"}) ${item.url}`);
      if (item.body) {
        lines.push(`  - ${item.body}`);
      }
    }
  } else {
    lines.push("- No Codex-authored discussion comments found.");
  }

  return lines.join("\n");
}

function main() {
  let args;
  try {
    args = parseArgs(process.argv.slice(2));
  } catch (error) {
    console.log(JSON.stringify({ error: String(error.message || error) }));
    return 2;
  }

  if (args.help) {
    printHelp();
    return 0;
  }

  if (args.prNumber === null) {
    console.log(JSON.stringify({ error: "Missing required argument: <pr_number>" }));
    return 2;
  }

  const actorSet = new Set();
  if (!args.noDefaultActors) {
    for (const actor of DEFAULT_ACTORS) {
      actorSet.add(actor.toLowerCase());
    }
  }
  for (const actor of args.actors) {
    actorSet.add(String(actor).toLowerCase());
  }

  let actorPattern = null;
  if (args.actorRegex) {
    try {
      actorPattern = new RegExp(args.actorRegex, "i");
    } catch (error) {
      console.log(JSON.stringify({ error: `Invalid --actor-regex: ${String(error.message || error)}` }));
      return 2;
    }
  }

  if (actorSet.size === 0 && !actorPattern) {
    console.log(JSON.stringify({ error: "No actor matcher configured; set --actor, --actor-regex, or keep defaults." }));
    return 2;
  }

  let repo;
  let reviews;
  let inlineComments;
  let discussionComments;
  try {
    repo = resolveRepo(args.repo);
    reviews = fetchEndpoint(`repos/${repo}/pulls/${args.prNumber}/reviews`);
    inlineComments = fetchEndpoint(`repos/${repo}/pulls/${args.prNumber}/comments`);
    discussionComments = fetchEndpoint(`repos/${repo}/issues/${args.prNumber}/comments`);
  } catch (error) {
    console.log(JSON.stringify({ error: String(error.message || error) }));
    return 2;
  }

  const filteredReviews = [];
  for (const review of reviews) {
    const login = review.user && review.user.login ? String(review.user.login) : "";
    if (!login || !actorMatches(login, actorSet, actorPattern)) {
      continue;
    }
    filteredReviews.push({
      id: review.id,
      state: review.state,
      author: login,
      submitted_at: review.submitted_at,
      url: review.html_url,
      body: compactText(review.body || "", args.maxBodyLength),
    });
  }

  const filteredInlineComments = [];
  for (const comment of inlineComments) {
    const login = comment.user && comment.user.login ? String(comment.user.login) : "";
    if (!login || !actorMatches(login, actorSet, actorPattern)) {
      continue;
    }
    filteredInlineComments.push({
      id: comment.id,
      author: login,
      path: comment.path,
      line: comment.line,
      side: comment.side,
      created_at: comment.created_at,
      url: comment.html_url,
      body: compactText(comment.body || "", args.maxBodyLength),
    });
  }

  const filteredDiscussionComments = [];
  for (const comment of discussionComments) {
    const login = comment.user && comment.user.login ? String(comment.user.login) : "";
    if (!login || !actorMatches(login, actorSet, actorPattern)) {
      continue;
    }
    filteredDiscussionComments.push({
      id: comment.id,
      author: login,
      created_at: comment.created_at,
      url: comment.html_url,
      body: compactText(comment.body || "", args.maxBodyLength),
    });
  }

  const summary = {
    repo,
    pr_number: args.prNumber,
    generated_at_utc: new Date().toISOString(),
    review_summaries: filteredReviews,
    inline_comments: filteredInlineComments,
    discussion_comments: filteredDiscussionComments,
    configured_actors: Array.from(actorSet).sort(),
    actor_regex: args.actorRegex,
  };

  if (args.format === "json") {
    console.log(JSON.stringify(summary, null, 2));
  } else {
    console.log(formatMarkdown(summary));
  }

  const totalItems = filteredReviews.length + filteredInlineComments.length + filteredDiscussionComments.length;
  if (args.failIfEmpty && totalItems === 0) {
    return 1;
  }
  return 0;
}

process.exit(main());
