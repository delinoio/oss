import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ReviewComment } from "../../lib/mock-data";
import { InlineCommentThread } from "./inline-comment-thread";

const ACTIVE_COMMENT: ReviewComment = {
  reviewCommentId: "comment-1",
  body: "Please simplify this branch logic.",
  filePath: "src/app.ts",
  side: "RIGHT",
  lineNumber: 42,
  status: "ACTIVE",
  prTrackingId: "pr-1",
  createdAt: "2026-03-14T00:00:00Z",
  updatedAt: "2026-03-14T00:00:00Z",
};

describe("InlineCommentThread", () => {
  it("auto-focuses reply textarea when reply editor opens", async () => {
    const user = userEvent.setup();
    render(
      <InlineCommentThread
        filePath={ACTIVE_COMMENT.filePath}
        lineNumber={ACTIVE_COMMENT.lineNumber}
        side={ACTIVE_COMMENT.side}
        comments={[ACTIVE_COMMENT]}
        onReply={vi.fn()}
        onResolve={vi.fn()}
        onReopen={vi.fn()}
        onDelete={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "+ Reply" }));
    const replyInput = screen.getByPlaceholderText("Reply... (Cmd+Enter to submit)");

    await waitFor(() => {
      expect(document.activeElement).toBe(replyInput);
    });
  });
});
