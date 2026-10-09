import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { RejectedInput, StartupRejection, startupRejection, isImageStartupRejectedInput } from "./startup-rejection";

function fixture() {
  const id = newRequestId(), execution = newRequestId(), input = newRequestId(), machine = newRequestId(), account = newRequestId(), connection = newRequestId(), hash = "a".repeat(64);
  const proof = { job_id: newRequestId(), execution_id: execution, session_id: id, preparation_digest: hash, manifest_digest: hash, target_digest: hash, journal_digest: hash, reason: "conflict", started_at: "2026-09-28T00:00:00.000000001Z", finished_at: "2026-09-28T00:00:01.000000002Z" };
  const rejection = { version: 1, type: "pr-startup-rejected", server_id: newRequestId(), device_id: newRequestId(), instance_id: newRequestId(), machine_id: machine, input_id: input, account_id: account, connection_id: connection, assignment_revision: "18446744073709551615", assignment_digest: hash, assignment_input_digest: hash, configuration_digest: hash, workspace: proof };
  const data = { workspace: "worktree", outcome: "not-started", dispatch: "paused", recovery: "none", machine_id: machine, initial_execution: { id: execution, input_id: input, initial_account_id: account, connection_id: connection, configuration_digest: hash }, startup_rejection: rejection };
  const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 9n, documentJson: encode(data) });
  const queued = create(ResourceSchema, { id: input, sessionId: id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 3n, documentJson: encode({ delivery: "rejected-before-start", execution_id: execution }) });
  return { data, session, queued };
}

it("shows an original pre-native rejection without native completion or resend actions", () => {
  const f = fixture();
  render(<><StartupRejection session={f.session} /><RejectedInput session={f.session} resource={f.queued} /></>);
  expect(screen.getByRole("region", { name: "Agent startup rejection" }).textContent).toContain("Agent did not start");
  expect(screen.getByText(/This input is retained in history and will not be sent again/)).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
  expect(startupRejection(f.session)?.assignment_revision).toBe("18446744073709551615");
});

it.each(["foreign-input", "foreign-account", "native-progress", "active-execution", "wrong-session", "mixed-proof", "rounded-revision", "unknown-reason", "invalid-time", "nanosecond-order", "invalid-calendar", "recovery"])("keeps %s rejection evidence unavailable", (scenario) => {
  const f = fixture();
  const data: Record<string, unknown> = structuredClone(f.data);
  const rejection = data.startup_rejection as typeof f.data.startup_rejection;
  switch (scenario) {
    case "foreign-input": rejection.input_id = newRequestId(); break;
    case "foreign-account": rejection.account_id = newRequestId(); break;
    case "native-progress": data.execution = { native_thread_id: newRequestId() }; break;
    case "active-execution": data.active_execution_id = newRequestId(); break;
    case "wrong-session": rejection.workspace.session_id = newRequestId(); break;
    case "mixed-proof": Object.assign(rejection, { native_completion: {} }); break;
    case "rounded-revision": Object.assign(rejection, { assignment_revision: 18446744073709551615 }); break;
    case "unknown-reason": rejection.workspace.reason = "recovery_required"; break;
    case "invalid-time": rejection.workspace.finished_at = "2026-09-27T00:00:00Z"; break;
    case "nanosecond-order": rejection.workspace.started_at = "2026-09-28T00:00:01.000000003Z"; break;
    case "invalid-calendar": rejection.workspace.finished_at = "2026-09-31T00:00:01Z"; break;
    case "recovery": data.recovery = "required"; break;
  }
  render(<StartupRejection session={create(ResourceSchema, { ...f.session, documentJson: encode(data) })} />);
  expect(screen.getByRole("status").textContent).toContain("unavailable");
  expect(screen.queryByRole("region", { name: "Agent startup rejection" })).toBeNull();
});

it("does not borrow another queued input's rejection", () => {
  const f = fixture();
  render(<RejectedInput session={f.session} resource={create(ResourceSchema, { ...f.queued, id: newRequestId() })} />);
  expect(screen.getByText(/could not be verified/)).toBeTruthy();
});

it("presents only the exact original rejected image input without claiming recovery", () => {
 const f=fixture(), job=newRequestId(), execution=f.data.initial_execution.id;
 const data={...f.data, initial_execution:f.data.initial_execution, startup:{job_id:job,execution_id:execution,failure:{state:2,phase:5,harness:"codex",problem_code:"unsupported",correlation_id:job,input_delivery:1,cleanup:1,failure_kind:1}}};
 const session=create(ResourceSchema,{...f.session,documentJson:encode(data)});
 expect(isImageStartupRejectedInput(f.queued,session)).toBe(true);
 for (const resource of [create(ResourceSchema,{...f.queued,id:newRequestId()}),create(ResourceSchema,{...f.queued,sessionId:newRequestId()}),create(ResourceSchema,{...f.queued,documentJson:encode({delivery:"accepted",execution_id:execution})})]) expect(isImageStartupRejectedInput(resource,session)).toBe(false);
 render(<RejectedInput session={session} resource={f.queued}/>);
 expect(screen.getByText(/Keep the image draft/)).toBeTruthy();
 expect(screen.queryByRole("button")).toBeNull();
});
