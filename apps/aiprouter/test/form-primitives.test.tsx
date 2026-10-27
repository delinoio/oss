// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect } from "@rstest/core";
import { Dialog, DialogSize, ErrorSummary, Field } from "../src/ui";
import { exceedsUtf8Bytes, utf8ByteLength } from "../src/validation";

describe("utf8ByteLength", () => {
  it("counts ASCII, multibyte and astral characters as UTF-8 bytes", () => {
    expect(utf8ByteLength("")).toBe(0);
    expect(utf8ByteLength("a".repeat(512))).toBe(512);
    expect(utf8ByteLength("한")).toBe(3);
    expect(utf8ByteLength("😀")).toBe(4);
    expect("한".repeat(170) + "aa").toHaveLength(172);
    expect(utf8ByteLength("한".repeat(170) + "aa")).toBe(512);
    expect(utf8ByteLength("한".repeat(170) + "aaa")).toBe(513);
  });
  it("reports values over the limit only at limit + 1 bytes", () => {
    expect(exceedsUtf8Bytes("a".repeat(512), 512)).toBe(false);
    expect(exceedsUtf8Bytes("a".repeat(513), 512)).toBe(true);
    expect(exceedsUtf8Bytes("한".repeat(170) + "aa", 512)).toBe(false);
    expect(exceedsUtf8Bytes("한".repeat(170) + "aaa", 512)).toBe(true);
  });
});

describe("Field", () => {
  it("keeps the previous hint-only association and omits aria-invalid", () => {
    render(
      <Field label="Name" hint="Pick a name.">
        <input />
      </Field>,
    );
    const input = screen.getByLabelText("Name");
    expect(input.getAttribute("aria-invalid")).toBeNull();
    expect(input.getAttribute("aria-describedby")).toBe(
      screen.getByText("Pick a name.").id,
    );
  });
  it("associates an error with the control and marks it invalid", () => {
    render(
      <Field label="Name" hint="Pick a name." error="Name is required.">
        <input />
      </Field>,
    );
    const input = screen.getByLabelText("Name");
    const hint = screen.getByText("Pick a name.");
    const error = screen.getByText("Name is required.");
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.getAttribute("aria-describedby")).toBe(
      `${hint.id} ${error.id}`,
    );
    expect(error.className).toContain("field-error");
  });
  it("describes the control with only the error when no hint exists", () => {
    render(
      <Field label="Name" error="Name is required.">
        <input />
      </Field>,
    );
    expect(screen.getByLabelText("Name").getAttribute("aria-describedby")).toBe(
      screen.getByText("Name is required.").id,
    );
  });
  it("removes the invalid state when the error clears", () => {
    const { rerender } = render(
      <Field label="Name" error="Bad.">
        <input />
      </Field>,
    );
    rerender(
      <Field label="Name">
        <input />
      </Field>,
    );
    const input = screen.getByLabelText("Name");
    expect(input.getAttribute("aria-invalid")).toBeNull();
    expect(input.getAttribute("aria-describedby")).toBeNull();
  });
});

describe("ErrorSummary", () => {
  it("renders nothing without errors", () => {
    const { container } = render(<ErrorSummary errors={[]} />);
    expect(container.firstChild).toBeNull();
  });
  it("lists every cause as an assertive alert and focuses the owning field", async () => {
    const user = userEvent.setup();
    render(
      <>
        <ErrorSummary
          errors={[
            { key: "alias", message: "Alias too long.", fieldId: "alias" },
            { key: "count", message: "Too many targets." },
          ]}
        />
        <input id="alias" aria-label="Alias" />
      </>,
    );
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("Fix the following before saving");
    expect(screen.getByText("Too many targets.")).toBeTruthy();
    await user.click(screen.getByRole("link", { name: "Alias too long." }));
    expect(document.activeElement).toBe(screen.getByLabelText("Alias"));
  });
});

describe("Dialog chrome", () => {
  it("renders a subtitle as the dialog description", () => {
    render(
      <Dialog
        title="Edit routing"
        subtitle="Only yours in Acme Studio."
        onClose={() => {}}
      >
        <input aria-label="Alias" />
      </Dialog>,
    );
    const dialog = screen.getByRole("dialog", { name: "Edit routing" });
    const subtitle = screen.getByText("Only yours in Acme Studio.");
    expect(dialog.getAttribute("aria-describedby")).toBe(subtitle.id);
    expect(screen.getByRole("button", { name: "Close dialog" })).toBeTruthy();
  });
  it("omits the description when no subtitle is given", () => {
    render(
      <Dialog title="Plain" onClose={() => {}}>
        <input aria-label="Alias" />
      </Dialog>,
    );
    expect(
      screen
        .getByRole("dialog", { name: "Plain" })
        .getAttribute("aria-describedby"),
    ).toBeNull();
  });
  it("applies size classes and keeps the legacy wide prop", () => {
    const { rerender } = render(
      <Dialog title="Large" size={DialogSize.Large} onClose={() => {}}>
        <input aria-label="Alias" />
      </Dialog>,
    );
    expect(screen.getByRole("dialog", { name: "Large" }).className).toBe(
      "large",
    );
    rerender(
      <Dialog title="Large" wide onClose={() => {}}>
        <input aria-label="Alias" />
      </Dialog>,
    );
    expect(screen.getByRole("dialog", { name: "Large" }).className).toBe(
      "wide",
    );
    rerender(
      <Dialog title="Large" onClose={() => {}}>
        <input aria-label="Alias" />
      </Dialog>,
    );
    expect(screen.getByRole("dialog", { name: "Large" }).className).toBe("");
  });
});
