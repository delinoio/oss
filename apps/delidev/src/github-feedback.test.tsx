import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { PRFeedback, validPRFeedback } from "./github-feedback";
import { feedbackObservation } from "./github-feedback-fixture";

const item = { base_sha: "a".repeat(40), head_sha: "b".repeat(40), url: "https://github.com/fixture-owner/repo/pull/17" };
it("shows approved feedback, exact bot identity and code context as inert content", () => {
  const value = feedbackObservation(); expect(validPRFeedback(value, item)).toBe(true);
  const view = render(<PRFeedback value={value} />);
  expect(screen.getByRole("region", { name: "Published PR feedback" })).toBeTruthy();
  expect(screen.getByText(/2 unsubmitted drafts excluded/)).toBeTruthy();
  expect(screen.getByText(/Outdated code position/)).toBeTruthy();
  expect(screen.getByText(/Original ID: 9007199254740993/)).toBeTruthy();
  expect(screen.getByText("<script>Approved review feedback</script>")).toBeTruthy();
  expect(view.container.querySelector("script")).toBeNull();
  expect(screen.queryAllByRole("link")).toHaveLength(0);
});
it("keeps provider dismissal and thread resolution separate from local handling", () => {
  const value = feedbackObservation(); value.entries[0].native_state = "DISMISSED"; value.entries[1].review_state = "DISMISSED"; value.threads[0].resolved = true;
  expect(validPRFeedback(value, item)).toBe(true); render(<PRFeedback value={value} />);
  expect(screen.getByText(/GitHub review dismissal is separate/)).toBeTruthy();
  expect(screen.getByText(/Thread resolved on GitHub/)).toBeTruthy();
  expect(screen.getByText("<script>Approved review feedback</script>")).toBeTruthy();
});
it("rejects drafts, mixed review states, duplicate IDs and foreign thread membership", () => {
  for (const mode of ["draft", "state", "duplicate", "thread", "url", "partial", "version", "capacity", "author", "head"]) {
    const value = feedbackObservation();
    switch (mode) {
      case "draft": value.entries[0].native_state = "PENDING"; break;
      case "state": value.entries[1].review_state = "DISMISSED"; break;
      case "duplicate": value.entries.push(value.entries[0]); break;
      case "thread": value.threads[0].comment_nodes.push("OTHER"); break;
      case "url": value.entries[0].url = "https://example.com"; break;
      case "partial": value.entries.splice(0, 1); break;
      case "version": value.entries[0].content_version = "short"; break;
      case "capacity": value.excluded_draft_reviews = 501; break;
      case "author": value.entries[0].author!.native_type = "FutureActor"; break;
      case "head": value.head_sha = "f".repeat(40); break;
    }
    expect(validPRFeedback(value, item), mode).toBe(false);
  }
});
