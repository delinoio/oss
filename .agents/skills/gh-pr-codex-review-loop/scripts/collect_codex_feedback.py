#!/usr/bin/env python3
"""
Collect Codex-authored feedback from a pull request.

This script gathers three streams:
1. PR review summaries (`/pulls/{number}/reviews`)
2. Inline review comments (`/pulls/{number}/comments`)
3. Discussion comments (`/issues/{number}/comments`)
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from datetime import datetime, timezone
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


def fetch_endpoint(path: str) -> list[dict[str, Any]]:
    output = run_gh(
        [
            "api",
            "-H",
            "Accept: application/vnd.github+json",
            path,
            "--method",
            "GET",
            "--field",
            "per_page=100",
        ]
    )
    payload = json.loads(output)
    if not isinstance(payload, list):
        raise RuntimeError(f"unexpected payload for endpoint {path!r}; expected a JSON array")
    return payload


def compact_text(text: str | None, max_body_length: int) -> str:
    if not text:
        return ""
    collapsed = " ".join(text.split())
    if len(collapsed) <= max_body_length:
        return collapsed
    keep = max_body_length - 3
    if keep <= 0:
        return "..."
    return f"{collapsed[:keep]}..."


def actor_matches(login: str, actors: set[str], actor_pattern: re.Pattern[str] | None) -> bool:
    lower_login = login.lower()
    if lower_login in actors:
        return True
    if actor_pattern and actor_pattern.search(login):
        return True
    return False


def format_markdown(summary: dict[str, Any]) -> str:
    lines: list[str] = []
    lines.append("# Codex Feedback Digest")
    lines.append("")
    lines.append(f"- Repository: `{summary['repo']}`")
    lines.append(f"- Pull request: `{summary['pr_number']}`")
    lines.append(f"- Generated at (UTC): `{summary['generated_at_utc']}`")
    lines.append("")
    lines.append(f"## Review Summaries ({len(summary['review_summaries'])})")
    if summary["review_summaries"]:
        for item in summary["review_summaries"]:
            lines.append(
                f"- [{item['state']}] @{item['author']} ({item['submitted_at'] or 'unknown time'}) "
                f"{item['url']}"
            )
            if item["body"]:
                lines.append(f"  - {item['body']}")
    else:
        lines.append("- No Codex-authored review summaries found.")
    lines.append("")
    lines.append(f"## Inline Comments ({len(summary['inline_comments'])})")
    if summary["inline_comments"]:
        for item in summary["inline_comments"]:
            location = item["path"]
            if item["line"] is not None:
                location = f"{location}:{item['line']}"
            lines.append(
                f"- @{item['author']} on `{location}` ({item['created_at'] or 'unknown time'}) {item['url']}"
            )
            if item["body"]:
                lines.append(f"  - {item['body']}")
    else:
        lines.append("- No Codex-authored inline comments found.")
    lines.append("")
    lines.append(f"## Discussion Comments ({len(summary['discussion_comments'])})")
    if summary["discussion_comments"]:
        for item in summary["discussion_comments"]:
            lines.append(
                f"- @{item['author']} ({item['created_at'] or 'unknown time'}) {item['url']}"
            )
            if item["body"]:
                lines.append(f"  - {item['body']}")
    else:
        lines.append("- No Codex-authored discussion comments found.")
    return "\n".join(lines)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Collect Codex-authored feedback for a pull request.",
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
        default="codex",
        help="Regex actor matcher (default: codex)",
    )
    parser.add_argument(
        "--no-default-actors",
        action="store_true",
        help="Disable default Codex actor list",
    )
    parser.add_argument(
        "--format",
        choices=["markdown", "json"],
        default="markdown",
        help="Output format",
    )
    parser.add_argument(
        "--max-body-length",
        type=int,
        default=360,
        help="Maximum body characters per item after whitespace compaction",
    )
    parser.add_argument(
        "--fail-if-empty",
        action="store_true",
        help="Exit 1 when no Codex-authored feedback is found",
    )
    return parser.parse_args()


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
        reviews = fetch_endpoint(f"repos/{repo}/pulls/{args.pr_number}/reviews")
        inline_comments = fetch_endpoint(f"repos/{repo}/pulls/{args.pr_number}/comments")
        discussion_comments = fetch_endpoint(f"repos/{repo}/issues/{args.pr_number}/comments")
    except Exception as error:  # noqa: BLE001
        print(json.dumps({"error": str(error)}))
        return 2

    filtered_reviews = []
    for review in reviews:
        login = str((review.get("user") or {}).get("login", ""))
        if not login or not actor_matches(login, configured_actors, actor_pattern):
            continue
        filtered_reviews.append(
            {
                "id": review.get("id"),
                "state": review.get("state"),
                "author": login,
                "submitted_at": review.get("submitted_at"),
                "url": review.get("html_url"),
                "body": compact_text(str(review.get("body") or ""), args.max_body_length),
            }
        )

    filtered_inline_comments = []
    for comment in inline_comments:
        login = str((comment.get("user") or {}).get("login", ""))
        if not login or not actor_matches(login, configured_actors, actor_pattern):
            continue
        filtered_inline_comments.append(
            {
                "id": comment.get("id"),
                "author": login,
                "path": comment.get("path"),
                "line": comment.get("line"),
                "side": comment.get("side"),
                "created_at": comment.get("created_at"),
                "url": comment.get("html_url"),
                "body": compact_text(str(comment.get("body") or ""), args.max_body_length),
            }
        )

    filtered_discussion_comments = []
    for comment in discussion_comments:
        login = str((comment.get("user") or {}).get("login", ""))
        if not login or not actor_matches(login, configured_actors, actor_pattern):
            continue
        filtered_discussion_comments.append(
            {
                "id": comment.get("id"),
                "author": login,
                "created_at": comment.get("created_at"),
                "url": comment.get("html_url"),
                "body": compact_text(str(comment.get("body") or ""), args.max_body_length),
            }
        )

    summary = {
        "repo": repo,
        "pr_number": args.pr_number,
        "generated_at_utc": datetime.now(timezone.utc).isoformat(),
        "review_summaries": filtered_reviews,
        "inline_comments": filtered_inline_comments,
        "discussion_comments": filtered_discussion_comments,
        "configured_actors": sorted(configured_actors),
        "actor_regex": args.actor_regex,
    }

    if args.format == "json":
        print(json.dumps(summary, indent=2, sort_keys=True))
    else:
        print(format_markdown(summary))

    total_items = (
        len(filtered_reviews) + len(filtered_inline_comments) + len(filtered_discussion_comments)
    )
    if args.fail_if_empty and total_items == 0:
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
