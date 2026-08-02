import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, describe, expect, it } from "vitest";

import { CaptureMode, PointerInclusion } from "../capture";
import { RealQaWorkspace, serializeFinalIssueBody } from "./RealQaWorkspace";
import {
  RealQaAccessMode,
  RealQaDesktopFamily,
  RealQaFailureCode,
  RealQaProductError,
  RealQaSelectorMode,
  type RealQaProductAction,
  type RealQaProductGateway,
  type RealQaProductSnapshot,
} from "./contracts";

const presetId = "01900000-0000-7000-8000-000000000001";
const draftId = "01900000-0000-7000-8000-000000000002";
const imageId = "01900000-0000-7000-8000-000000000003";

afterEach(cleanup);

function snapshot(
  platform = RealQaDesktopFamily.Ubuntu,
  access = RealQaAccessMode.Online,
): RealQaProductSnapshot {
  return {
    platform,
    access,
    online: access === RealQaAccessMode.Online,
    destinations: [{
      destinationId: "destination-1",
      repository: "delinoio/oss",
      connected: true,
      revision: 3,
    }],
    definitions: [{
      definitionId: "bug-form",
      name: "Bug report",
      kind: "issue-form",
      issueType: "Bug",
      fields: [
        { fieldId: "summary", kind: "input", label: "Summary", required: true, defaultValue: "" },
        { fieldId: "logs", kind: "textarea", label: "Logs", required: false, defaultValue: "", renderLanguage: "shell" },
        { fieldId: "severity", kind: "dropdown", label: "Severity", required: true, multiple: false, options: ["Low", "High"], defaultValue: "High" },
        { fieldId: "browsers", kind: "dropdown", label: "Browsers", required: false, multiple: true, options: ["Chrome", "Firefox"], defaultValue: "" },
        { fieldId: "checks", kind: "checkboxes", label: "Checks", required: true, options: ["Reproduced", "Sanitized"] },
      ],
    }],
    presets: [{
      presetId,
      revision: 4,
      name: "Release QA",
      captureMode: CaptureMode.Region,
      pointer: PointerInclusion.Exclude,
      selectorMode: RealQaSelectorMode.Dom,
      destinationId: "destination-1",
      definitionId: "bug-form",
      labels: ["bug"],
      assignees: ["octocat"],
      milestone: "v1",
      projects: ["Release"],
      payer: "Acme / QA",
      backgroundGrant: "active",
      shortcut: "Control+Shift+7",
    }],
    drafts: [{
      draftId,
      revision: 2,
      presetId,
      title: "Captured regression",
      body: "Steps to reproduce",
      url: "https://example.com/report",
      urlWarning: false,
      environment: [
        { id: "os", label: "Operating system", value: platform },
        { id: "arch", label: "Architecture", value: "x86_64" },
      ],
      dom: [{ id: "selector", label: "CSS selector", value: "main > button" }],
      issueAnswers: {},
      labels: ["bug"],
      assignees: ["octocat"],
      milestone: "v1",
      projects: ["Release"],
      images: [{
        imageId,
        revision: 1,
        name: "Capture 1",
        encodedBytes: 8192,
        selected: true,
        uploadState: "local",
        uploadProgress: 0,
      }],
    }],
    submissions: [{
      submissionId: "01900000-0000-7000-8000-000000000004",
      revision: 8,
      state: "reconciling",
      issueUrl: null,
      graceExpiresAt: null,
      authorizationId: "01900000-0000-7000-8000-000000000005",
      authorizationRevision: 7,
      images: [{
        imageId: "01900000-0000-7000-8000-000000000006",
        revision: 9,
        name: "Submitted capture",
        encodedBytes: 8192,
        selected: true,
        uploadState: "public",
        uploadProgress: 100,
      }],
    }],
    replacementBillingScopes: [{
      organizationId: "01900000-0000-7000-8000-000000000008",
      teamId: "01900000-0000-7000-8000-000000000009",
      label: "Acme / Release",
    }],
  };
}

class FixtureGateway implements RealQaProductGateway {
  readonly actions: RealQaProductAction[] = [];
  nextFailure: RealQaProductError | null = null;

  constructor(public value: RealQaProductSnapshot) {}

  async load() {
    return this.value;
  }

  async execute(
    action: RealQaProductAction,
    reportProgress: (progress: number) => void,
  ) {
    this.actions.push(action);
    if (this.nextFailure) {
      const failure = this.nextFailure;
      this.nextFailure = null;
      throw failure;
    }
    if (action.kind === "connect-destination") {
      return this.value;
    }
    if (action.kind === "create-preset") {
      this.value = {
        ...this.value,
        presets: [{
          ...action.preset,
          presetId,
          revision: 1,
        }, ...this.value.presets],
      };
    }
    if (action.kind === "save-preset") {
      this.value = {
        ...this.value,
        presets: this.value.presets.map((preset) =>
          preset.presetId === action.preset.presetId
            ? { ...action.preset, revision: preset.revision + 1 }
            : preset,
        ),
      };
    }
    if (action.kind === "create-draft") {
      const template = snapshot().drafts[0];
      if (template === undefined) throw new Error("Fixture draft is missing.");
      this.value = {
        ...this.value,
        drafts: [{
          ...template,
          draftId,
          presetId: action.presetId,
          revision: 1,
          title: "",
          body: "",
          issueAnswers: {},
          images: [],
        }, ...this.value.drafts],
      };
    }
    if (action.kind === "capture") {
      this.value = {
        ...this.value,
        drafts: this.value.drafts.map((draft) =>
          draft.draftId === action.draftId
            ? {
                ...draft,
                images: [...draft.images, {
                  imageId: `capture-${draft.images.length + 1}`,
                  revision: 1,
                  name: `Capture ${draft.images.length + 1}`,
                  encodedBytes: 4096,
                  selected: true,
                  uploadState: "local" as const,
                  uploadProgress: 0,
                }],
              }
            : draft,
        ),
      };
    }
    if (action.kind === "save-draft") {
      this.value = {
        ...this.value,
        drafts: this.value.drafts.map((draft) =>
          draft.draftId === action.draft.draftId
            ? { ...action.draft, revision: draft.revision + 1 }
            : draft,
        ),
      };
    }
    if (action.kind === "delete-draft") {
      this.value = {
        ...this.value,
        drafts: this.value.drafts.filter((draft) => draft.draftId !== action.draftId),
      };
    }
    if (action.kind === "submit") {
      for (const progress of [10, 40, 75, 100]) reportProgress(progress);
      this.value = {
        ...this.value,
        drafts: this.value.drafts.filter((draft) => draft.draftId !== action.draft.draftId),
        submissions: [{
          submissionId: action.draft.draftId,
          revision: 1,
          state: "submitted",
          issueUrl: "https://github.com/delinoio/oss/issues/757",
          graceExpiresAt: null,
          authorizationId: "01900000-0000-7000-8000-000000000007",
          authorizationRevision: 1,
          images: action.draft.images.map((image) => ({ ...image, uploadState: "public", uploadProgress: 100 })),
        }, ...this.value.submissions],
      };
    }
    if (action.kind === "retry-submission") {
      this.value = {
        ...this.value,
        submissions: this.value.submissions.map((submission) =>
          submission.submissionId === action.submissionId
            ? { ...submission, state: "submitted", issueUrl: "https://github.com/delinoio/oss/issues/757" }
            : submission,
        ),
      };
    }
    if (action.kind === "disconnect-destination") {
      this.value = {
        ...this.value,
        destinations: this.value.destinations.map((destination) =>
          destination.destinationId === action.destinationId
            ? { ...destination, connected: false, revision: destination.revision + 1 }
            : destination,
        ),
      };
    }
    if (action.kind === "delete-preset") {
      this.value = {
        ...this.value,
        presets: this.value.presets.filter(
          (preset) => preset.presetId !== action.presetId,
        ),
      };
    }
    if (action.kind === "delete-feature-data") {
      this.value = { ...this.value, presets: [], submissions: [] };
    }
    return this.value;
  }
}

async function renderWorkspace(gateway: FixtureGateway) {
  const result = render(<RealQaWorkspace gateway={gateway} />);
  await screen.findByRole("heading", { name: "Capture and review" });
  return result;
}

describe("RealQA desktop production workspace", () => {
  it.each(Object.values(RealQaDesktopFamily))(
    "completes the fixture capture-to-issue flow on %s",
    async (platform) => {
      const user = userEvent.setup();
      const gateway = new FixtureGateway(snapshot(platform));
      await renderWorkspace(gateway);

      expect(screen.getByLabelText("Operating system")).toHaveValue(platform);
      await user.selectOptions(screen.getByLabelText("Mode", { selector: "select" }), CaptureMode.MultiMonitor);
      await user.click(screen.getByLabelText("Include pointer"));
      await user.clear(screen.getByLabelText("Width"));
      await user.type(screen.getByLabelText("Width"), "800");
      await user.click(screen.getByRole("button", { name: "Capture screenshot" }));
      expect(await screen.findByText(/Capture added/u)).toBeVisible();
      expect(gateway.actions.at(-1)).toMatchObject({
        kind: "capture",
        captureMode: CaptureMode.MultiMonitor,
        pointer: PointerInclusion.Include,
        selection: { width: 800 },
      });
      const editButton = screen.getAllByRole("button", { name: "Edit nondestructively" })[0];
      if (editButton === undefined) throw new Error("Fixture image edit action is missing.");
      await user.click(editButton);
      expect(gateway.actions.at(-1)).toMatchObject({ kind: "edit-image", imageId });

      await user.type(screen.getByLabelText("Summary"), "Viewport regression");
      await user.selectOptions(screen.getByLabelText("Severity"), "High");
      await user.selectOptions(screen.getByLabelText("Browsers"), ["Chrome", "Firefox"]);
      await user.click(screen.getByLabelText("Reproduced"));
      await user.click(screen.getByRole("button", { name: "Review and submit" }));
      const dialog = screen.getByRole("dialog", { name: "Confirm public screenshots" });
      expect(dialog).toHaveTextContent("Anyone with the GitHub issue URL or an image URL");
      await user.click(within(dialog).getByRole("button", { name: "Confirm and submit" }));

      await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
        kind: "submit",
        publicImageConfirmation: true,
      }));
      expect(await screen.findByText(/local raw draft was deleted/u)).toBeVisible();
      expect(screen.getByText("https://github.com/delinoio/oss/issues/757")).toBeVisible();
    },
    10_000,
  );

  it("keeps prior-bound offline drafts editable but disables synchronized and remote actions", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot(RealQaDesktopFamily.Macos, RealQaAccessMode.PriorBoundOffline));
    await renderWorkspace(gateway);
    expect(screen.getByRole("heading", { name: "Offline draft mode" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Save preset" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Capture screenshot" })).toBeEnabled();
    await user.clear(screen.getByLabelText("Issue title"));
    await user.type(screen.getByLabelText("Issue title"), "Offline edit");
    await user.click(screen.getByRole("button", { name: "Save draft" }));
    expect(gateway.actions.at(-1)).toMatchObject({ kind: "save-draft" });
  });

  it("enforces the 20-shortcut device limit without discarding preset data", async () => {
    const value = snapshot();
    const template = value.presets[0];
    if (template === undefined) throw new Error("Fixture preset is missing.");
    const gateway = new FixtureGateway({
      ...value,
      presets: [
        { ...template, presetId: "preset-without-shortcut", name: "Unbound preset", shortcut: "" },
        ...Array.from({ length: 20 }, (_, index) => ({
          ...template,
          presetId: `shortcut-${index}`,
          name: `Shortcut ${index + 1}`,
          shortcut: `Control+Shift+${index + 1}`,
        })),
      ],
    });
    await renderWorkspace(gateway);

    expect(screen.getByText("20/20 shortcuts")).toBeVisible();
    expect(screen.getByLabelText("Global shortcut")).toBeDisabled();
    expect(screen.getByLabelText("Name")).toHaveValue("Unbound preset");
    expect(screen.getByText(/Conflicts remain inactive\./u)).toBeVisible();
  });

  it("reviews editable URLs and submits only the sanitized value", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);
    const url = screen.getByLabelText("Sanitized URL");

    await user.clear(url);
    await user.type(url, "file:///tmp/private");
    expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent("HTTP or HTTPS");

    await user.clear(url);
    await user.type(url, "https://example.com/report?token=private#trace");
    await user.tab();
    expect(url).toHaveValue("https://example.com/report");
    await user.click(screen.getByRole("button", { name: "Review and submit" }));
    await user.click(within(screen.getByRole("dialog", { name: "Confirm public screenshots" })).getByRole("button", { name: "Confirm and submit" }));
    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "submit",
      draft: { url: "https://example.com/report" },
    }));
  });

  it("surfaces closed typed failures without rendering provider content", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    gateway.nextFailure = new RealQaProductError(RealQaFailureCode.StaleRevision);
    await renderWorkspace(gateway);
    await user.click(screen.getByRole("button", { name: "Save preset" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Reload it, compare your changes");
    expect(document.body).not.toHaveTextContent("provider response");
  });

  it("uses the refreshed preset revision for later saves and deletion", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Save preset" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(1));
    await user.click(screen.getByRole("button", { name: "Save preset" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(2));
    expect(gateway.actions[1]).toMatchObject({
      kind: "save-preset",
      preset: { revision: 5 },
    });

    await user.click(screen.getByRole("button", { name: "Delete preset" }));
    await user.click(within(screen.getByRole("dialog", { name: "Delete RealQA preset" })).getByRole("button", { name: "Delete preset" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(3));
    expect(gateway.actions[2]).toMatchObject({
      kind: "delete-preset",
      expectedRevision: 6,
    });
    expect(await screen.findByText("No presets are available.")).toBeVisible();
  });

  it("selects the next retained preset after deleting the active preset", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const active = value.presets[0];
    if (active === undefined) throw new Error("Fixture preset is missing.");
    const gateway = new FixtureGateway({
      ...value,
      presets: [active, { ...active, presetId: "preset-2", name: "Retained preset" }],
    });
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Delete preset" }));
    await user.click(within(screen.getByRole("dialog", { name: "Delete RealQA preset" })).getByRole("button", { name: "Delete preset" }));

    await waitFor(() => expect(screen.getByLabelText("Preset", { selector: "select" })).toHaveValue("preset-2"));
    expect(screen.getByLabelText("Name")).toHaveValue("Retained preset");
  });

  it("resets capture overrides to each selected draft's preset defaults", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const preset = value.presets[0];
    const draft = value.drafts[0];
    if (preset === undefined || draft === undefined) {
      throw new Error("Fixture preset and draft are missing.");
    }
    const gateway = new FixtureGateway({
      ...value,
      presets: [
        {
          ...preset,
          captureMode: CaptureMode.MultiMonitor,
          pointer: PointerInclusion.Include,
          selectorMode: RealQaSelectorMode.Dom,
        },
        {
          ...preset,
          presetId: "preset-2",
          captureMode: CaptureMode.Window,
          pointer: PointerInclusion.Exclude,
          selectorMode: RealQaSelectorMode.Normal,
        },
      ],
      drafts: [draft, { ...draft, draftId: "draft-2", presetId: "preset-2" }],
    });
    await renderWorkspace(gateway);

    expect(screen.getByLabelText("Mode", { selector: "select" })).toHaveValue(CaptureMode.MultiMonitor);
    expect(screen.getByLabelText("Include pointer")).toBeChecked();
    expect(screen.getByLabelText("Select a DOM target in Chrome")).toBeChecked();

    await user.selectOptions(screen.getByLabelText("Draft"), "draft-2");
    await waitFor(() => {
      expect(screen.getByLabelText("Mode", { selector: "select" })).toHaveValue(CaptureMode.Window);
      expect(screen.getByLabelText("Include pointer")).not.toBeChecked();
      expect(screen.getByLabelText("Select a DOM target in Chrome")).not.toBeChecked();
    });
  });

  it("selects the next retained draft after submission removes the current draft", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const first = value.drafts[0];
    if (first === undefined) throw new Error("Fixture draft is missing.");
    const gateway = new FixtureGateway({
      ...value,
      drafts: [first, { ...first, draftId: "draft-2", title: "Retained draft" }],
    });
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Review and submit" }));
    await user.click(within(screen.getByRole("dialog", { name: "Confirm public screenshots" })).getByRole("button", { name: "Confirm and submit" }));

    await waitFor(() => expect(screen.getByLabelText("Draft")).toHaveValue("draft-2"));
    expect(screen.getByLabelText("Issue title")).toHaveValue("Retained draft");
  });

  it("measures form answers and editable metadata in the serialized final body", async () => {
    const value = snapshot();
    const draft = value.drafts[0];
    const definition = value.definitions[0];
    if (draft === undefined || definition === undefined) {
      throw new Error("Fixture content is missing.");
    }
    const oversized = {
      ...draft,
      environment: [{ id: "logs", label: "Logs", value: "x".repeat(60_000) }],
    };
    expect(new TextEncoder().encode(serializeFinalIssueBody(oversized, definition)).byteLength).toBeGreaterThan(60_000);
    const gateway = new FixtureGateway({ ...value, drafts: [oversized] });
    await renderWorkspace(gateway);

    expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
    expect(screen.getByText(/60,000 body bytes/u)).toHaveClass("error");
  });

  it("materializes visible issue-form defaults in saved and submitted drafts", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);

    expect(screen.getByLabelText("Severity")).toHaveValue("High");
    await user.click(screen.getByRole("button", { name: "Save draft" }));
    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "save-draft",
      draft: { issueAnswers: { severity: ["High"] } },
    }));
    await user.click(screen.getByRole("button", { name: "Review and submit" }));
    await user.click(within(screen.getByRole("dialog", { name: "Confirm public screenshots" })).getByRole("button", { name: "Confirm and submit" }));
    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "submit",
      draft: { issueAnswers: { severity: ["High"] } },
    }));
  });

  it("offers first-preset creation and first-draft creation", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const gateway = new FixtureGateway({ ...value, presets: [], drafts: [] });
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Create first preset" }));
    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({ kind: "create-preset" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      idempotencyKey: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u),
    });
    expect(await screen.findByRole("button", { name: "Create draft" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Create draft" }));
    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "create-draft",
      presetId,
    }));
    expect(await screen.findByLabelText("Issue title")).toHaveValue("");
  });

  it("reuses the CreatePreset identity after an ambiguous failure", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const gateway = new FixtureGateway({ ...value, presets: [], drafts: [] });
    gateway.nextFailure = new RealQaProductError(RealQaFailureCode.ServiceUnavailable);
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Create first preset" }));
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Create first preset" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(2));

    expect(gateway.actions[0]).toMatchObject({ kind: "create-preset" });
    expect(gateway.actions[1]).toMatchObject({ kind: "create-preset" });
    expect(gateway.actions[1]).toHaveProperty(
      "idempotencyKey",
      (gateway.actions[0] as Extract<RealQaProductAction, { kind: "create-preset" }>).idempotencyKey,
    );
  });

  it("offers GitHub connection when a first preset has no destination", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const gateway = new FixtureGateway({
      ...value,
      presets: [],
      destinations: [],
      definitions: [],
      drafts: [],
    });
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Connect GitHub" }));
    expect(gateway.actions.at(-1)).toEqual({ kind: "connect-destination" });
  });

  it("preserves distinct disconnect, draft deletion, and server feature deletion confirmations", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Disconnect GitHub" }));
    await user.click(within(screen.getByRole("dialog", { name: "Disconnect RealQA from GitHub" })).getByRole("button", { name: "Disconnect" }));
    expect(gateway.actions.at(-1)).toMatchObject({ kind: "disconnect-destination" });
    expect(screen.getByText(/presets were retained/u)).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Delete draft" }));
    const draftDialog = screen.getByRole("dialog", { name: "Delete local RealQA draft" });
    await user.keyboard("{Escape}");
    expect(draftDialog).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Delete server feature data" }));
    await user.click(within(screen.getByRole("dialog", { name: "Delete server RealQA data" })).getByRole("button", { name: "Confirm server deletion" }));
    expect(gateway.actions.at(-1)).toEqual({ kind: "delete-feature-data" });
  });

  it("supports revision-safe reconciliation, image deletion, and exact-scope rebind", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);
    await user.click(screen.getByRole("button", { name: "Retry reconciliation" }));
    expect(gateway.actions.at(-1)).toMatchObject({ kind: "retry-submission" });
    await user.click(screen.getByRole("button", { name: "Delete Submitted capture" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "delete-image",
      expectedSubmissionRevision: 8,
      expectedImageRevision: 9,
    });
    await user.click(screen.getByRole("button", { name: "Delete all images" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "delete-submission-assets",
      expectedSubmissionRevision: 8,
    });
    expect(screen.getByLabelText("Replacement payer")).toHaveValue(
      "01900000-0000-7000-8000-000000000008/01900000-0000-7000-8000-000000000009",
    );
    await user.click(screen.getByRole("button", { name: "Rebind payer" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "rebind-authorization",
      expectedRevision: 7,
      replacementBilling: {
        organizationId: "01900000-0000-7000-8000-000000000008",
        teamId: "01900000-0000-7000-8000-000000000009",
      },
      idempotencyKey: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-7/u),
    });
    expect(screen.queryByRole("button", { name: "Revoke grant" })).not.toBeInTheDocument();
  });

  it("reuses the exact-scope rebind identity after an ambiguous failure", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    gateway.nextFailure = new RealQaProductError(RealQaFailureCode.ServiceUnavailable);
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Rebind payer" }));
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Rebind payer" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(2));

    const first = gateway.actions[0] as Extract<
      RealQaProductAction,
      { kind: "rebind-authorization" }
    >;
    const retry = gateway.actions[1] as Extract<
      RealQaProductAction,
      { kind: "rebind-authorization" }
    >;
    expect(retry.idempotencyKey).toBe(first.idempotencyKey);
    expect(retry.replacementBilling).toEqual(first.replacementBilling);
  });

  it("has no automated WCAG violations", async () => {
    const gateway = new FixtureGateway(snapshot());
    const { container } = await renderWorkspace(gateway);
    const results = await axe.run(container, { rules: { "color-contrast": { enabled: false } } });
    expect(results.violations).toEqual([]);
  });
});
