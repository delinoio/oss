// SPDX-License-Identifier: Apache-2.0
import { useEffect } from "react";
import {
  clientFailure,
  FailureCause,
  FailureCode,
  EntityKind,
} from "@delinoio/delidev-api-client";
import { type JobObservation, JobState } from "./jobs";
import { text } from "./documents";
import { copy } from "./localization";

export function isConfigurationNameConflict(error: unknown) {
  const failure = clientFailure(error);
  return (
    failure.code === FailureCode.Conflict &&
    failure.cause === FailureCause.ConfigurationNameConflict
  );
}
export function configurationNameConflictCopy(kind: EntityKind) {
  return kind === EntityKind.PROJECT
    ? copy("configuration-fields.projectNameConflict")
    : copy("configuration-fields.repositoryNameConflict");
}

// Only a verified terminal job can release its original retained request.
export function ConfigurationNameJobFailure({
  observation,
  active,
  onConflict,
}: {
  observation: JobObservation;
  active: boolean;
  onConflict: () => void;
}) {
  const collision =
    active &&
    observation.verified &&
    observation.state === JobState.Failed &&
    text(observation.problem.code) === FailureCode.Conflict &&
    text(observation.problem.cause) === FailureCause.ConfigurationNameConflict;
  useEffect(() => {
    if (collision) onConflict();
  }, [collision, onConflict]);
  return null;
}

import "./configuration-name-conflict.css";
