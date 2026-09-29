import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { PullRequestProblemCollectionKind } from "@delinoio/delidev-api-client";
import { prAllowanceKey, prSelectionKey, PRWorkflowProvider, usePRWorkflow } from "./pr-workflow";

const selection = { repositoryId: "local-repository", remoteRepositoryId: "9007199254740993", pullRequestId: "9007199254740995", number: "17" };

function Harness() {
  const [visible, setVisible] = useState(true);
  return <PRWorkflowProvider><button onClick={() => setVisible((value) => !value)}>{visible ? "Hide detail" : "Show detail"}</button>{visible ? <WorkflowDetail /> : null}</PRWorkflowProvider>;
}

function WorkflowDetail() {
  const workflow = usePRWorkflow();
  const key = prAllowanceKey(selection), selectionKey = prSelectionKey(selection);
  return <section>
    <button onClick={() => workflow.confirmAllowance({ key, selection, id: "set-id", revision: 9007199254740993n })}>Stage confirmation</button>
    <label>Collection kind<select value={workflow.collectionKinds.get(selectionKey) ?? PullRequestProblemCollectionKind.FEEDBACK} onChange={(event) => workflow.setCollectionKind(selectionKey, Number(event.target.value) as PullRequestProblemCollectionKind)}><option value={PullRequestProblemCollectionKind.FEEDBACK}>Feedback</option><option value={PullRequestProblemCollectionKind.CI}>CI</option></select></label>
    <output>{workflow.confirmations.get(key) ? `${workflow.confirmations.get(key)!.id}:${workflow.confirmations.get(key)!.revision}` : "No confirmation"}</output>
    <button onClick={() => workflow.cancelAllowance(key)}>Cancel confirmation</button>
  </section>;
}

it("retains PR detail drafts and unsent confirmations across temporary unmounts", () => {
  render(<Harness />);
  fireEvent.change(screen.getByLabelText("Collection kind"), { target: { value: String(PullRequestProblemCollectionKind.CI) } });
  fireEvent.click(screen.getByRole("button", { name: "Stage confirmation" }));
  expect(screen.getByText("set-id:9007199254740993")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Hide detail" }));
  fireEvent.click(screen.getByRole("button", { name: "Show detail" }));
  expect((screen.getByLabelText("Collection kind") as HTMLSelectElement).value).toBe(String(PullRequestProblemCollectionKind.CI));
  expect(screen.getByText("set-id:9007199254740993")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel confirmation" }));
  expect(screen.getByText("No confirmation")).toBeTruthy();
});
