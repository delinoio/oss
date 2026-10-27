// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useMutation } from "@connectrpc/connect-query";
import { create } from "@bufbuild/protobuf";
import {
  InstanceSettingsSchema,
  OperatorService,
  type InstanceSettings,
} from "@aiprouter/client";
import { Card } from "../ui";
import { useDirtyForm } from "../api";
import { t as translate, useLocale } from "../i18n";
import { Empty, useAction } from "./common";

// Stable identifiers for the two per-user request limit fields.
enum LimitField {
  RequestsPerMinute = "requestsPerMinute",
  ConcurrentRequests = "concurrentRequests",
}
type LimitInputs = Record<LimitField, string>;
type LimitErrors = Partial<Record<LimitField, string>>;

// Copy is resolved at render time so it follows the selected console language.
const limitFields = () =>
  [
    {
      field: LimitField.RequestsPerMinute,
      name: translate("requestsPerMinute"),
      help: translate("message16000RequestsPerMinuteDefault"),
      unit: translate("requestsMin"),
      min: 1,
      max: 6000,
      error: translate("enterAWholeNumberFrom1To6000"),
    },
    {
      field: LimitField.ConcurrentRequests,
      name: translate("concurrentRequests"),
      help: translate("message1100ConcurrentRequestsDefault10"),
      unit: translate("concurrentRequestsUnit"),
      min: 1,
      max: 100,
      error: translate("enterAWholeNumberFrom1To100"),
    },
  ] as const;

function toInputs(settings: InstanceSettings): LimitInputs {
  return {
    [LimitField.RequestsPerMinute]: String(
      settings.requestLimits?.requestsPerMinute ?? "",
    ),
    [LimitField.ConcurrentRequests]: String(
      settings.requestLimits?.concurrentRequests ?? "",
    ),
  };
}

// Text is parsed strictly so invalid input is reported instead of being
// coerced to zero or an unlimited value.
export function parseLimit(
  text: string,
  min: number,
  max: number,
): number | undefined {
  if (!/^\d+$/u.test(text.trim())) return undefined;
  const value = Number(text.trim());
  return Number.isSafeInteger(value) && value >= min && value <= max
    ? value
    : undefined;
}

export function RequestLimitsForm({
  settings: initialSettings,
}: {
  settings: InstanceSettings;
}) {
  useLocale();
  const fields = limitFields();
  // Keep the reviewed snapshot across background refetches. A newer revision
  // must not silently replace the draft or advance the reviewed revision.
  const [settings, setSettings] = useState(initialSettings);
  const [draft, setDraft] = useState(() =>
    create(InstanceSettingsSchema, initialSettings),
  );
  const [inputs, setInputs] = useState(() => toInputs(initialSettings));
  const [changed, setChanged] = useState(false);
  const [attempts, setAttempts] = useState(0);
  const dirty = useDirtyForm();
  useEffect(() => {
    dirty(changed);
    return () => dirty(false);
  }, [dirty, changed]);
  const action = useAction(true);
  const update = useMutation(OperatorService.method.updateInstanceSettings);
  const ids = useId();
  const summary = useRef<HTMLDivElement>(null);
  const available = !!draft.requestLimits;

  const errors: LimitErrors = {};
  if (attempts > 0)
    for (const item of fields)
      if (parseLimit(inputs[item.field], item.min, item.max) === undefined)
        errors[item.field] = item.error;
  const errorFields = fields.filter((item) => errors[item.field]);

  // Move focus to the error summary after each rejected submit attempt.
  useEffect(() => {
    if (attempts > 0) summary.current?.focus();
  }, [attempts]);

  function save(next: InstanceSettings) {
    void action.run(
      async () => {
        const response = await update.mutateAsync({
          settings: next,
          expectedRevision: settings.revision,
        });
        if (response.settings) {
          setSettings(response.settings);
          setDraft(create(InstanceSettingsSchema, response.settings));
          setInputs(toInputs(response.settings));
        }
      },
      { done: () => setChanged(false) },
    );
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    if (action.pending || !draft.requestLimits) return;
    const values = fields.map((item) =>
      parseLimit(inputs[item.field], item.min, item.max),
    );
    if (values.some((value) => value === undefined)) {
      setAttempts((count) => count + 1);
      return;
    }
    // Send the complete reviewed settings; only the two limits are replaced.
    const next = create(InstanceSettingsSchema, {
      ...draft,
      requestLimits: {
        ...draft.requestLimits,
        requestsPerMinute: values[0]!,
        concurrentRequests: values[1]!,
      },
    });
    setDraft(next);
    setAttempts(0);
    save(next);
  }

  const footer = (
    <div className="admin-limits-footer">
      <p className="admin-limits-status">
        {changed ? translate("unsavedChanges") : translate("noUnsavedChanges")}
      </p>
      <button
        type="submit"
        className="admin-primary"
        disabled={action.pending || !changed || !available}
      >
        {translate("saveChanges")}
      </button>
    </div>
  );

  return (
    <Card className="admin-limits-card">
      {action.feedback}
      <h2>{translate("perUserLimits")}</h2>
      <p className="admin-limits-description">
        {translate("setTheRequestRateAndConcurrencyAllowed")}
      </p>
      {!available ? (
        <Empty>{translate("instanceSettingsAreUnavailable")}</Empty>
      ) : (
        <form
          noValidate
          onSubmit={submit}
          aria-label={translate("perUserLimits")}
        >
          {errorFields.length > 0 && (
            <div
              ref={summary}
              role="alert"
              tabIndex={-1}
              className="admin-error admin-limits-summary"
            >
              <p>
                <strong>{translate("fixTheseLimitsBeforeSaving")}</strong>
              </p>
              <ul>
                {errorFields.map((item) => (
                  <li key={item.field}>
                    <a
                      href={`#${ids}-${item.field}`}
                      onClick={(event) => {
                        event.preventDefault();
                        document
                          .getElementById(`${ids}-${item.field}`)
                          ?.focus();
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
            {fields.map((item) => {
              const inputId = `${ids}-${item.field}`;
              const error = errors[item.field];
              return (
                <div className="admin-limit-row" key={item.field}>
                  <div className="admin-limit-copy">
                    <label htmlFor={inputId}>{item.name}</label>
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
                          error ? `${inputId}-error` : undefined,
                        ]
                          .filter(Boolean)
                          .join(" ")}
                        onChange={(e) => {
                          setInputs({
                            ...inputs,
                            [item.field]: e.target.value,
                          });
                          setChanged(true);
                        }}
                      />
                      <span id={`${inputId}-unit`} className="admin-limit-unit">
                        {item.unit}
                      </span>
                    </div>
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
            <h3>{translate("howTheseLimitsApply")}</h3>
            <p>{translate("eachUserHasASeparateAllowanceWithin")}</p>
          </div>
          {footer}
        </form>
      )}
      {!available && footer}
    </Card>
  );
}
