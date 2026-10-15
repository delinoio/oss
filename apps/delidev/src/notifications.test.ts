import { create } from "@bufbuild/protobuf";
import {
  Code,
  ConnectError,
  createClient,
  createRouterTransport,
} from "@connectrpc/connect";
import { expect, it, vi } from "vitest";
import {
  ClaimNotificationResponseSchema,
  InboxService,
  NotificationCandidateSchema,
  NotificationKind,
  NotificationState,
  newRequestId,
} from "@delinoio/delidev-api-client";
import {
  NativeNotificationResult as Result,
  NotificationPump,
  notificationReadiness,
  notificationReady,
} from "./notifications";
import { PresentationPhase } from "./presentation-diagnostics";

function fixture() {
  const candidate = create(NotificationCandidateSchema, {
    inboxId: newRequestId(),
    sessionId: newRequestId(),
    kind: NotificationKind.REQUEST,
  });
  let claimed = false;
  const claim = vi.fn((request: { requestId: string }) => {
    const fresh = !claimed;
    claimed = true;
    return create(ClaimNotificationResponseSchema, {
      requestId: request.requestId,
      mayPresent: fresh,
      delivery: {
        claimId: request.requestId,
        state: NotificationState.CLAIMED,
        candidate,
      },
    });
  });
  const report = vi.fn(() => ({}));
  const service = createClient(
    InboxService,
    createRouterTransport((router) =>
      router.service(InboxService, {
        claimNotification: claim,
        reportNotification: report,
      }),
    ),
  );
  const bridge = {
      end: vi.fn(async () => {}),
      begin: vi.fn(async () => newRequestId()),
      present: vi.fn(async () => Result.Submitted),
    },
    problem = vi.fn(),
    changed = vi.fn();
  const pump = new NotificationPump(service, bridge, problem, changed);
  return { candidate, claim, report, bridge, problem, changed, pump };
}

it("presents only a fresh exact claim and never replays its native side effect", async () => {
  const value = fixture();
  value.pump.update([value.candidate]);
  await vi.waitFor(() => expect(value.changed).toHaveBeenCalledTimes(1));
  expect(value.bridge.present).toHaveBeenCalledTimes(1);
  const notice = value.bridge.present.mock.calls[0] as unknown as [
    string,
    { claim_id: string; inbox_id: string; kind: string },
  ];
  expect(notice[1]).toEqual({
    claim_id: value.claim.mock.calls[0][0].requestId,
    inbox_id: value.candidate.inboxId,
    kind: "request",
  });
  expect(JSON.stringify(notice)).not.toContain(value.candidate.sessionId);
  expect(value.report).toHaveBeenCalledWith(
    expect.objectContaining({
      state: NotificationState.SUBMITTED,
      claimId: notice[1].claim_id,
    }),
    expect.anything(),
  );
  value.pump.update([value.candidate]);
  await vi.waitFor(() => expect(value.changed).toHaveBeenCalledTimes(2));
  expect(value.bridge.begin).toHaveBeenCalledTimes(1);
  expect(value.bridge.present).toHaveBeenCalledTimes(1);
  value.pump.close();
});

it.each(["replayed", "mismatched"])(
  "refuses %s claim authority",
  async (kind) => {
    const value = fixture();
    value.claim.mockImplementation((request) =>
      create(ClaimNotificationResponseSchema, {
        requestId: request.requestId,
        mayPresent: true,
        replayed: kind === "replayed",
        delivery: {
          claimId: request.requestId,
          state: NotificationState.CLAIMED,
          candidate: {
            ...value.candidate,
            sessionId:
              kind === "mismatched"
                ? newRequestId()
                : value.candidate.sessionId,
          },
        },
      }),
    );
    value.pump.update([value.candidate]);
    await vi.waitFor(() =>
      expect(value.problem).toHaveBeenCalledWith(
        true,
        PresentationPhase.NotificationPump,
      ),
    );
    expect(value.bridge.present).not.toHaveBeenCalled();
    expect(value.report).not.toHaveBeenCalled();
    value.pump.close();
  },
);

it("retains an uncertain server reservation after a lost acknowledgment without retrying display", async () => {
  const value = fixture();
  value.claim.mockImplementationOnce(() => {
    throw new ConnectError("Lost acknowledgment", Code.Unavailable);
  });
  value.pump.update([value.candidate]);
  await vi.waitFor(() =>
    expect(value.problem).toHaveBeenCalledWith(
      true,
      PresentationPhase.NotificationPump,
    ),
  );
  expect(value.claim).toHaveBeenCalledTimes(1);
  expect(value.bridge.present).not.toHaveBeenCalled();
  expect(value.changed).not.toHaveBeenCalled();
  value.pump.close();
});

it.each([Result.Denied, Result.Failed, Result.Uncertain])(
  "reports native %s once and stops that batch",
  async (result) => {
    const value = fixture();
    value.bridge.present.mockResolvedValue(result);
    value.pump.update([
      value.candidate,
      { ...value.candidate, inboxId: newRequestId() },
    ]);
    await vi.waitFor(() =>
      expect(value.problem).toHaveBeenCalledWith(
        true,
        PresentationPhase.NotificationPresent,
      ),
    );
    expect(value.claim).toHaveBeenCalledTimes(1);
    expect(value.report).toHaveBeenCalledTimes(1);
    expect(value.changed).not.toHaveBeenCalled();
    value.pump.close();
  },
);

it("does not repeat a submitted notice when metadata reporting fails", async () => {
  const value = fixture();
  value.report.mockImplementationOnce(() => {
    throw new ConnectError("Lost report", Code.Unavailable);
  });
  value.pump.update([value.candidate]);
  await vi.waitFor(() =>
    expect(value.problem).toHaveBeenCalledWith(
      true,
      PresentationPhase.NotificationPump,
    ),
  );
  value.pump.update([value.candidate]);
  await vi.waitFor(() => expect(value.changed).toHaveBeenCalledTimes(1));
  expect(value.bridge.present).toHaveBeenCalledTimes(1);
  expect(value.report).toHaveBeenCalledTimes(1);
  value.pump.close();
});

it("cancels outstanding connection work and cannot display after disposal", async () => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const value = fixture(),
    original = value.claim.getMockImplementation()!;
  // The real transport may finish a committed mutation even after cancellation.
  const service = createClient(
    InboxService,
    createRouterTransport((router) =>
      router.service(InboxService, {
        claimNotification: async (request) => {
          value.claim(request);
          await held;
          return original(request);
        },
      }),
    ),
  );
  const pump = new NotificationPump(
    service,
    value.bridge,
    value.problem,
    value.changed,
  );
  pump.update([value.candidate]);
  await vi.waitFor(() => expect(value.claim).toHaveBeenCalledTimes(1));
  pump.close();
  release();
  await new Promise<void>((resolve) => setTimeout(resolve, 0));
  expect(value.bridge.present).not.toHaveBeenCalled();
  expect(value.problem).not.toHaveBeenCalled();
});

it("reports a native scope initialization failure by phase and recovers on the next refresh", async () => {
  const value = fixture();
  value.bridge.begin.mockRejectedValueOnce(new Error("private native detail"));
  value.pump.prepare();
  await vi.waitFor(() =>
    expect(value.problem).toHaveBeenCalledWith(
      true,
      PresentationPhase.NotificationScopeBegin,
    ),
  );
  expect(JSON.stringify(value.problem.mock.calls)).not.toContain("private");
  value.pump.update([value.candidate]);
  await vi.waitFor(() => expect(value.changed).toHaveBeenCalledTimes(1));
  expect(value.problem).toHaveBeenLastCalledWith(
    false,
    PresentationPhase.NotificationPresent,
  );
  value.pump.close();
});

// Disposal and replacement cells of the pump rows in the diagnostics table of
// the desktop contract: a begin rejection that settles after the connection
// closed reports nothing, whether the pump was only prepared or already drained.
it.each([
  ["prepared", false],
  ["draining", true],
] as const)(
  "reports no scope failure that settles after a %s pump closed",
  async (_name, draining) => {
    const value = fixture();
    let reject!: (error: Error) => void;
    value.bridge.begin.mockReturnValueOnce(
      new Promise<string>((_, fail) => {
        reject = fail;
      }),
    );
    if (draining) value.pump.update([value.candidate]);
    else value.pump.prepare();
    await vi.waitFor(() => expect(value.bridge.begin).toHaveBeenCalledTimes(1));
    value.pump.close();
    reject(new Error("private native detail"));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(value.problem).not.toHaveBeenCalled();
    expect(value.claim).not.toHaveBeenCalled();
  },
);

it("reports a scope failure that settles while the pump is still open", async () => {
  const value = fixture();
  let reject!: (error: Error) => void;
  value.bridge.begin.mockReturnValueOnce(
    new Promise<string>((_, fail) => {
      reject = fail;
    }),
  );
  value.pump.prepare();
  reject(new Error("private native detail"));
  await vi.waitFor(() =>
    expect(value.problem).toHaveBeenCalledWith(
      true,
      PresentationPhase.NotificationScopeBegin,
    ),
  );
  value.pump.close();
});

it("rejects oversized pages before native initialization and separates Linux service availability from permission", () => {
  const value = fixture();
  value.pump.update(Array.from({ length: 51 }, () => value.candidate));
  expect(value.problem).toHaveBeenCalledWith(
    true,
    PresentationPhase.NotificationCandidateInvalid,
  );
  expect(value.bridge.begin).not.toHaveBeenCalled();
  expect(
    notificationReady(
      notificationReadiness({
        permission: "service-available",
        problem: "none",
      }),
    ),
  ).toBe(true);
  expect(
    notificationReady(
      notificationReadiness({ permission: "unavailable", problem: "capacity" }),
    ),
  ).toBe(false);
  expect(() =>
    notificationReadiness({ permission: "probably-granted", problem: "none" }),
  ).toThrow();
  value.pump.close();
});
