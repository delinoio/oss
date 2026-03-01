#!/usr/bin/env python3
"""
Check whether a PR has a Codex thumbs-up reaction.

Exit code:
- 0: approval detected
- 1: approval not detected
- 2: execution error
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from typing import Any


DEFAULT_ACTORS = (
    "codex",
    "codex[bot]",
    "openai-codex",
    "openai-codex[bot]",
)


def run_gh(arguments: list[str]) -> str:
    command = ["gh", *arguments]
    process = subprocess.run(command, check=False, capture_output=True, text=True)
    if process.returncode != 0:
        stderr = process.stderr.strip()
        stdout = process.stdout.strip()
        detail = stderr or stdout or f"exit code {process.returncode}"
        raise RuntimeError(f"gh command failed: {' '.join(command)} ({detail})")
    return process.stdout


def resolve_repo(repo: str | None) -> str:
    if repo:
        return repo
    output = run_gh(["repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"]).strip()
    if "/" not in output:
        raise RuntimeError(f"could not resolve repository from gh output: {output!r}")
    return output


def fetch_reactions(repo: str, pr_number: int) -> list[dict[str, Any]]:
    output = run_gh(
        [
            "api",
            "-H",
            "Accept: application/vnd.github+json",
            f"repos/{repo}/issues/{pr_number}/reactions",
            "--method",
            "GET",
            "--field",
            "per_page=100",
        ]
    )
    payload = json.loads(output)
    if not isinstance(payload, list):
        raise RuntimeError("unexpected reactions payload; expected a JSON array")
    return payload


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Check if Codex has left a :+1: reaction on the target PR.",
    )
    parser.add_argument("pr_number", type=int, help="Pull request number")
    parser.add_argument("--repo", help="Repository in owner/repo form")
    parser.add_argument(
        "--actor",
        action="append",
        default=[],
        help="Exact actor login to match (repeatable)",
    )
    parser.add_argument(
        "--actor-regex",
        help="Regex actor matcher (case-insensitive) to complement exact matches",
    )
    parser.add_argument(
        "--no-default-actors",
        action="store_true",
        help="Disable default Codex actor list",
    )
    parser.add_argument(
        "--exit-zero",
        action="store_true",
        help="Always exit 0 after printing JSON output",
    )
    return parser.parse_args()


def actor_matches(login: str, actors: set[str], actor_pattern: re.Pattern[str] | None) -> bool:
    lower_login = login.lower()
    if lower_login in actors:
        return True
    if actor_pattern and actor_pattern.search(login):
        return True
    return False


def main() -> int:
    args = parse_args()

    configured_actors = set()
    if not args.no_default_actors:
        configured_actors.update(actor.lower() for actor in DEFAULT_ACTORS)
    configured_actors.update(actor.lower() for actor in args.actor)

    actor_pattern = None
    if args.actor_regex:
        actor_pattern = re.compile(args.actor_regex, re.IGNORECASE)

    if not configured_actors and actor_pattern is None:
        print(
            json.dumps(
                {
                    "error": "No actor matcher configured; set --actor, --actor-regex, or keep defaults.",
                }
            )
        )
        return 2

    try:
        repo = resolve_repo(args.repo)
        reactions = fetch_reactions(repo, args.pr_number)
    except Exception as error:  # noqa: BLE001
        print(
            json.dumps(
                {
                    "error": str(error),
                    "approved": False,
                }
            )
        )
        return 2

    matches: list[dict[str, Any]] = []
    for reaction in reactions:
        if reaction.get("content") != "+1":
            continue
        user = reaction.get("user") or {}
        login = str(user.get("login", ""))
        if not login:
            continue
        if not actor_matches(login, configured_actors, actor_pattern):
            continue
        matches.append(
            {
                "id": reaction.get("id"),
                "user": login,
                "created_at": reaction.get("created_at"),
            }
        )

    approved = len(matches) > 0
    result = {
        "approved": approved,
        "repo": repo,
        "pr_number": args.pr_number,
        "reaction_count": len(reactions),
        "matched_reaction_count": len(matches),
        "matches": matches,
        "configured_actors": sorted(configured_actors),
        "actor_regex": args.actor_regex,
    }
    print(json.dumps(result, indent=2, sort_keys=True))

    if args.exit_zero:
        return 0
    return 0 if approved else 1


if __name__ == "__main__":
    sys.exit(main())
