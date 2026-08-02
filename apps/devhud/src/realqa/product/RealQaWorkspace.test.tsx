import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, describe, expect, it } from "vitest";

import { CaptureMode, PointerInclusion } from "../capture";
import { MAX_FINAL_BODY_UTF8_BYTES } from "../onlineSubmission";
import { ShortcutKey, ShortcutModifier } from "../../persistence/contracts";
import {
  publishPersistenceReset,
  publishSessionInvalidation,
  publishSessionReauthentication,
} from "../../runtime/theme";
import { RealQaWorkspace, serializeFinalIssueBody } from "./RealQaWorkspace";
import {
  RealQaAccessMode,
  RealQaDesktopFamily,
  RealQaFailureCode,
  RealQaOwnerScopeKind,
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
  const draft = {
    draftId,
    submissionIdempotencyKey: draftId,
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
    milestoneNumber: 4,
    projectNodeIds: ["PVT_kwDORelease"],
    images: [{
      imageId,
      revision: 1,
      name: "Capture 1",
      encodedBytes: 8192,
      selected: true,
      uploadState: "local" as const,
      uploadProgress: 0,
      uploadDeadline: null,
    }],
  };
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
      destinationId: "destination-1",
      definitionId: "bug-form",
      name: "Bug report",
      kind: "issue-form",
      issueType: "Bug",
      fields: [
        { fieldId: "summary", kind: "input", label: "Summary", required: true, defaultValue: "" },
        { fieldId: "logs", kind: "textarea", label: "Logs", required: false, defaultValue: "", renderLanguage: "shell" },
        { fieldId: "severity", kind: "dropdown", label: "Severity", required: true, multiple: false, options: ["Low", "High"], defaultValue: "High" },
        { fieldId: "browsers", kind: "dropdown", label: "Browsers", required: false, multiple: true, options: ["Chrome", "Firefox"], defaultValue: "" },
        {
          fieldId: "checks",
          kind: "checkboxes",
          label: "Checks",
          required: false,
          options: [
            { value: "reproduced", label: "Reproduced", required: true },
            { value: "sanitized", label: "Sanitized", required: false },
          ],
        },
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
      milestoneNumber: 4,
      projectNodeIds: ["PVT_kwDORelease"],
      processUrlRules: [{
        ruleId: "01900000-0000-7000-8000-000000000011",
        exactProcessName: "DevHud",
        safeWindowTitlePattern: "Issue ([0-9]+)",
        urlTemplate: "https://github.com/delinoio/oss/issues/$1",
        enabled: true,
      }],
      billing: {
        organizationId: "01900000-0000-7000-8000-000000000008",
        teamId: "01900000-0000-7000-8000-000000000009",
      },
      backgroundGrant: "active",
      shortcut: {
        modifiers: [ShortcutModifier.Control, ShortcutModifier.Shift],
        key: ShortcutKey.Digit7,
      },
    }],
    drafts: [draft],
    submissions: [{
      submissionId: "01900000-0000-7000-8000-000000000004",
      revision: 8,
      state: "reconciling",
      issueUrl: null,
      graceExpiresAt: null,
      authorizationId: "01900000-0000-7000-8000-000000000005",
      authorizationRevision: 7,
      rebindAvailable: false,
      replay: {
        idempotencyKey: draftId,
        expectedSubmissionRevision: 8,
        originalDraft: draft,
      },
      images: [{
        imageId: "01900000-0000-7000-8000-000000000006",
        revision: 9,
        name: "Submitted capture",
        encodedBytes: 8192,
        selected: true,
        uploadState: "public",
        uploadProgress: 100,
        uploadDeadline: null,
      }],
    }],
    replacementBillingScopes: [{
      organizationId: "01900000-0000-7000-8000-000000000008",
      teamId: "01900000-0000-7000-8000-000000000009",
      label: "Acme / Release",
    }],
    featureDeletionScopes: [
      {
        kind: RealQaOwnerScopeKind.Personal,
        personalAccountId: "01900000-0000-7000-8000-000000000012",
        label: "Personal account",
      },
      {
        kind: RealQaOwnerScopeKind.Organization,
        organizationId: "01900000-0000-7000-8000-000000000008",
        label: "Acme",
      },
    ],
  };
}

function storageRecoverySnapshot(): RealQaProductSnapshot {
  const value = snapshot();
  return {
    ...value,
    submissions: value.submissions.map((submission) => ({
      ...submission,
      state: "storage-billing-grace",
      graceExpiresAt: "2026-09-01T00:00:00Z",
      rebindAvailable: true,
    })),
  };
}

class FixtureGateway implements RealQaProductGateway {
  readonly actions: RealQaProductAction[] = [];
  loadCalls = 0;
  nextFailure: RealQaProductError | null = null;

  constructor(public value: RealQaProductSnapshot) {}

  async load() {
    this.loadCalls += 1;
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
      const createdPresetId = this.value.presets.length === 0
        ? presetId
        : "01900000-0000-7000-8000-000000000010";
      this.value = {
        ...this.value,
        presets: [...this.value.presets, {
          ...action.preset,
          presetId: createdPresetId,
          revision: 1,
        }],
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
                  uploadDeadline: null,
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
          rebindAvailable: false,
          replay: null,
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
    "completes the retained-capture-to-issue flow on %s",
    async (platform) => {
      const user = userEvent.setup();
      const gateway = new FixtureGateway(snapshot(platform));
      await renderWorkspace(gateway);

      expect(screen.getByLabelText("Operating system")).toHaveValue(platform);
      expect(screen.queryByRole("button", { name: "Capture screenshot" })).toBeNull();
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
        idempotencyKey: draftId,
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
    expect(screen.queryByRole("button", { name: "Capture screenshot" })).toBeNull();
    await user.clear(screen.getByLabelText("Issue title"));
    await user.type(screen.getByLabelText("Issue title"), "Offline edit");
    await user.click(screen.getByRole("button", { name: "Save draft" }));
    expect(gateway.actions.at(-1)).toMatchObject({ kind: "save-draft" });
  });

  it("requires every required issue-form answer before opening submission review", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);
    const review = screen.getByRole("button", { name: "Review and submit" });

    expect(review).toBeDisabled();
    expect(screen.getByText(/Complete every required issue-form field/u)).toBeVisible();
    await user.type(screen.getByLabelText("Summary"), "Required answers");
    expect(review).toBeDisabled();
    await user.click(screen.getByLabelText("Sanitized"));
    expect(review).toBeDisabled();
    await user.click(screen.getByLabelText("Reproduced"));

    expect(review).toBeEnabled();
  });

  it("preserves comma-bearing labels while editing preset and draft label lists", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const preset = value.presets[0];
    const draft = value.drafts[0];
    if (preset === undefined || draft === undefined) {
      throw new Error("Fixture preset or draft is missing.");
    }
    const gateway = new FixtureGateway({
      ...value,
      presets: [{ ...preset, labels: ["area,ui"] }],
      drafts: [{ ...draft, labels: ["area,ui"] }],
    });
    await renderWorkspace(gateway);

    const labelInputs = screen.getAllByLabelText("Labels");
    const presetLabels = labelInputs[0];
    const draftLabels = labelInputs[1];
    if (presetLabels === undefined || draftLabels === undefined) {
      throw new Error("Fixture label inputs are missing.");
    }
    expect(presetLabels).toHaveValue('"area,ui"');
    expect(draftLabels).toHaveValue('"area,ui"');

    await user.type(presetLabels, ", regression");
    await user.click(screen.getByRole("button", { name: "Save preset" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "save-preset",
      preset: { labels: ["area,ui", "regression"] },
    });

    await user.type(draftLabels, ", screenshot");
    await user.click(screen.getByRole("button", { name: "Save draft" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "save-draft",
      draft: { labels: ["area,ui", "screenshot"] },
    });
  });

  it("filters retained issue-form values against the current definition", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const draft = value.drafts[0];
    const definition = value.definitions[0];
    if (draft === undefined) throw new Error("Fixture draft is missing.");
    if (definition === undefined) throw new Error("Fixture definition is missing.");
    const gateway = new FixtureGateway({
      ...value,
      definitions: [{
        ...definition,
        fields: definition.fields.map((field) =>
          field.kind === "dropdown" && field.fieldId === "severity"
            ? { ...field, defaultValue: "" }
            : field
        ),
      }],
      drafts: [{
        ...draft,
        issueAnswers: {
          summary: ["Required answers"],
          severity: ["Removed severity"],
          browsers: ["Safari", "Chrome"],
          checks: ["removed-check", "reproduced"],
          retiredField: ["retired value"],
        },
      }],
    });
    await renderWorkspace(gateway);

    const review = screen.getByRole("button", { name: "Review and submit" });
    expect(review).toBeDisabled();
    expect(screen.getByLabelText("Browsers")).toHaveValue(["Chrome"]);
    await user.selectOptions(screen.getByLabelText("Severity"), "High");
    expect(review).toBeEnabled();
    await user.click(review);
    await user.click(within(screen.getByRole("dialog", { name: "Confirm public screenshots" })).getByRole("button", { name: "Confirm and submit" }));

    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "submit",
      draft: {
        issueAnswers: {
          summary: ["Required answers"],
          severity: ["High"],
          browsers: ["Chrome"],
          checks: ["reproduced"],
        },
      },
    }));
  });

  it("blocks blank and overlong UTF-8 issue titles before submission", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const definition = value.definitions[0];
    if (definition === undefined) throw new Error("Fixture issue definition is missing.");
    const gateway = new FixtureGateway({
      ...value,
      definitions: [{ ...definition, fields: [] }],
    });
    await renderWorkspace(gateway);
    const title = screen.getByLabelText("Issue title");
    const review = screen.getByRole("button", { name: "Review and submit" });

    await user.clear(title);
    await user.type(title, "   ");
    expect(review).toBeDisabled();
    expect(title).toHaveAttribute("aria-invalid", "true");

    fireEvent.change(title, { target: { value: "é".repeat(129) } });
    expect(review).toBeDisabled();
    expect(screen.getByText(/between 1 and 256 UTF-8 bytes/u)).toBeVisible();

    fireEvent.change(title, { target: { value: "é".repeat(128) } });
    expect(review).toBeEnabled();

    fireEvent.change(title, { target: { value: `${" ".repeat(300)}Bug${" ".repeat(300)}` } });
    expect(review).toBeEnabled();
    await user.click(review);
    await user.click(within(screen.getByRole("dialog", { name: "Confirm public screenshots" })).getByRole("button", { name: "Confirm and submit" }));
    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "submit",
      draft: { title: "Bug" },
    }));
  });

  it("measures the sanitized URL that submission sends", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const definition = value.definitions[0];
    const draft = value.drafts[0];
    if (definition === undefined || draft === undefined) {
      throw new Error("Fixture issue definition or draft is missing.");
    }
    const definitionWithoutFields = { ...definition, fields: [] };
    const canonicalUrl = "https://example.com/report";
    const oneByteBodyDraft = {
      ...draft,
      body: "x",
      url: canonicalUrl,
      issueAnswers: {},
    };
    const oneByteBodySize = new TextEncoder().encode(
      serializeFinalIssueBody(oneByteBodyDraft, definitionWithoutFields),
    ).byteLength;
    const body = "x".repeat(MAX_FINAL_BODY_UTF8_BYTES - oneByteBodySize + 1);
    const gateway = new FixtureGateway({
      ...value,
      definitions: [definitionWithoutFields],
      drafts: [{
        ...draft,
        body,
        url: `${canonicalUrl}?secret=${"x".repeat(1_000)}#details`,
        issueAnswers: {},
      }],
    });
    await renderWorkspace(gateway);

    expect(screen.getByText("60,000 / 60,000 body bytes")).toBeVisible();
    const review = screen.getByRole("button", { name: "Review and submit" });
    expect(review).toBeEnabled();
    await user.click(review);
    await user.click(within(screen.getByRole("dialog", { name: "Confirm public screenshots" })).getByRole("button", { name: "Confirm and submit" }));
    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "submit",
      draft: { url: canonicalUrl },
    }));
  });

  it("does not expose shortcut editing before native registration is connected", async () => {
    const value = snapshot();
    const template = value.presets[0];
    if (template === undefined) throw new Error("Fixture preset is missing.");
    const gateway = new FixtureGateway({
      ...value,
      presets: [
        { ...template, presetId: "preset-without-shortcut", name: "Unbound preset", shortcut: null },
        ...Array.from({ length: 20 }, (_, index) => ({
          ...template,
          presetId: `shortcut-${index}`,
          name: `Shortcut ${index + 1}`,
          shortcut: {
            modifiers: [ShortcutModifier.Control, ShortcutModifier.Shift],
            key: Object.values(ShortcutKey)[index] ?? ShortcutKey.K,
          },
        })),
      ],
    });
    await renderWorkspace(gateway);

    expect(screen.queryByRole("button", { name: "Record shortcut" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Clear shortcut" })).toBeNull();
    expect(screen.getByLabelText("Name")).toHaveValue("Unbound preset");
  });

  it("preserves existing shortcut data while shortcut editing is hidden", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);

    expect(screen.queryByRole("button", { name: "Record shortcut" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Save preset" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "save-preset",
      preset: {
        shortcut: {
          modifiers: [ShortcutModifier.Control, ShortcutModifier.Shift],
          key: ShortcutKey.Digit7,
        },
      },
    });
  });

  it("preserves typed GitHub metadata and ordered URL rules through edits", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);

    const processName = screen.getByLabelText("Exact process name");
    await user.clear(processName);
    await user.type(processName, "DevHud Preview");
    await user.click(screen.getByRole("button", { name: "Save preset" }));

    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "save-preset",
      preset: {
        milestoneNumber: 4,
        projectNodeIds: ["PVT_kwDORelease"],
        processUrlRules: [{
          ruleId: "01900000-0000-7000-8000-000000000011",
          exactProcessName: "DevHud Preview",
          urlTemplate: "https://github.com/delinoio/oss/issues/$1",
        }],
      },
    });
  });

  it("offers only definitions belonging to the selected destination", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const definition = value.definitions[0];
    if (definition === undefined) throw new Error("Fixture issue definition is missing.");
    const gateway = new FixtureGateway({
      ...value,
      destinations: [
        ...value.destinations,
        {
          destinationId: "destination-2",
          repository: "delinoio/another-repository",
          connected: true,
          revision: 1,
        },
      ],
      definitions: [
        definition,
        {
          ...definition,
          destinationId: "destination-2",
          definitionId: "feature-form",
          name: "Feature request",
        },
      ],
    });
    await renderWorkspace(gateway);

    await user.selectOptions(screen.getByLabelText("Destination"), "destination-2");
    const definitionSelect = screen.getByLabelText("Template or form");
    expect(definitionSelect).toHaveValue("feature-form");
    expect(within(definitionSelect).queryByRole("option", { name: /Bug report/u })).toBeNull();
    expect(within(definitionSelect).getByRole("option", { name: /Feature request/u })).toBeVisible();
  });

  it("reviews editable URLs and submits only the sanitized value", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);
    const url = screen.getByLabelText("Sanitized URL");
    await user.type(screen.getByLabelText("Summary"), "Sanitized URL");
    await user.click(screen.getByLabelText("Reproduced"));

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

  it("submits OS captures without a page URL", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const draft = value.drafts[0];
    if (draft === undefined) throw new Error("Fixture draft is missing.");
    const gateway = new FixtureGateway({
      ...value,
      drafts: [{ ...draft, url: "", urlWarning: false }],
    });
    await renderWorkspace(gateway);

    const url = screen.getByLabelText("Sanitized URL");
    expect(url).toHaveAttribute("aria-invalid", "false");
    await user.type(screen.getByLabelText("Summary"), "Window capture");
    await user.click(screen.getByLabelText("Reproduced"));
    await user.click(screen.getByRole("button", { name: "Review and submit" }));
    await user.click(within(screen.getByRole("dialog", { name: "Confirm public screenshots" })).getByRole("button", { name: "Confirm and submit" }));

    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "submit",
      draft: { url: "", urlWarning: false },
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

  it("refreshes a stale destination after a typed GitHub disconnect failure", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);
    gateway.value = {
      ...gateway.value,
      destinations: gateway.value.destinations.map((destination) => ({
        ...destination,
        connected: false,
        revision: destination.revision + 1,
      })),
    };
    gateway.nextFailure = new RealQaProductError(
      RealQaFailureCode.GitHubDisconnected,
    );

    await user.click(screen.getByRole("button", { name: "Save preset" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "GitHub connection was disconnected",
    );
    expect(gateway.loadCalls).toBe(2);
    expect(screen.getAllByRole("button", { name: "Reconnect GitHub" })).not.toHaveLength(0);
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

  it("saves payer changes as an exact billing scope", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const gateway = new FixtureGateway({
      ...value,
      replacementBillingScopes: [
        ...value.replacementBillingScopes,
        {
          organizationId: "01900000-0000-7000-8000-000000000010",
          teamId: "01900000-0000-7000-8000-000000000011",
          label: "Acme / QA",
        },
      ],
    });
    await renderWorkspace(gateway);

    await user.selectOptions(
      screen.getByLabelText("Payer / team"),
      "01900000-0000-7000-8000-000000000010/01900000-0000-7000-8000-000000000011",
    );
    await user.click(screen.getByRole("button", { name: "Save preset" }));

    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "save-preset",
      preset: {
        billing: {
          organizationId: "01900000-0000-7000-8000-000000000010",
          teamId: "01900000-0000-7000-8000-000000000011",
        },
      },
    });
  });

  it("blocks preset saves until an unavailable billing scope is replaced", async () => {
    const value = snapshot();
    const gateway = new FixtureGateway({ ...value, replacementBillingScopes: [] });
    await renderWorkspace(gateway);

    expect(screen.getByRole("button", { name: "Save preset" })).toBeDisabled();
    expect(screen.getByText(/Choose an available payer \/ team/u)).toBeVisible();
    expect(gateway.actions).toHaveLength(0);
  });

  it("blocks invalid milestone numbers in preset and draft actions", async () => {
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);
    const milestones = screen.getAllByLabelText("Milestone number");
    const presetMilestone = milestones[0];
    const draftMilestone = milestones[1];
    if (presetMilestone === undefined || draftMilestone === undefined) {
      throw new Error("Fixture milestone inputs are missing.");
    }

    fireEvent.change(presetMilestone, { target: { value: "1.5" } });
    expect(screen.getByRole("button", { name: "Save preset" })).toBeDisabled();
    fireEvent.change(presetMilestone, { target: { value: "4" } });

    fireEvent.change(draftMilestone, { target: { value: "0" } });
    expect(screen.getByRole("button", { name: "Save draft" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
    expect(screen.getAllByText(/positive whole number/u)).not.toHaveLength(0);
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

  it("keeps native capture controls hidden across draft selections", async () => {
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

    expect(screen.queryByRole("button", { name: "Capture screenshot" })).toBeNull();

    await user.selectOptions(screen.getByLabelText("Draft"), "draft-2");
    await waitFor(() => {
      expect(screen.getByLabelText("Draft")).toHaveValue("draft-2");
      expect(screen.queryByRole("button", { name: "Capture screenshot" })).toBeNull();
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

    await user.type(screen.getByLabelText("Summary"), "First draft");
    await user.click(screen.getByLabelText("Reproduced"));
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
    const value = snapshot();
    const definition = value.definitions[0];
    if (definition === undefined) throw new Error("Fixture issue definition is missing.");
    const gateway = new FixtureGateway({
      ...value,
      definitions: [{
        ...definition,
        fields: definition.fields.map((field) =>
          field.kind === "dropdown" && field.multiple
            ? { ...field, defaultValue: "Chrome" }
            : field,
        ),
      }],
    });
    await renderWorkspace(gateway);

    expect(screen.getByLabelText("Severity")).toHaveValue("High");
    expect(screen.getByLabelText("Browsers")).toHaveValue(["Chrome"]);
    await user.type(screen.getByLabelText("Summary"), "Default fields");
    await user.click(screen.getByLabelText("Reproduced"));
    await user.click(screen.getByRole("button", { name: "Save draft" }));
    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "save-draft",
      draft: { issueAnswers: { severity: ["High"], browsers: ["Chrome"] } },
    }));
    await user.click(screen.getByRole("button", { name: "Review and submit" }));
    await user.click(within(screen.getByRole("dialog", { name: "Confirm public screenshots" })).getByRole("button", { name: "Confirm and submit" }));
    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "submit",
      draft: { issueAnswers: { severity: ["High"], browsers: ["Chrome"] } },
    }));
  });

  it("blocks review and final submission while retained storage is in grace", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    const view = await renderWorkspace(gateway);
    await user.type(screen.getByLabelText("Summary"), "Storage grace");
    await user.click(screen.getByLabelText("Reproduced"));
    await user.click(screen.getByRole("button", { name: "Review and submit" }));

    const blocked = snapshot();
    const retained = blocked.submissions[0];
    if (retained === undefined) throw new Error("Fixture submission is missing.");
    const blockedGateway = new FixtureGateway({
      ...blocked,
      submissions: [{
        ...retained,
        state: "storage-billing-grace",
        graceExpiresAt: "2026-09-01T00:00:00Z",
      }],
    });
    view.rerender(<RealQaWorkspace gateway={blockedGateway} />);

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
    });
    await user.click(within(screen.getByRole("dialog", { name: "Confirm public screenshots" })).getByRole("button", { name: "Confirm and submit" }));
    expect(blockedGateway.actions).toHaveLength(0);
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
      preset: {
        billing: {
          organizationId: "01900000-0000-7000-8000-000000000008",
          teamId: "01900000-0000-7000-8000-000000000009",
        },
      },
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

  it("creates another preset from the non-empty preset manager", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "New preset" }));

    await waitFor(() => expect(gateway.actions.at(-1)).toMatchObject({
      kind: "create-preset",
      preset: { name: "New RealQA preset" },
    }));
    expect(screen.getByLabelText("Preset")).toHaveValue(
      "01900000-0000-7000-8000-000000000010",
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

  it("offers GitHub reconnection before creating a first preset", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const destination = value.destinations[0];
    if (destination === undefined) throw new Error("Fixture destination is missing.");
    const gateway = new FixtureGateway({
      ...value,
      presets: [],
      destinations: [{ ...destination, connected: false }],
      drafts: [],
    });
    await renderWorkspace(gateway);

    expect(screen.queryByRole("button", { name: "Create first preset" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Reconnect GitHub" }));
    expect(gateway.actions.at(-1)).toEqual({
      kind: "reconnect-destination",
      destinationId: destination.destinationId,
    });
  });

  it("blocks submission when a retained draft's preset is gone", async () => {
    const gateway = new FixtureGateway({ ...snapshot(), presets: [] });
    await renderWorkspace(gateway);

    expect(screen.getByText(/draft's preset is no longer available/u)).toBeVisible();
    expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
  });

  it("blocks submission and offers reconnection for a retained disconnected destination", async () => {
    const user = userEvent.setup();
    const value = snapshot();
    const destination = value.destinations[0];
    if (destination === undefined) throw new Error("Fixture destination is missing.");
    const gateway = new FixtureGateway({
      ...value,
      destinations: [{ ...destination, connected: false }],
    });
    await renderWorkspace(gateway);

    expect(screen.getByText(/destination is disconnected/u)).toBeVisible();
    expect(screen.getByRole("button", { name: "Save preset" })).toBeDisabled();
    expect(screen.getByText(/Reconnect the selected GitHub destination before saving/u)).toBeVisible();
    expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
    const reconnect = screen.getAllByRole("button", { name: "Reconnect GitHub" }).at(-1);
    if (reconnect === undefined) throw new Error("Reconnect action is missing.");
    await user.click(reconnect);
    expect(gateway.actions.at(-1)).toEqual({
      kind: "reconnect-destination",
      destinationId: destination.destinationId,
    });
  });

  it("blocks submission until the preset background storage grant is active", async () => {
    const value = snapshot();
    const preset = value.presets[0];
    if (preset === undefined) throw new Error("Fixture preset is missing.");
    const gateway = new FixtureGateway({
      ...value,
      presets: [{ ...preset, backgroundGrant: "rebind-required" }],
    });
    await renderWorkspace(gateway);

    expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
    expect(screen.getByText(/Rebind its RealQA storage authorization in DeliDev/u)).toBeVisible();
  });

  it("blocks selected images that exceed the session byte limit", async () => {
    const value = snapshot();
    const draft = value.drafts[0];
    if (draft === undefined) throw new Error("Fixture draft is missing.");
    const image = draft.images[0];
    if (image === undefined) throw new Error("Fixture image is missing.");
    const gateway = new FixtureGateway({
      ...value,
      drafts: [{
        ...draft,
        images: Array.from({ length: 11 }, (_, index) => ({
          ...image,
          imageId: `image-${index}`,
          encodedBytes: 25 * 1024 * 1024,
        })),
      }],
    });
    await renderWorkspace(gateway);

    expect(screen.getByText(/exceed the 250 MiB session limit/u)).toBeVisible();
    expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
  });

  it("blocks submission and retry after a staged upload deadline expires", async () => {
    const value = snapshot();
    const draft = value.drafts[0];
    const submission = value.submissions[0];
    if (draft === undefined || submission === undefined) {
      throw new Error("Fixture draft or submission is missing.");
    }
    const expired = "2020-01-01T00:00:00Z";
    const gateway = new FixtureGateway({
      ...value,
      drafts: [{
        ...draft,
        images: draft.images.map((image) => ({
          ...image,
          uploadState: "verified" as const,
          uploadDeadline: expired,
        })),
      }],
      submissions: [{
        ...submission,
        images: submission.images.map((image) => ({
          ...image,
          uploadState: "verified" as const,
          uploadDeadline: expired,
        })),
      }],
    });
    await renderWorkspace(gateway);

    expect(screen.getByText(/staged upload deadline expired/u)).toBeVisible();
    expect(screen.getByRole("button", { name: "Review and submit" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Retry reconciliation" })).toBeDisabled();
    expect(screen.getByText(/upload window expired/u)).toBeVisible();
  });

  it.each([
    ["session invalidation", () => publishSessionInvalidation()],
    ["persistence reset", () => publishPersistenceReset({ status: "complete" })],
  ])("locks rendered draft data after %s", async (_label, invalidate) => {
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);

    expect(screen.getAllByDisplayValue("Captured regression")).not.toHaveLength(0);
    act(invalidate);

    expect(await screen.findByRole("heading", { name: "Opening RealQA" })).toBeVisible();
    expect(screen.getByText(/Sign in with the previously bound DeliDev account/u)).toBeVisible();
    expect(screen.queryAllByDisplayValue("Captured regression")).toHaveLength(0);
  });

  it("reloads a locked workspace after successful RealQA reauthentication", async () => {
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);
    act(() => publishSessionInvalidation());
    await screen.findByRole("heading", { name: "Opening RealQA" });

    const refreshed = snapshot();
    const draft = refreshed.drafts[0];
    if (draft === undefined) throw new Error("Fixture draft is missing.");
    gateway.value = {
      ...refreshed,
      drafts: [{ ...draft, title: "Reauthenticated draft" }],
    };
    act(() => publishSessionReauthentication());

    expect(await screen.findByLabelText("Issue title")).toHaveValue(
      "Reauthenticated draft",
    );
    expect(gateway.loadCalls).toBe(2);
  });

  it("preserves distinct disconnect, draft deletion, and server feature deletion confirmations", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Disconnect GitHub" }));
    await user.click(within(screen.getByRole("dialog", { name: "Disconnect RealQA from GitHub" })).getByRole("button", { name: "Disconnect" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "disconnect-destination",
      idempotencyKey: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-7/u),
    });
    expect(screen.getByText(/presets were retained/u)).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Delete draft" }));
    const draftDialog = screen.getByRole("dialog", { name: "Delete local RealQA draft" });
    await user.keyboard("{Escape}");
    expect(draftDialog).not.toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Owner scope"), "organization/01900000-0000-7000-8000-000000000008");
    await user.click(screen.getByRole("button", { name: "Delete server feature data" }));
    await user.click(within(screen.getByRole("dialog", { name: "Delete server RealQA data" })).getByRole("button", { name: "Confirm server deletion" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "delete-feature-data",
      owner: {
        kind: RealQaOwnerScopeKind.Organization,
        organizationId: "01900000-0000-7000-8000-000000000008",
      },
      idempotencyKey: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-7/u),
    });
    expect(await screen.findByText("No presets are available.")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Save preset" })).not.toBeInTheDocument();
  });

  it("reuses disconnect and feature-deletion identities after ambiguous failures", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);

    gateway.nextFailure = new RealQaProductError(RealQaFailureCode.ServiceUnavailable);
    await user.click(screen.getByRole("button", { name: "Disconnect GitHub" }));
    await user.click(within(screen.getByRole("dialog", { name: "Disconnect RealQA from GitHub" })).getByRole("button", { name: "Disconnect" }));
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Disconnect GitHub" }));
    await user.click(within(screen.getByRole("dialog", { name: "Disconnect RealQA from GitHub" })).getByRole("button", { name: "Disconnect" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(2));
    const firstDisconnect = gateway.actions[0] as Extract<
      RealQaProductAction,
      { kind: "disconnect-destination" }
    >;
    const retriedDisconnect = gateway.actions[1] as Extract<
      RealQaProductAction,
      { kind: "disconnect-destination" }
    >;
    expect(retriedDisconnect.idempotencyKey).toBe(firstDisconnect.idempotencyKey);

    gateway.nextFailure = new RealQaProductError(RealQaFailureCode.ServiceUnavailable);
    await user.click(screen.getByRole("button", { name: "Delete server feature data" }));
    await user.click(within(screen.getByRole("dialog", { name: "Delete server RealQA data" })).getByRole("button", { name: "Confirm server deletion" }));
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Delete server feature data" }));
    await user.click(within(screen.getByRole("dialog", { name: "Delete server RealQA data" })).getByRole("button", { name: "Confirm server deletion" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(4));
    const firstDeletion = gateway.actions[2] as Extract<
      RealQaProductAction,
      { kind: "delete-feature-data" }
    >;
    const retriedDeletion = gateway.actions[3] as Extract<
      RealQaProductAction,
      { kind: "delete-feature-data" }
    >;
    expect(retriedDeletion.idempotencyKey).toBe(firstDeletion.idempotencyKey);
  });

  it("supports revision-safe reconciliation and deletion without offering healthy rebind", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    await renderWorkspace(gateway);
    await user.click(screen.getByRole("button", { name: "Retry reconciliation" }));
    expect(gateway.actions).toHaveLength(0);
    const retryDialog = screen.getByRole("dialog", {
      name: "Confirm public screenshots for retry",
    });
    expect(retryDialog).toHaveTextContent("Anyone with the GitHub issue URL");
    await user.click(within(retryDialog).getByRole("button", { name: "Confirm and retry" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "retry-submission",
      expectedSubmissionRevision: 8,
      idempotencyKey: draftId,
      originalDraft: { draftId },
      publicImageConfirmation: true,
    });
    await user.click(screen.getByRole("button", { name: "Delete Submitted capture" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "delete-image",
      expectedSubmissionRevision: 8,
      expectedImageRevision: 9,
      idempotencyKey: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-7/u),
    });
    await user.click(screen.getByRole("button", { name: "Delete all images" }));
    expect(gateway.actions.at(-1)).toMatchObject({
      kind: "delete-submission-assets",
      expectedSubmissionRevision: 8,
      idempotencyKey: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-7/u),
    });
    expect(screen.queryByRole("button", { name: "Rebind payer" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Revoke grant" })).not.toBeInTheDocument();
  });

  it("offers exact-scope rebind only during storage billing recovery", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(storageRecoverySnapshot());
    await renderWorkspace(gateway);

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
  });

  it("reuses image and submission-asset deletion identities after ambiguous failures", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(snapshot());
    gateway.nextFailure = new RealQaProductError(RealQaFailureCode.ServiceUnavailable);
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Delete Submitted capture" }));
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Delete Submitted capture" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(2));
    const firstImage = gateway.actions[0] as Extract<
      RealQaProductAction,
      { kind: "delete-image" }
    >;
    const retriedImage = gateway.actions[1] as Extract<
      RealQaProductAction,
      { kind: "delete-image" }
    >;
    expect(retriedImage.idempotencyKey).toBe(firstImage.idempotencyKey);

    gateway.nextFailure = new RealQaProductError(RealQaFailureCode.ServiceUnavailable);
    await user.click(screen.getByRole("button", { name: "Delete all images" }));
    await screen.findByRole("alert");
    await user.click(screen.getByRole("button", { name: "Delete all images" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(4));
    const firstAll = gateway.actions[2] as Extract<
      RealQaProductAction,
      { kind: "delete-submission-assets" }
    >;
    const retriedAll = gateway.actions[3] as Extract<
      RealQaProductAction,
      { kind: "delete-submission-assets" }
    >;
    expect(retriedAll.idempotencyKey).toBe(firstAll.idempotencyKey);
  });

  it("reuses the exact-scope rebind identity after an ambiguous failure", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(storageRecoverySnapshot());
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

  it("starts a new rebind identity when the expected authorization changes", async () => {
    const user = userEvent.setup();
    const gateway = new FixtureGateway(storageRecoverySnapshot());
    gateway.nextFailure = new RealQaProductError(RealQaFailureCode.ServiceUnavailable);
    await renderWorkspace(gateway);

    await user.click(screen.getByRole("button", { name: "Rebind payer" }));
    await screen.findByRole("alert");
    const first = gateway.actions[0] as Extract<
      RealQaProductAction,
      { kind: "rebind-authorization" }
    >;

    gateway.value = {
      ...gateway.value,
      submissions: gateway.value.submissions.map((submission) => ({
        ...submission,
        authorizationId: "01900000-0000-7000-8000-000000000011",
        authorizationRevision: 8,
      })),
    };
    await user.click(screen.getByRole("button", { name: "Delete Submitted capture" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(2));
    await user.click(screen.getByRole("button", { name: "Rebind payer" }));
    await waitFor(() => expect(gateway.actions).toHaveLength(3));

    const retry = gateway.actions[2] as Extract<
      RealQaProductAction,
      { kind: "rebind-authorization" }
    >;
    expect(retry.idempotencyKey).not.toBe(first.idempotencyKey);
    expect(retry.expectedAuthorizationId).toBe(
      "01900000-0000-7000-8000-000000000011",
    );
    expect(retry.expectedRevision).toBe(8);
  });

  it("has no automated WCAG violations", async () => {
    const gateway = new FixtureGateway(snapshot());
    const { container } = await renderWorkspace(gateway);
    const results = await axe.run(container, { rules: { "color-contrast": { enabled: false } } });
    expect(results.violations).toEqual([]);
  });
});
