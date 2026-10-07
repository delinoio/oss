// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { act } from "react";
import { expect, it, vi } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";
import { InlineRemediation, Problem, ServiceProblem } from "./ui";
import { copy, i18n } from "./localization";

it("keeps explanation and original guarded actions visible outside technical disclosure", () => {
  const retry = vi.fn();
  const view = render(<Problem error={new ConnectError("Unavailable", Code.Unavailable)} summary="The Runner inventory could not be read." actions={<button disabled onClick={retry}>Retry original read</button>} />);
  const action = screen.getByRole("button", { name: "Retry original read" });
  expect(action.closest("details")).toBeNull();
  expect(screen.getByText("The Runner inventory could not be read.").closest("details")).toBeNull();
  expect(view.container.querySelector("details")?.open).toBe(false);
  fireEvent.click(action);
  expect(retry).not.toHaveBeenCalled();
  view.rerender(<Problem error={new ConnectError("Unavailable", Code.Unavailable)} summary="The Runner inventory could not be read." actions={<button onClick={retry}>Retry original read</button>} />);
  expect(retry).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Retry original read" }));
  expect(retry).toHaveBeenCalledTimes(1);
});

it("renders no remediation for an absent typed failure", () => {
  const retry = vi.fn();
  render(<Problem error={undefined} actions={<button onClick={retry}>Retry</button>} />);
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.queryByRole("button")).toBeNull();
  expect(retry).not.toHaveBeenCalled();
});

it("keeps unsupported manual guidance inert without fabricating a repair action", () => {
  render(<InlineRemediation summary={<p>Ask the administrator of the selected remote computer to install the supported harness.</p>} details={<code>unsupported</code>} />);
  expect(screen.queryByRole("button")).toBeNull();
  expect(screen.getByText(/Ask the administrator/).closest("details")).toBeNull();
});

it("localizes a shared failure without replaying the owner's action", async () => {
  const observe = vi.fn();
  function View() { return <ServiceProblem code="permission_denied" summary={copy("ui.failure.permission_denied")} actions={<button onClick={observe}>{copy("ui.loadMore_ac8991")}</button>}><code>permission_denied</code></ServiceProblem>; }
  const view = render(<View />);
  await act(async () => { await i18n.changeLanguage("ko"); });
  view.rerender(<View />);
  expect(screen.getByRole("button").closest("details")).toBeNull();
  expect(observe).not.toHaveBeenCalled();
});
