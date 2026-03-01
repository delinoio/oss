#!/usr/bin/env node

"use strict";

const { execFileSync } = require("node:child_process");

const DEFAULT_ACTORS = [
  "codex",
  "codex[bot]",
  "openai-codex",
  "openai-codex[bot]",
  "chatgpt-codex-connector[bot]",
];

function printHelp() {
  console.log(`Usage: check_codex_thumbs_up.js <pr_number> [options]

Check whether a PR has a Codex :+1: reaction.

Options:
  --repo <owner/repo>       Repository override
  --actor <login>           Exact actor login to match (repeatable)
  --actor-regex <pattern>   Case-insensitive regex actor matcher
  --no-default-actors       Disable default actor list
  --exit-zero               Always exit with 0 after output
  -h, --help                Show this help message`);
}

function parseArgs(argv) {
  const options = {
    repo: null,
    actors: [],
    actorRegex: null,
    noDefaultActors: false,
    exitZero: false,
    prNumber: null,
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
    if (arg === "--exit-zero") {
      options.exitZero = true;
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

function fetchReactions(repo, prNumber) {
  const out = runGh([
    "api",
    "-H",
    "Accept: application/vnd.github+json",
    `repos/${repo}/issues/${prNumber}/reactions`,
    "--method",
    "GET",
    "--field",
    "per_page=100",
  ]);
  const payload = JSON.parse(out);
  if (!Array.isArray(payload)) {
    throw new Error("unexpected reactions payload; expected a JSON array");
  }
  return payload;
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

function main() {
  let args;
  try {
    args = parseArgs(process.argv.slice(2));
  } catch (error) {
    console.log(JSON.stringify({ error: String(error.message || error), approved: false }));
    return 2;
  }

  if (args.help) {
    printHelp();
    return 0;
  }

  if (args.prNumber === null) {
    console.log(JSON.stringify({ error: "Missing required argument: <pr_number>", approved: false }));
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
      console.log(JSON.stringify({ error: `Invalid --actor-regex: ${String(error.message || error)}`, approved: false }));
      return 2;
    }
  }

  if (actorSet.size === 0 && !actorPattern) {
    console.log(JSON.stringify({ error: "No actor matcher configured; set --actor, --actor-regex, or keep defaults.", approved: false }));
    return 2;
  }

  let repo;
  let reactions;
  try {
    repo = resolveRepo(args.repo);
    reactions = fetchReactions(repo, args.prNumber);
  } catch (error) {
    console.log(JSON.stringify({ error: String(error.message || error), approved: false }));
    return 2;
  }

  const matches = [];
  for (const reaction of reactions) {
    if (reaction.content !== "+1") {
      continue;
    }
    const login = reaction.user && reaction.user.login ? String(reaction.user.login) : "";
    if (!login) {
      continue;
    }
    if (!actorMatches(login, actorSet, actorPattern)) {
      continue;
    }
    matches.push({
      id: reaction.id,
      user: login,
      created_at: reaction.created_at,
    });
  }

  const approved = matches.length > 0;
  const result = {
    approved,
    repo,
    pr_number: args.prNumber,
    reaction_count: reactions.length,
    matched_reaction_count: matches.length,
    matches,
    configured_actors: Array.from(actorSet).sort(),
    actor_regex: args.actorRegex,
  };

  console.log(JSON.stringify(result, null, 2));

  if (args.exitZero) {
    return 0;
  }
  return approved ? 0 : 1;
}

process.exit(main());
