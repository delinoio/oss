// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { RepositoryCloneFields, repositoryCloneDirectory, repositoryCloneParent, repositoryClonePath, repositoryCloneURL, type RepositoryCloneDraft } from "./repository-clone-fields";

it("derives an editable folder and final path without replacing a user edit", () => {
  function View() {
    const [draft, change] = useState<RepositoryCloneDraft>({ url: "", parent: "" });
    return <RepositoryCloneFields draft={draft} change={change} busy={false} browse={() => {}} supported />;
  }
  render(<View />);
  fireEvent.change(screen.getByRole("textbox", { name: "Git URL" }), { target: { value: "https://github.com/owner/repo.git" } });
  expect((screen.getByRole("textbox", { name: "Repository folder name" }) as HTMLInputElement).value).toBe("repo");
  fireEvent.change(screen.getByRole("textbox", { name: "Clone to" }), { target: { value: "/parent" } });
  expect(screen.getByText("Final path: /parent/repo")).toBeTruthy();
  fireEvent.change(screen.getByRole("textbox", { name: "Repository folder name" }), { target: { value: "my repo" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Git URL" }), { target: { value: "git@github.com:owner/repo.git" } });
  expect(screen.getByText("Final path: /parent/my repo")).toBeTruthy();
});
it("keeps credential guidance and old-server update guidance separate from folder registration", () => {
  const browse = vi.fn();
  render(<RepositoryCloneFields draft={{ url: "", parent: "" }} change={() => {}} busy={false} browse={browse} supported={false} />);
  fireEvent.click(screen.getByRole("button", { name: "Browse…" })); expect(browse).toHaveBeenCalledOnce();
  expect(screen.getByText("Private repositories use this computer's Git or SSH credentials.")).toBeTruthy();
  expect(screen.getByRole("status").textContent).toContain("Update the selected server");
});
it.each(["https://github.com/owner/repo.git", "ssh://git@github.com/owner/repo.git", "git@github.com:owner/repo.git"])("retains the same GitHub identity for %s", value => {
  expect(repositoryCloneURL(value)).toEqual({ directory: "repo", githubOwner: "owner", githubName: "repo" });
});
it.each(["/tmp/repo", "C:/repo", "ext::helper", "https://token@host/repo", "https://user:pass@host/repo", "https://host/repo?token=secret", "https://host/repo%00", "https://host/../repo", "https://host/%2e%2e/repo", "ssh://user:pass@host/repo"])("rejects unsafe presentation URL %s", value => {
  expect(repositoryCloneURL(value)).toBeUndefined();
});
it("validates portable names and displays native Windows and Unix paths", () => {
  for (const value of ["repo", "my repo", "한글"]) expect(repositoryCloneDirectory(value)).toBe(true);
  for (const value of ["..", "a/b", "CON", "nul.txt", "trailing.", "trailing "]) expect(repositoryCloneDirectory(value)).toBe(false);
  expect(repositoryCloneParent("/parent")).toBe(true); expect(repositoryCloneParent("C:\\parent")).toBe(true); expect(repositoryCloneParent("relative")).toBe(false);
  expect(repositoryClonePath("C:\\parent\\", "repo")).toBe("C:\\parent\\repo"); expect(repositoryClonePath("/", "repo")).toBe("/repo");
});
