// SPDX-License-Identifier: Apache-2.0
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { FailureCode } from "@delinoio/delidev-api-client";
import { ProductIdentity, ProductIdentityScope } from "./product-identity-labels";
import { ProductIdentityKind } from "./product-identity";
import { SettingsActionButton, SettingsActionIcon, SettingsActionScope } from "./settings-action";
import { Failure } from "./ui";

const first = "123e4567-e89b-12d3-a456-426614174000";
const second = "123e4567-e89b-12d3-a456-426614174001";
describe("product identity presentation", () => {
  it("keeps equal names separate and stable after reorder without name reads", () => {
    const list = (ids: string[]) => <ProductIdentityScope scope="original-connection">{ids.map(id => <p key={id}><ProductIdentity id={id} kind={ProductIdentityKind.Project} name="Same name" numbered /></p>)}</ProductIdentityScope>;
    const view = render(list([first, second]));
    expect(view.container.textContent).toContain("Same name · Project 1");
    expect(view.container.textContent).toContain("Same name · Project 2");
    view.rerender(list([second, first]));
    expect([...view.container.querySelectorAll("p")].map(row => row.textContent)).toEqual(["Same name · Project 2", "Same name · Project 1"]);
    expect(view.container.textContent).not.toContain(first);
    expect(view.container.textContent).not.toContain(second);
  });
  it("keeps UUID-shaped user names and original mutation identity", () => {
    const invoke = vi.fn();
    const view = render(<ProductIdentityScope scope="original-view"><ProductIdentity id={second} kind={ProductIdentityKind.Account} name={first} numbered /><SettingsActionScope><SettingsActionButton icon={SettingsActionIcon.Delete} targetId={second} onClick={() => invoke(second)}>Delete</SettingsActionButton></SettingsActionScope></ProductIdentityScope>);
    expect(view.container.textContent).toContain(first);
    expect(screen.getByRole("button").getAttribute("aria-label")).not.toContain(second);
    fireEvent.click(screen.getByRole("button"));
    expect(invoke).toHaveBeenCalledWith(second);
  });
  it("omits error correlation rows and sanitizes only owned diagnostic prose", () => {
    const failure = { code: FailureCode.Unavailable, message: `Request ${first} failed`, guidance: "Retry the same request", correlationId: second };
    const view = render(<ProductIdentityScope scope="failure-view"><Failure failure={failure} summary={<p>{first}</p>} /></ProductIdentityScope>);
    expect(view.container.querySelector("strong")?.textContent).toBeTruthy();
    expect(view.container.textContent).toContain("Request Reference 1 failed");
    expect(view.container.textContent).not.toContain(second);
    expect(view.container.textContent).toContain(first);
    expect(failure.message).toContain(first);
    expect(failure.correlationId).toBe(second);
  });
});
