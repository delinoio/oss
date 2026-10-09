// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { NativeImageView } from "./native-image-view";

const reference = { reference_id: "01900000-0000-7000-8000-000000000001", machine_id: "01900000-0000-7000-8000-000000000002", manifest_digest: "a".repeat(64), location: "images/original.png" };
const fixture = () => ({ output: null, started: { kind: "image-view", status: "running", image_view: { ...reference } }, completed: { kind: "image-view", status: "completed", image_view: { ...reference } } });

test("presents one inert original image observation without fetching bytes or a live path", () => {
 const fetch = vi.spyOn(globalThis, "fetch");
 const { container } = render(<NativeImageView tool={fixture()} state="complete" />);
 expect(screen.getByText("images/original.png")).toBeTruthy();
 expect(screen.getByText(/Preview unavailable/)).toBeTruthy();
 expect(container.querySelector("details")?.open).toBe(false);
 expect(container.querySelector("a,img,button,input,iframe")).toBeNull();
 expect(fetch).not.toHaveBeenCalled();
 fetch.mockRestore();
});

test.each(["machine_id", "reference_id", "manifest_digest", "location", "repository_id"])("rejects changed %s between original lifecycle observations", field => {
 const tool = fixture();
 Object.assign(tool.completed.image_view, { [field]: field === "location" ? "images/another.png" : field === "manifest_digest" ? "b".repeat(64) : "01900000-0000-7000-8000-000000000003" });
 render(<NativeImageView tool={tool} state="complete" />);
 expect(screen.getByText("This image-view observation is unavailable.")).toBeTruthy();
});

test.each(["../outside.png", "/absolute.png", "https://example.com/image.png", "images//image.png"])("rejects unscoped location %s", location => {
 const tool = fixture(); tool.started.image_view.location = location; tool.completed.image_view.location = location;
 render(<NativeImageView tool={tool} state="complete" />);
 expect(screen.getByText("This image-view observation is unavailable.")).toBeTruthy();
});
