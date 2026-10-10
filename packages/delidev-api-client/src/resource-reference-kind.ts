// SPDX-License-Identifier: Apache-2.0
import { EntityKind } from "./gen/delidev/v1/common_pb.js";
import { ProductReferenceKind as Kind } from "./product-references.js";
export function resourceReferenceKind(kind?: EntityKind): Kind {
  switch (kind) {
    case EntityKind.PROJECT: return Kind.Project;
    case EntityKind.REPOSITORY: return Kind.Repository;
    case EntityKind.AGENT: return Kind.Agent;
    case EntityKind.ACCOUNT: return Kind.Account;
    case EntityKind.PROVIDER: return Kind.Provider;
    case EntityKind.MODEL: return Kind.Model;
    case EntityKind.MACHINE: return Kind.Worker;
    case EntityKind.SESSION: return Kind.Session;
    case EntityKind.TEMPLATE: return Kind.Template;
    case EntityKind.SCHEDULE: return Kind.Schedule;
    case EntityKind.SNAPSHOT: return Kind.Snapshot;
    case EntityKind.DEVICE: return Kind.Device;
    case EntityKind.JOB: return Kind.Operation;
    case EntityKind.QUEUE: return Kind.Input;
    default: return Kind.Resource;
  }
}
