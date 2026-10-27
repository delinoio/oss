// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useMutation, useTransport } from "@connectrpc/connect-query";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import {
  InstanceSettingsSchema,
  RetentionSettingsSchema,
  OperatorService,
  type InstanceSettings,
} from "@aiprouter/client";
import { t as translate, useLocale } from "../i18n";
import { Card } from "../ui";
import { useDirtyForm } from "../api";
import { Confirm, ErrorNotice, useAction } from "./common";
import { parseLimit } from "./limits";

// Stable identifiers for the three retention fields. The values match the
// generated wire field names on `InstanceSettings.retention`.
enum RetentionField {
  Request = "requestDays",
  Aggregate = "aggregateDays",
  Audit = "auditDays",
}
// Outcome of comparing a validated draft value with the saved snapshot.
enum RetentionChange {
  Reduced = "Reduced",
  Increased = "Increased",
  Unchanged = "Unchanged",
}
type RetentionInputs = Record<RetentionField, string>;

// Copy is resolved at render time so it follows the selected console language.
const retentionFields = () =>
  [
    {
      field: RetentionField.Request,
      name: translate("requestMetadata"),
      help: translate("message190DaysDefault30"),
      min: 1,
      max: 90,
      error: translate("enterAWholeNumberFrom1To90Days"),
    },
    {
      field: RetentionField.Aggregate,
      name: translate("dailyAggregates"),
      help: translate("message30730DaysDefault365"),
      min: 30,
      max: 730,
      error: translate("enterAWholeNumberFrom30To730Days"),
    },
    {
      field: RetentionField.Audit,
      name: translate("auditRecords"),
      help: translate("message30365DaysDefault90"),
      min: 30,
      max: 365,
      error: translate("enterAWholeNumberFrom30To365Days"),
    },
  ] as const;

function days(value: number) {
  return translate(value === 1 ? "message1Day" : "retentionDayCount", {
    value0: value,
  });
}
function changeLabel(change: RetentionChange | undefined) {
  return change === RetentionChange.Reduced
    ? translate("reduced")
    : change === RetentionChange.Increased
      ? translate("increased")
      : translate("unchanged");
}
function toInputs(settings: InstanceSettings): RetentionInputs {
  return {
    [RetentionField.Request]: String(settings.retention?.requestDays ?? ""),
    [RetentionField.Aggregate]: String(settings.retention?.aggregateDays ?? ""),
    [RetentionField.Audit]: String(settings.retention?.auditDays ?? ""),
  };
}
function compare(saved: number, next: number) {
  return next < saved
    ? RetentionChange.Reduced
    : next > saved
      ? RetentionChange.Increased
      : RetentionChange.Unchanged;
}

export function RetentionForm({
  settings: initialSettings,
}: {
  settings: InstanceSettings;
}) {
  useLocale();
  const fields = retentionFields();
  // Keep the reviewed snapshot across background refetches. A newer revision
  // must not silently replace typed values or advance the reviewed revision.
  const [settings, setSettings] = useState(initialSettings);
  const [inputs, setInputs] = useState(() => toInputs(initialSettings));
  const [review, setReview] = useState(false);
  const [attempts, setAttempts] = useState(0);
  const [reloading, setReloading] = useState(false);
  const [reloadError, setReloadError] = useState<unknown>();
  const [reloaded, setReloaded] = useState(false);
  const dirty = useDirtyForm();
  const action = useAction(true);
  const update = useMutation(OperatorService.method.updateInstanceSettings);
  const transport = useTransport();
  const ids = useId();
  const summary = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);

  const saved = settings.retention;
  const savedInputs = toInputs(settings);
  const values = fields.map((item) =>
    parseLimit(inputs[item.field], item.min, item.max),
  );
  const valid = values.every((value) => value !== undefined);
  const differs = fields.some(
    (item) => inputs[item.field] !== savedInputs[item.field],
  );
  // Reductions are derived from the saved snapshot, never from defaults.
  const changes = fields.map((item, index) => {
    const value = values[index];
    return value === undefined || !saved
      ? undefined
      : compare(saved[item.field], value);
  });
  const reductions = changes.filter(
    (change) => change === RetentionChange.Reduced,
  ).length;
  const conflict =
    !!action.error && ConnectError.from(action.error).code === Code.Aborted;
  const errorFields = fields.filter(
    (item, index) => values[index] === undefined,
  );

  useEffect(() => {
    dirty(differs);
    return () => dirty(false);
  }, [dirty, differs]);
  // Move focus to the error summary after each rejected submit attempt.
  useEffect(() => {
    if (attempts > 0) summary.current?.focus();
  }, [attempts]);

  function build(): InstanceSettings {
    // Send the complete reviewed settings; only the retention days are replaced.
    return create(InstanceSettingsSchema, {
      ...settings,
      retention: create(RetentionSettingsSchema, {
        ...settings.retention,
        requestDays: values[0]!,
        aggregateDays: values[1]!,
        auditDays: values[2]!,
      }),
    });
  }
  function save() {
    if (!valid || action.pending) return;
    const next = build();
    setReloaded(false);
    void action.run(
      async () => {
        const response = await update.mutateAsync({
          settings: next,
          expectedRevision: settings.revision,
        });
        if (response.settings) {
          setSettings(response.settings);
          setInputs(toInputs(response.settings));
        }
      },
      { done: () => setReview(false) },
    );
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    if (action.pending || reloading || conflict || !saved) return;
    if (!valid) {
      setAttempts((count) => count + 1);
      return;
    }
    if (!differs) return;
    if (reductions > 0) {
      // Keyboard submission can start from an input; restore focus to the
      // visible trigger when the review closes.
      trigger.current?.focus();
      setReloaded(false);
      setReview(true);
    } else save();
  }
  // Explicit recovery: fetch a complete fresh snapshot, keep the typed
  // retention values and adopt every untouched field from the server.
  async function reload() {
    if (reloading) return;
    setReloading(true);
    setReloadError(undefined);
    try {
      const response = await createClient(
        OperatorService,
        transport,
      ).getInstanceSettings({});
      if (!response.settings?.retention)
        throw new ConnectError(
          translate("instanceSettingsAreUnavailable"),
          Code.Unavailable,
        );
      setSettings(response.settings);
      setReview(false);
      action.clearError();
      setReloaded(true);
    } catch (cause) {
      setReloadError(cause);
    } finally {
      setReloading(false);
    }
  }

  const conflictNotice = conflict && (
    <div className="admin-retention-conflict">
      <p>{translate("yourRetentionDraftIsPreservedReloadThe")}</p>
      <button type="button" disabled={reloading} onClick={() => void reload()}>
        {translate("reloadCurrentSettings")}
      </button>
      <ErrorNotice error={reloadError} />
    </div>
  );
  const status = !differs
    ? translate("noUnsavedChanges")
    : reductions > 0
      ? translate(
          reductions === 1
            ? "unsavedChanges1ReductionToReview"
            : "unsavedChangesReductionsToReview",
          { value0: reductions },
        )
      : translate("unsavedChanges");

  return (
    <Card className="admin-limits-card admin-retention-card">
      {action.feedback}
      {reloaded && (
        <p role="status">
          {translate("currentSettingsLoadedReviewYourDraftBefore")}
        </p>
      )}
      <h2>{translate("recordRetention")}</h2>
      <p className="admin-limits-description">
        {translate("enterWholeDaysWithinEachAllowedRange")}
      </p>
      <form
        noValidate
        onSubmit={submit}
        aria-label={translate("recordRetention")}
      >
        {errorFields.length > 0 && (
          <div
            ref={summary}
            role="alert"
            tabIndex={-1}
            className="admin-error admin-limits-summary"
          >
            <p>
              <strong>
                {translate("fixTheseRetentionPeriodsBeforeSaving")}
              </strong>
            </p>
            <ul>
              {errorFields.map((item) => (
                <li key={item.field}>
                  <a
                    href={`#${ids}-${item.field}`}
                    onClick={(event) => {
                      event.preventDefault();
                      document.getElementById(`${ids}-${item.field}`)?.focus();
                    }}
                  >
                    {item.name}
                  </a>
                  : {item.error}
                </li>
              ))}
            </ul>
          </div>
        )}
        <div className="admin-limit-rows">
          {fields.map((item, index) => {
            const inputId = `${ids}-${item.field}`;
            const error = values[index] === undefined ? item.error : undefined;
            return (
              <div className="admin-limit-row" key={item.field}>
                <div className="admin-limit-copy">
                  <label htmlFor={inputId}>
                    {item.name}{" "}
                    <span className="admin-visually-hidden">
                      {translate("daysSuffix")}
                    </span>
                  </label>
                  <p id={`${inputId}-help`}>{item.help}</p>
                </div>
                <div className="admin-limit-control">
                  <div className="admin-limit-input">
                    <input
                      id={inputId}
                      type="number"
                      inputMode="numeric"
                      required
                      min={item.min}
                      max={item.max}
                      step={1}
                      value={inputs[item.field]}
                      aria-invalid={error ? true : undefined}
                      aria-describedby={[
                        `${inputId}-help`,
                        `${inputId}-unit`,
                        `${inputId}-saved`,
                        error ? `${inputId}-error` : undefined,
                      ]
                        .filter(Boolean)
                        .join(" ")}
                      onChange={(e) => {
                        setInputs({ ...inputs, [item.field]: e.target.value });
                        setReloaded(false);
                      }}
                    />
                    <span id={`${inputId}-unit`} className="admin-limit-unit">
                      {translate("daysUnit")}
                    </span>
                  </div>
                  <p id={`${inputId}-saved`}>
                    {translate("savedValue", {
                      value0: saved
                        ? days(saved[item.field])
                        : translate("unknown"),
                    })}
                  </p>
                  {error && (
                    <p id={`${inputId}-error`} className="admin-field-error">
                      {error}
                    </p>
                  )}
                </div>
              </div>
            );
          })}
        </div>
        <div className="admin-limits-panel">
          <h3>{translate("beforeReducingRetention")}</h3>
          <p>{translate("recordsOlderThanTheNewLimitsBecome1")}</p>
        </div>
        {!review && conflictNotice}
        <div className="admin-limits-footer">
          <p className="admin-limits-status">{status}</p>
          <button
            ref={trigger}
            type="submit"
            className="admin-primary"
            disabled={
              action.pending ||
              reloading ||
              conflict ||
              !differs ||
              !valid ||
              !saved
            }
          >
            {reductions > 0
              ? translate("reviewReductions")
              : translate("saveChanges")}
          </button>
        </div>
      </form>
      {review && valid && saved && (
        <Confirm
          title={translate("reduceRetention")}
          pending={action.pending}
          pendingLabel={translate("message_23e39291d6")}
          onClose={() => setReview(false)}
          action={save}
        >
          <p>{translate("reviewTheseChangesBeforeSaving")}</p>
          <table className="admin-retention-table">
            <thead>
              <tr>
                <th scope="col">{translate("recordType")}</th>
                <th scope="col">{translate("saved")}</th>
                <th scope="col">{translate("new")}</th>
                <th scope="col">{translate("change")}</th>
              </tr>
            </thead>
            <tbody>
              {fields.map((item, index) => (
                <tr key={item.field}>
                  <th scope="row">{item.name}</th>
                  <td data-label={translate("saved")}>
                    {days(saved[item.field])}
                  </td>
                  <td data-label={translate("new")}>{days(values[index]!)}</td>
                  <td
                    data-label={translate("change")}
                    className={
                      changes[index] === RetentionChange.Reduced
                        ? "admin-retention-reduced"
                        : undefined
                    }
                  >
                    {changeLabel(changes[index])}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="admin-retention-warning">
            <p>{translate("recordsOlderThanTheNewLimitsBecome2")}</p>
          </div>
          <p>{translate("existingBackupsFollowOperatorPolicy")}</p>
          <ErrorNotice error={action.error} />
          {conflictNotice}
        </Confirm>
      )}
    </Card>
  );
}
