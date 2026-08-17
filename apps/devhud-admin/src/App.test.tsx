import { Code, ConnectError } from "@connectrpc/connect";
import {
  AdministrativeBlockState,
  AuditAction,
  AuditOutcome,
  PaginationFailureReason,
  PaginationFailureSchema,
  StaticCapability,
  UploadState,
} from "@delinoio/devhud-api-client";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";

const runtime = vi.hoisted(() => ({
  getBootstrap: vi.fn(),
  createAdminClient: vi.fn(),
  fromBootstrap: vi.fn(),
  auth: {
    completeCallback: vi.fn(),
    isAuthenticated: vi.fn(),
    begin: vi.fn(),
    signOut: vi.fn(),
  },
  client: {
    listUsers: vi.fn(),
    setUserBlocked: vi.fn(),
    getUserUsage: vi.fn(),
    listUploads: vi.fn(),
    quarantineUpload: vi.fn(),
    deleteUpload: vi.fn(),
    listAuditEvents: vi.fn(),
  },
}));

vi.mock("./api", () => ({
  getBootstrap: runtime.getBootstrap,
  createAdminClient: runtime.createAdminClient,
}));

vi.mock("./auth", () => ({
  AdminAuth: { fromBootstrap: runtime.fromBootstrap },
}));

const user = {
  userId: { value: "018f7c1e-7b4a-7abc-8def-0123456789ad" },
  logtoSubject: "target-subject",
  displayName: "Target User",
  email: "target@example.com",
  administrativeBlockState: AdministrativeBlockState.UNBLOCKED,
};

const upload = {
  uploadId: { value: "018f7c1e-7b4a-7abc-8def-0123456789ae" },
  uploadGroupId: { value: "018f7c1e-7b4a-7abc-8def-0123456789af" },
  state: UploadState.FINALIZED,
  sizeBytes: 1024n,
};

beforeEach(() => {
  localStorage.clear();
  document.documentElement.lang = "en";
  for (const mock of [
    runtime.getBootstrap,
    runtime.createAdminClient,
    runtime.fromBootstrap,
    ...Object.values(runtime.auth),
    ...Object.values(runtime.client),
  ]) {
    mock.mockReset();
  }
  runtime.getBootstrap.mockResolvedValue({
    capabilities: [StaticCapability.OFFICIAL_UPLOADS],
    publicAssetBaseUrl: "https://assets.example.com/uploads/",
  });
  runtime.createAdminClient.mockReturnValue(runtime.client);
  runtime.fromBootstrap.mockReturnValue(runtime.auth);
  runtime.auth.completeCallback.mockResolvedValue(false);
  runtime.auth.isAuthenticated.mockResolvedValue(true);
  runtime.client.listUsers.mockResolvedValue({ users: [user], nextPageToken: "" });
  runtime.client.listUploads.mockResolvedValue({ uploads: [upload], nextPageToken: "" });
  runtime.client.listAuditEvents.mockResolvedValue({ auditEvents: [], nextPageToken: "" });
  HTMLDialogElement.prototype.showModal = function showModal() {
    this.setAttribute("open", "");
  };
});

afterEach(cleanup);

describe("administrator console review regressions", () => {
  it("keeps continuation tokens bound to the submitted search query", async () => {
    runtime.client.listUsers
      .mockResolvedValueOnce({ users: [user], nextPageToken: "" })
      .mockResolvedValueOnce({ users: [user], nextPageToken: "scoped-token" })
      .mockResolvedValueOnce({ users: [], nextPageToken: "" });
    render(<App />);
    const search = await screen.findByRole("search");
    const input = screen.getByPlaceholderText("Search by name, email, or Logto subject");
    fireEvent.change(input, { target: { value: "submitted" } });
    fireEvent.submit(search);
    await screen.findByRole("button", { name: "Load more" });
    fireEvent.change(input, { target: { value: "draft" } });
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    await waitFor(() => expect(runtime.client.listUsers).toHaveBeenCalledTimes(3));
    expect(runtime.client.listUsers.mock.calls[2]?.[0]).toMatchObject({
      query: "submitted",
      page: { pageToken: "scoped-token" },
    });
  });

  it("ignores user responses superseded by a newer search", async () => {
    type UserListResponse = { users: Array<typeof user>; nextPageToken: string };
    let resolveInitial = (_response: UserListResponse) => {};
    const initial = new Promise<UserListResponse>((resolve) => {
      resolveInitial = resolve;
    });
    const newerUser = {
      ...user,
      userId: { value: "018f7c1e-7b4a-7abc-8def-0123456789b0" },
      displayName: "Newer Result",
    };
    const staleUser = {
      ...user,
      userId: { value: "018f7c1e-7b4a-7abc-8def-0123456789b1" },
      displayName: "Stale Result",
    };
    runtime.client.listUsers
      .mockReturnValueOnce(initial)
      .mockResolvedValueOnce({ users: [newerUser], nextPageToken: "" });

    render(<App />);
    const search = await screen.findByRole("search");
    fireEvent.change(screen.getByPlaceholderText("Search by name, email, or Logto subject"), {
      target: { value: "newer" },
    });
    fireEvent.submit(search);
    expect(await screen.findByText("Newer Result")).toBeTruthy();

    await act(async () => {
      resolveInitial({ users: [staleUser], nextPageToken: "stale-token" });
      await initial;
    });
    expect(screen.getByText("Newer Result")).toBeTruthy();
    expect(screen.queryByText("Stale Result")).toBeNull();
    expect(screen.queryByRole("button", { name: "Load more" })).toBeNull();
  });

  it("renders correlated unavailable errors and retries from the first page", async () => {
    const correlationId = "018f7c1e-7b4a-7abc-8def-0123456789b2";
    runtime.client.listUsers
      .mockRejectedValueOnce(new ConnectError("temporarily unavailable", Code.Unavailable, {
        "x-devhud-correlation-id": correlationId,
      }))
      .mockResolvedValueOnce({ users: [user], nextPageToken: "" });

    render(<App />);
    expect(
      await screen.findByText("The administrator service is temporarily unavailable. Try again."),
    ).toBeTruthy();
    expect(screen.getByText(correlationId)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("Target User")).toBeTruthy();
    expect(runtime.client.listUsers.mock.calls[1]?.[0]).toMatchObject({
      page: { pageToken: "" },
    });
  });

  it("renders the typed pagination recovery path", async () => {
    runtime.client.listUsers.mockRejectedValueOnce(
      new ConnectError("page token scope mismatch", Code.InvalidArgument, undefined, [
        {
          desc: PaginationFailureSchema,
          value: { reason: PaginationFailureReason.TOKEN_SCOPE_MISMATCH },
        },
      ]),
    );

    render(<App />);
    expect(
      await screen.findByText(
        "These results changed or expired. Reload the first page to continue.",
      ),
    ).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy();
  });

  it("guides reauthentication without offering a failing list retry", async () => {
    runtime.client.listUsers.mockRejectedValueOnce(
      new ConnectError("expired", Code.Unauthenticated),
    );

    render(<App />);
    expect(
      await screen.findByText(
        "Your administrator session expired. Sign out, then sign in again.",
      ),
    ).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    expect(screen.getByRole("button", { name: "Sign out" })).toBeTruthy();
  });

  it("hides quota inspection without the official uploads capability", async () => {
    runtime.getBootstrap.mockResolvedValue({
      capabilities: [],
      publicAssetBaseUrl: "https://assets.example.com/uploads/",
    });
    render(<App />);
    await screen.findByText("Target User");
    expect(screen.queryByRole("button", { name: "Usage and quota" })).toBeNull();
  });

  it("validates mutation reasons by shared UTF-8 and sensitive-content rules", async () => {
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Block" }));
    const reason = screen.getByLabelText("Reason");
    const confirmation = screen.getByLabelText(
      "I understand this action is destructive.",
    );
    const submit = screen.getByRole("button", { name: "Confirm" });
    fireEvent.change(reason, { target: { value: "가".repeat(1366) } });
    fireEvent.click(confirmation);
    expect((submit as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("Enter a safe reason no longer than 4 KiB of UTF-8.")).toBeTruthy();
    fireEvent.change(reason, { target: { value: "See /Users/example/private/incident.txt" } });
    expect((submit as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(reason, { target: { value: "Reviewed the policy violation." } });
    expect((submit as HTMLButtonElement).disabled).toBe(false);
  });

  it("reloads user and upload records after concurrent mutation conflicts", async () => {
    runtime.client.setUserBlocked.mockRejectedValue(new ConnectError("changed", Code.Aborted));
    runtime.client.quarantineUpload.mockRejectedValue(new ConnectError("changed", Code.Aborted));
    render(<App />);

    fireEvent.click(await screen.findByRole("button", { name: "Block" }));
    fireEvent.change(screen.getByLabelText("Reason"), {
      target: { value: "Reviewed the policy violation." },
    });
    fireEvent.click(screen.getByLabelText("I understand this action is destructive."));
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await screen.findByText(
      "The record changed. Current data was reloaded; review it before trying again.",
    );
    await waitFor(() => expect(runtime.client.listUsers).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByRole("button", { name: "Uploads" }));
    fireEvent.click(await screen.findByRole("button", { name: "Quarantine" }));
    fireEvent.change(screen.getByLabelText("Reason"), {
      target: { value: "Reviewed the uploaded object." },
    });
    fireEvent.click(screen.getByLabelText("I understand this action is destructive."));
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(runtime.client.listUploads).toHaveBeenCalledTimes(2));
    expect(
      screen.getByText(
        "The record changed. Current data was reloaded; review it before trying again.",
      ),
    ).toBeTruthy();
  });

  it("renders administrator labels and enum values in Korean", async () => {
    localStorage.setItem("devhud.admin.locale", "ko");
    runtime.client.listAuditEvents.mockResolvedValue({
      auditEvents: [{
        auditEventId: { value: "018f7c1e-7b4a-7abc-8def-0123456789aa" },
        action: AuditAction.USER_BLOCKED,
        outcome: AuditOutcome.REJECTED,
      }],
      nextPageToken: "",
    });
    render(<App />);
    expect(await screen.findByText("신원")).toBeTruthy();
    expect(document.documentElement.lang).toBe("ko");
    expect(await screen.findByText("Logto 주체")).toBeTruthy();
    expect(screen.getByText("차단되지 않음")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "업로드" }));
    expect(await screen.findByText("콘텐츠 관리")).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "그룹" })).toBeTruthy();
    expect(screen.getByText("완료됨")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "감사 기록" }));
    expect(await screen.findByText("사용자 차단")).toBeTruthy();
    expect(screen.getByText("거부됨")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "English" }));
    expect(document.documentElement.lang).toBe("en");
  });
});
