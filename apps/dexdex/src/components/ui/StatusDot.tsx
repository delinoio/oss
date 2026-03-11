import {
  AgentSessionStatus,
  PrStatus,
  SubTaskStatus,
  UnitTaskStatus,
} from "../../gen/v1/dexdex_pb";

export function unitTaskDotClass(status: number): string {
  switch (status) {
    case UnitTaskStatus.IN_PROGRESS:     return "dot-running";
    case UnitTaskStatus.COMPLETED:       return "dot-completed";
    case UnitTaskStatus.FAILED:          return "dot-failed";
    case UnitTaskStatus.ACTION_REQUIRED: return "dot-action-required";
    case UnitTaskStatus.BLOCKED:         return "dot-warning";
    case UnitTaskStatus.CANCELLED:       return "dot-cancelled";
    default:                             return "dot-pending";
  }
}

export function subTaskDotClass(status: number): string {
  switch (status) {
    case SubTaskStatus.IN_PROGRESS:               return "dot-running";
    case SubTaskStatus.COMPLETED:                 return "dot-completed";
    case SubTaskStatus.FAILED:                    return "dot-failed";
    case SubTaskStatus.WAITING_FOR_PLAN_APPROVAL: return "dot-waiting";
    case SubTaskStatus.WAITING_FOR_USER_INPUT:    return "dot-action-required";
    case SubTaskStatus.CANCELLED:                 return "dot-cancelled";
    default:                                      return "dot-pending";
  }
}

export function sessionDotClass(status: number): string {
  switch (status) {
    case AgentSessionStatus.RUNNING:          return "dot-running";
    case AgentSessionStatus.COMPLETED:        return "dot-completed";
    case AgentSessionStatus.FAILED:           return "dot-failed";
    case AgentSessionStatus.WAITING_FOR_INPUT: return "dot-waiting";
    case AgentSessionStatus.STARTING:         return "dot-pending";
    case AgentSessionStatus.CANCELLED:        return "dot-cancelled";
    default:                                  return "dot-default";
  }
}

export function prDotClass(status: number): string {
  switch (status) {
    case PrStatus.OPEN:               return "dot-open";
    case PrStatus.APPROVED:           return "dot-approved";
    case PrStatus.MERGED:             return "dot-merged";
    case PrStatus.CHANGES_REQUESTED:  return "dot-changes-requested";
    case PrStatus.CLOSED:             return "dot-closed";
    case PrStatus.CI_FAILED:          return "dot-ci-failed";
    default:                          return "dot-default";
  }
}

type StatusDotProps = {
  className: string;
};

export function StatusDot({ className }: StatusDotProps) {
  return <span className={`item-row-dot ${className}`} />;
}
