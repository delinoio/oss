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
import {
  PresentationFailureLog,
  PresentationOutcome,
  PresentationPhase,
  type PresentationRecord,
} from "./presentation-diagnostics";

function fixture() {
  const candidate = create(NotificationCandidateSchema, {
    inboxId: newRequestId(),
    sessionId: newRequestId(),
    kind: NotificationKind.REQUEST,
  });
  const claimed = new Set<string>();
  const claim = vi.fn((request: { requestId: string; inboxId: string }) => {
    const fresh = !claimed.has(request.inboxId);
    claimed.add(request.inboxId);
    return create(ClaimNotificationResponseSchema, {
      requestId: request.requestId,
      mayPresent: fresh,
      delivery: {
        claimId: request.requestId,
        state: NotificationState.CLAIMED,
        candidate: { ...candidate, inboxId: request.inboxId },
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
      PresentationPhase.NotificationReport,
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
  const before = value.problem.mock.calls.length;
  pump.close();
  release();
  await new Promise<void>((resolve) => setTimeout(resolve, 0));
  expect(value.bridge.present).not.toHaveBeenCalled();
  expect(value.problem).toHaveBeenCalledTimes(before);
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
    const before = value.problem.mock.calls.length;
    value.pump.close();
    reject(new Error("private native detail"));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(value.problem).toHaveBeenCalledTimes(before);
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

// Every scenario is one cell of the notification rows in the diagnostics table
// of the desktop contract. The pump reports through the real reducer, so the
// expected list is the exact sequence of warning and recovery records.
type Fixture = ReturnType<typeof fixture>;
const failedAt = (phase: PresentationPhase): PresentationRecord => ({
  phase,
  outcome: PresentationOutcome.Failed,
});
const recoveredAt = (phase: PresentationPhase): PresentationRecord => ({
  phase,
  outcome: PresentationOutcome.Recovered,
});
const invalidPage = (value: Fixture) =>
  Array.from({ length: 51 }, () => value.candidate);
const another = (value: Fixture) => ({
  ...value.candidate,
  inboxId: newRequestId(),
});
const idle = (value: Fixture) =>
  vi.waitFor(() =>
    expect((value.pump as unknown as { running: boolean }).running).toBe(false),
  );
const refuse = () => {
  throw new ConnectError("Lost", Code.Unavailable);
};
const grant =
  (value: Fixture) => (request: { requestId: string; inboxId: string }) =>
    create(ClaimNotificationResponseSchema, {
      requestId: request.requestId,
      mayPresent: false,
      delivery: {
        claimId: request.requestId,
        state: NotificationState.CLAIMED,
        candidate: { ...value.candidate, inboxId: request.inboxId },
      },
    });
const cells: ReadonlyArray<{
  name: string;
  steps: (value: Fixture) => Promise<void>;
  records: PresentationRecord[];
}> = [
  {
    name: "a submitted notice on a valid page records nothing",
    steps: async (v) => {
      v.pump.update([v.candidate]);
      await idle(v);
    },
    records: [],
  },
  {
    name: "an oversized page fails notification-candidate-invalid",
    steps: async (v) => v.pump.update(invalidPage(v)),
    records: [failedAt(PresentationPhase.NotificationCandidateInvalid)],
  },
  {
    name: "a repeated invalid page adds no record",
    steps: async (v) => {
      v.pump.update(invalidPage(v));
      v.pump.update(invalidPage(v));
    },
    records: [failedAt(PresentationPhase.NotificationCandidateInvalid)],
  },
  {
    name: "a valid page recovers notification-candidate-invalid",
    steps: async (v) => {
      v.pump.update(invalidPage(v));
      v.pump.update([v.candidate]);
      await idle(v);
    },
    records: [
      failedAt(PresentationPhase.NotificationCandidateInvalid),
      recoveredAt(PresentationPhase.NotificationCandidateInvalid),
    ],
  },
  {
    name: "a rejected begin fails notification-scope-begin and claims nothing",
    steps: async (v) => {
      v.bridge.begin.mockRejectedValueOnce(new Error("private"));
      v.pump.update([v.candidate]);
      await idle(v);
      expect(v.claim).not.toHaveBeenCalled();
    },
    records: [failedAt(PresentationPhase.NotificationScopeBegin)],
  },
  {
    name: "the next page recovers notification-scope-begin",
    steps: async (v) => {
      v.bridge.begin.mockRejectedValueOnce(new Error("private"));
      v.pump.update([v.candidate]);
      await idle(v);
      v.pump.update([v.candidate]);
      await idle(v);
    },
    records: [
      failedAt(PresentationPhase.NotificationScopeBegin),
      recoveredAt(PresentationPhase.NotificationScopeBegin),
    ],
  },
  {
    name: "an invalid scope fails notification-scope-begin and claims nothing",
    steps: async (v) => {
      v.bridge.begin.mockResolvedValueOnce("not an entity id");
      v.pump.update([v.candidate]);
      await idle(v);
      expect(v.claim).not.toHaveBeenCalled();
    },
    records: [failedAt(PresentationPhase.NotificationScopeBegin)],
  },
  {
    name: "a rejected claim fails notification-pump and shows nothing",
    steps: async (v) => {
      v.claim.mockImplementationOnce(refuse);
      v.pump.update([v.candidate]);
      await idle(v);
      expect(v.bridge.present).not.toHaveBeenCalled();
    },
    records: [failedAt(PresentationPhase.NotificationPump)],
  },
  {
    name: "a later verified claim recovers notification-pump",
    steps: async (v) => {
      v.claim.mockImplementationOnce(refuse);
      v.pump.update([v.candidate]);
      await idle(v);
      v.pump.update([v.candidate]);
      await idle(v);
    },
    records: [
      failedAt(PresentationPhase.NotificationPump),
      recoveredAt(PresentationPhase.NotificationPump),
    ],
  },
  {
    name: "a claim that may not present also recovers notification-pump",
    steps: async (v) => {
      v.claim.mockImplementationOnce(refuse);
      v.pump.update([v.candidate]);
      await idle(v);
      v.claim.mockImplementationOnce(grant(v));
      v.pump.update([v.candidate]);
      await idle(v);
      expect(v.bridge.present).not.toHaveBeenCalled();
    },
    records: [
      failedAt(PresentationPhase.NotificationPump),
      recoveredAt(PresentationPhase.NotificationPump),
    ],
  },
  {
    name: "an unverifiable claim fails notification-pump",
    steps: async (v) => {
      v.claim.mockImplementationOnce((request: { requestId: string }) =>
        create(ClaimNotificationResponseSchema, {
          requestId: request.requestId,
          mayPresent: true,
          replayed: true,
          delivery: {
            claimId: request.requestId,
            state: NotificationState.CLAIMED,
            candidate: v.candidate,
          },
        }),
      );
      v.pump.update([v.candidate]);
      await idle(v);
      expect(v.bridge.present).not.toHaveBeenCalled();
    },
    records: [failedAt(PresentationPhase.NotificationPump)],
  },
  {
    name: "a claim that was not found records nothing",
    steps: async (v) => {
      v.claim.mockImplementationOnce(() => {
        throw new ConnectError("Gone", Code.NotFound);
      });
      v.pump.update([v.candidate]);
      await idle(v);
    },
    records: [],
  },
  {
    name: "a denied display fails notification-present and its report completes",
    steps: async (v) => {
      v.bridge.present.mockResolvedValueOnce(Result.Denied);
      v.pump.update([v.candidate]);
      await idle(v);
      expect(v.report).toHaveBeenCalledTimes(1);
    },
    records: [failedAt(PresentationPhase.NotificationPresent)],
  },
  {
    name: "a thrown display fails notification-present",
    steps: async (v) => {
      v.bridge.present.mockRejectedValueOnce(new Error("private"));
      v.pump.update([v.candidate]);
      await idle(v);
    },
    records: [failedAt(PresentationPhase.NotificationPresent)],
  },
  {
    name: "a later submitted display recovers notification-present",
    steps: async (v) => {
      v.bridge.present.mockResolvedValueOnce(Result.Failed);
      v.pump.update([v.candidate]);
      await idle(v);
      v.pump.update([another(v)]);
      await idle(v);
    },
    records: [
      failedAt(PresentationPhase.NotificationPresent),
      recoveredAt(PresentationPhase.NotificationPresent),
    ],
  },
  {
    name: "a failed display and a rejected report fail both phases in one batch",
    steps: async (v) => {
      v.bridge.present.mockResolvedValueOnce(Result.Uncertain);
      v.report.mockImplementationOnce(refuse);
      v.pump.update([v.candidate]);
      await idle(v);
    },
    records: [
      failedAt(PresentationPhase.NotificationPresent),
      failedAt(PresentationPhase.NotificationReport),
    ],
  },
  {
    name: "a submitted display and a rejected report fail only notification-report",
    steps: async (v) => {
      v.report.mockImplementationOnce(refuse);
      v.pump.update([v.candidate]);
      await idle(v);
    },
    records: [failedAt(PresentationPhase.NotificationReport)],
  },
  {
    name: "a later completed report recovers notification-report",
    steps: async (v) => {
      v.report.mockImplementationOnce(refuse);
      v.pump.update([v.candidate]);
      await idle(v);
      v.pump.update([another(v)]);
      await idle(v);
    },
    records: [
      failedAt(PresentationPhase.NotificationReport),
      recoveredAt(PresentationPhase.NotificationReport),
    ],
  },
  {
    name: "a failed display recovers every earlier phase it did not fail",
    steps: async (v) => {
      v.bridge.begin.mockRejectedValueOnce(new Error("private"));
      v.pump.update([v.candidate]);
      await idle(v);
      v.claim.mockImplementationOnce(refuse);
      v.pump.update([v.candidate]);
      await idle(v);
      v.report.mockImplementationOnce(refuse);
      v.pump.update(invalidPage(v));
      v.bridge.present.mockResolvedValueOnce(Result.Denied);
      v.pump.update([another(v)]);
      await idle(v);
    },
    records: [
      failedAt(PresentationPhase.NotificationScopeBegin),
      recoveredAt(PresentationPhase.NotificationScopeBegin),
      failedAt(PresentationPhase.NotificationPump),
      failedAt(PresentationPhase.NotificationCandidateInvalid),
      recoveredAt(PresentationPhase.NotificationCandidateInvalid),
      recoveredAt(PresentationPhase.NotificationPump),
      failedAt(PresentationPhase.NotificationPresent),
      failedAt(PresentationPhase.NotificationReport),
    ],
  },
];
it.each(cells)("$name", async ({ steps, records }) => {
  const value = fixture();
  const observed: PresentationRecord[] = [];
  const log = new PresentationFailureLog();
  vi.spyOn(console, "warn").mockImplementation((_name, record) => {
    observed.push(record);
  });
  vi.spyOn(console, "info").mockImplementation((_name, record) => {
    observed.push(record);
  });
  value.problem.mockImplementation((failed: boolean, phase) =>
    log.report(phase, failed),
  );
  await steps(value);
  value.pump.close();
  expect(observed).toEqual(records);
  expect(JSON.stringify(observed)).not.toContain("private");
  vi.restoreAllMocks();
});

it.each([
  ["a pending display", "present"],
  ["a pending report", "report"],
] as const)(
  "%s that settles after the pump closed records nothing",
  async (_name, held) => {
    const value = fixture();
    let settle!: () => void;
    const gate = new Promise<void>((resolve) => {
      settle = resolve;
    });
    value.bridge.present.mockImplementationOnce(async () => {
      if (held === "present") await gate;
      return Result.Denied;
    });
    value.report.mockImplementationOnce(async () => {
      await gate;
      throw new ConnectError("Lost", Code.Unavailable);
    });
    value.pump.update([value.candidate]);
    await vi.waitFor(() =>
      expect(
        held === "present" ? value.bridge.present : value.report,
      ).toHaveBeenCalledTimes(1),
    );
    const before = value.problem.mock.calls.length;
    value.pump.close();
    settle();
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(value.problem).toHaveBeenCalledTimes(before);
    if (held === "present") expect(value.report).not.toHaveBeenCalled();
  },
);
