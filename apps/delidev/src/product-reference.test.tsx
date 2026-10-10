// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ProductReference, ProductReferenceProvider, ProductReferenceKind } from "./product-reference";
it("keeps original user text while generated references retain their connection labels after reorder", () => {
  const a = "01900000-0000-7000-8000-000000000001", b = "01900000-0000-7000-8000-000000000002";
  const content = (ids: string[]) => <ProductReferenceProvider>{ids.map(id => <span key={id}><ProductReference value={id} kind={ProductReferenceKind.Backup} /></span>)}<pre>{a}</pre></ProductReferenceProvider>;
  const view = render(content([a, b]));
  expect(screen.getByText("Backup 1")).toBeTruthy(); expect(screen.getByText("Backup 2")).toBeTruthy();
  view.rerender(content([b, a]));
  expect([...view.container.querySelectorAll("span")].map(node => node.textContent)).toEqual(["Backup 2", "Backup 1"]);
  expect(screen.getByText(a)).toBeTruthy();
});
it("keeps generated failure prose UUID-free while retaining the original failure and user/native content", async () => {
  const { Failure } = await import("./ui");
  const { FailureCode } = await import("@delinoio/delidev-api-client");
  const id = "01900000-0000-7000-8000-000000000001";
  const failure = { code: FailureCode.Internal, message: `Cannot inspect ${id}.`, guidance: "Retry the original inspection.", correlationId: id };
  const view = render(<ProductReferenceProvider><Failure failure={failure} /><pre>{id}</pre></ProductReferenceProvider>);
  const error = view.container.querySelector('[role="alert"]')!;
  expect(error.textContent).not.toContain(id);
  expect(error.textContent).toContain("Retry the original inspection.");
  expect(screen.getByText(id)).toBeTruthy();
  expect(failure.correlationId).toBe(id);
  expect(failure.message).toContain(id);
});
