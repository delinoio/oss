// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import { useMutation } from "@connectrpc/connect-query";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  AllowedDestinationSchema,
  InstanceSettingsSchema,
  OperatorService,
  type AllowedDestination,
  type InstanceSettings,
} from "@aiprouter/client";
import { t as translate, useLocale } from "../i18n";
import { Card } from "../ui";
import { useDirtyForm } from "../api";
import { Confirm, ConfirmTone, ErrorNotice, useAction } from "./common";

// Derived state of the policy draft. It is computed from the reviewed snapshot,
// the draft and the editor inputs; it is never stored as a server fact.
// Values are stable identifiers; display text comes from the message catalog.
enum PolicyDraftState {
  Matches = "matches",
  Unsaved = "unsaved",
  EditorInProgress = "editor-in-progress",
}
function policyStateLabel(state: PolicyDraftState) {
  return state === PolicyDraftState.Unsaved
    ? translate("unsavedChanges")
    : state === PolicyDraftState.EditorInProgress
      ? translate("destinationEditInProgress")
      : translate("matchesSavedPolicy");
}
// Which control receives focus after a local draft operation.
enum FocusTarget {
  EditorOrigin = "editor-origin",
  RecordHeading = "record-heading",
  RecordEdit = "record-edit",
  Card = "card",
}
interface FocusRequest {
  target: FocusTarget;
  index?: number;
  // Distinguishes repeated requests for the same target.
  nonce: number;
}

// Validation of the typed origin. Values are never rewritten to become valid.
export function validOrigin(value: string) {
  try {
    const u = new URL(value);
    return (
      ["http:", "https:"].includes(u.protocol) &&
      !u.username &&
      !u.password &&
      u.origin === value
    );
  } catch {
    return false;
  }
}

// CIDR text is tokenised on whitespace and commas. The server owns prefix
// validation and canonicalisation.
function parseNetworks(text: string) {
  return text.split(/[\s,]+/u).filter(Boolean);
}

function sameDestinations(
  a: readonly AllowedDestination[],
  b: readonly AllowedDestination[],
) {
  return (
    a.length === b.length &&
    a.every(
      (item, index) =>
        item.id === b[index]!.id &&
        item.origin === b[index]!.origin &&
        item.allowedCidrs.length === b[index]!.allowedCidrs.length &&
        item.allowedCidrs.every(
          (cidr, i) => cidr === b[index]!.allowedCidrs[i],
        ),
    )
  );
}

// Static policy explanation shown above the policy card on the page.
export function DestinationPolicyNotice() {
  useLocale();
  const id = useId();
  return (
    <section className="admin-destination-notice" aria-labelledby={id}>
      <h2 id={id}>{translate("publicHttpsIsAllowedByDefault")}</h2>
      <p>{translate("additionalApprovalsPermitPrecisePrivateOrLocal")}</p>
      <p>{translate("aNonemptyCidrListAppliesToEveryResolved")}</p>
    </section>
  );
}

// Change of a draft rule relative to the saved snapshot, matched by stable ID.
// Values are stable identifiers; display text comes from the message catalog.
enum RuleChange {
  Added = "added",
  Changed = "changed",
  Unchanged = "unchanged",
}
function ruleChangeLabel(change: RuleChange) {
  return change === RuleChange.Added
    ? translate("ruleAdded")
    : change === RuleChange.Changed
      ? translate("ruleChanged")
      : translate("unchanged");
}
interface ReviewRule {
  // Local render key only. New rules have empty server IDs.
  key: string;
  rule: AllowedDestination;
  change: RuleChange;
  saved?: AllowedDestination;
}
interface PolicyReview {
  result: ReviewRule[];
  removed: AllowedDestination[];
}

function sameCidrs(a: readonly string[], b: readonly string[]) {
  return a.length === b.length && a.every((cidr, i) => cidr === b[i]);
}

// Compares the complete draft with the saved snapshot by retained stable ID.
// Nothing here is sent to the server; it only labels the review.
export function reviewPolicy(
  saved: readonly AllowedDestination[],
  draft: readonly AllowedDestination[],
): PolicyReview {
  const savedById = new Map<string, AllowedDestination>();
  for (const rule of saved) if (rule.id) savedById.set(rule.id, rule);
  const retained = new Set<string>();
  const result = draft.map((rule, index): ReviewRule => {
    const previous = rule.id ? savedById.get(rule.id) : undefined;
    if (!previous) {
      return { key: `new:${index}`, rule, change: RuleChange.Added };
    }
    retained.add(rule.id);
    const changed =
      previous.origin !== rule.origin ||
      !sameCidrs(previous.allowedCidrs, rule.allowedCidrs);
    return {
      key: `saved:${rule.id}`,
      rule,
      change: changed ? RuleChange.Changed : RuleChange.Unchanged,
      saved: previous,
    };
  });
  return {
    result,
    removed: saved.filter((rule) => !rule.id || !retained.has(rule.id)),
  };
}

function CidrLines({ cidrs }: { cidrs: readonly string[] }) {
  return (
    <ul className="admin-cidr-list">
      {cidrs.map((cidr, i) => (
        <li key={`${cidr}:${i}`}>{cidr}</li>
      ))}
    </ul>
  );
}

// Complete resulting rule with its network scope.
function RuleScope({
  cidrs,
  listed = true,
}: {
  cidrs: readonly string[];
  // False when the After block already lists the proposed networks.
  listed?: boolean;
}) {
  useLocale();
  return cidrs.length > 0 ? (
    <>
      <p className="admin-destination-scope">
        {translate("restrictedNetworksHeading")}
      </p>
      {listed && <CidrLines cidrs={cidrs} />}
      <p>{translate("allResolvedAddressesMustMatchAddressesOutside")}</p>
    </>
  ) : (
    <p className="admin-destination-scope">
      {translate("exactOriginApprovalNoCidrNarrowing")}
    </p>
  );
}

function DestinationReview({
  saved,
  draft,
  pending,
  error,
  onClose,
  onSave,
}: {
  saved: readonly AllowedDestination[];
  draft: readonly AllowedDestination[];
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: () => void;
}) {
  useLocale();
  const ids = useId();
  const { result, removed } = reviewPolicy(saved, draft);
  const code = error ? ConnectError.from(error).code : undefined;
  return (
    <Confirm
      title={translate("saveDestinationPolicy")}
      tone={ConfirmTone.Primary}
      pending={pending}
      pendingLabel={translate("message_23e39291d6")}
      onClose={onClose}
      action={onSave}
      footerNote={translate("nothingChangesUntilTheServerConfirmsThe")}
    >
      <div className="admin-destination-review">
        <p>{translate("reviewTheCompletePolicyBeforeSaving")}</p>
        <p>
          <span className="admin-policy-state">
            {translate("draftNotSaved")}
          </span>
        </p>
        <section
          className="admin-destination-notice"
          aria-labelledby={`${ids}-default`}
        >
          <h3 id={`${ids}-default`}>
            {translate("defaultPolicyRemainsActive")}
          </h3>
          <p>{translate("publicHttpsIsAllowedByDefaultAnExplicit")}</p>
        </section>
        <section aria-labelledby={`${ids}-result`}>
          <h3 id={`${ids}-result`}>
            {translate("additionalDestinationsAfterSave")}
          </h3>
          {result.length === 0 ? (
            <p className="admin-destination-clear">
              {translate("noAdditionalDestinationsWillRemainApprovedPublic")}
            </p>
          ) : (
            <ul className="admin-destination-review-list">
              {result.map(({ key, rule, change, saved: previous }) => {
                const cleared =
                  !!previous &&
                  previous.allowedCidrs.length > 0 &&
                  rule.allowedCidrs.length === 0;
                const networksChanged =
                  !!previous &&
                  !sameCidrs(previous.allowedCidrs, rule.allowedCidrs);
                return (
                  <li key={key} data-change={change}>
                    <p className="admin-destination-origin">
                      <strong>{rule.origin}</strong>{" "}
                      <span className="admin-diff-badge">
                        {ruleChangeLabel(change)}
                      </span>
                    </p>
                    {previous && previous.origin !== rule.origin && (
                      <p>
                        {translate("previouslyValue", {
                          value0: previous.origin,
                        })}
                      </p>
                    )}
                    {networksChanged && previous ? (
                      <div className="admin-diff-blocks">
                        <div>
                          <p className="admin-destination-scope">
                            {translate("before")}
                          </p>
                          {previous.allowedCidrs.length > 0 ? (
                            <CidrLines cidrs={previous.allowedCidrs} />
                          ) : (
                            <p>{translate("noCidrNarrowingBare")}</p>
                          )}
                        </div>
                        <div>
                          <p className="admin-destination-scope">
                            {translate("after")}
                          </p>
                          {rule.allowedCidrs.length > 0 ? (
                            <CidrLines cidrs={rule.allowedCidrs} />
                          ) : (
                            <p>{translate("noCidrNarrowingBare")}</p>
                          )}
                        </div>
                      </div>
                    ) : null}
                    <RuleScope
                      cidrs={rule.allowedCidrs}
                      listed={!networksChanged}
                    />
                    {cleared && (
                      <p className="admin-destination-warning">
                        {translate(
                          "networkRestrictionsRemovedClearingPreviousNetworksBroadens",
                        )}
                      </p>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </section>
        {removed.length > 0 && (
          <section
            className="admin-destination-removals"
            aria-labelledby={`${ids}-removed`}
          >
            <h3 id={`${ids}-removed`}>{translate("approvalsToRemove")}</h3>
            <p>{translate("futureConnectionsThatRequireThisApprovalWill")}</p>
            <ul className="admin-destination-review-list">
              {removed.map((rule, index) => (
                <li key={`${rule.id}:${index}`}>
                  <p className="admin-destination-origin">
                    <strong>{rule.origin}</strong>{" "}
                    <span className="admin-diff-badge">
                      {translate("ruleRemoved")}
                    </span>
                  </p>
                  {rule.allowedCidrs.length > 0 ? (
                    <>
                      <p className="admin-destination-scope">
                        {translate("previousNetworks")}
                      </p>
                      <CidrLines cidrs={rule.allowedCidrs} />
                      {rule.origin.startsWith("https://") && (
                        <p>
                          {translate("removingACidrRestrictionOnAPublicHttps")}
                        </p>
                      )}
                    </>
                  ) : (
                    <p className="admin-destination-scope">
                      {translate(
                        "previousScopeExactOriginApprovalNoCidrNarrowing",
                      )}
                    </p>
                  )}
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
      <p role="status" className="admin-destination-pending">
        {pending ? translate("message_238b2274b1") : ""}
      </p>
      <ErrorNotice error={error} />
      {code === Code.InvalidArgument && (
        <p>{translate("checkTheExactOriginsAndCidrPrefixes")}</p>
      )}
      {code === Code.Aborted && (
        <p>{translate("yourDraftAndNetworkEntriesArePreservedWhile")}</p>
      )}
    </Confirm>
  );
}

export function DestinationPolicyForm({
  settings: initialSettings,
}: {
  settings: InstanceSettings;
}) {
  useLocale();
  // Keep the reviewed snapshot across background refetches. A new revision
  // must not silently replace safe edits or authorize overwriting newer policy.
  const [settings, setSettings] = useState(initialSettings);
  const [draft, setDraft] = useState(() =>
    create(InstanceSettingsSchema, initialSettings),
  );
  const [review, setReview] = useState(false);
  const [origin, setOrigin] = useState("");
  const [networks, setNetworks] = useState("");
  const [editingDestination, setEditingDestination] = useState<number>();
  const [status, setStatus] = useState("");
  const [focusRequest, setFocusRequest] = useState<FocusRequest>();
  const ids = useId();
  const originRef = useRef<HTMLInputElement>(null);
  const editorRef = useRef<HTMLDivElement>(null);
  const cardRef = useRef<HTMLHeadingElement>(null);
  const headings = useRef(new Map<number, HTMLHeadingElement>());
  const editButtons = useRef(new Map<number, HTMLButtonElement>());

  const destinationInputDirty =
    !!origin || !!networks || editingDestination !== undefined;
  const draftChanged = !sameDestinations(
    draft.allowedDestinations,
    settings.allowedDestinations,
  );
  const policyState = draftChanged
    ? PolicyDraftState.Unsaved
    : destinationInputDirty
      ? PolicyDraftState.EditorInProgress
      : PolicyDraftState.Matches;
  const dirty = useDirtyForm();
  useEffect(() => {
    dirty(draftChanged || destinationInputDirty);
    return () => dirty(false);
  }, [dirty, draftChanged, destinationInputDirty]);

  // Apply focus after the render that makes the target available.
  useEffect(() => {
    if (!focusRequest) return;
    const { target, index = -1 } = focusRequest;
    const element =
      target === FocusTarget.EditorOrigin
        ? originRef.current
        : target === FocusTarget.RecordHeading
          ? headings.current.get(index)
          : target === FocusTarget.RecordEdit
            ? editButtons.current.get(index)
            : cardRef.current;
    if (target === FocusTarget.EditorOrigin)
      editorRef.current?.scrollIntoView?.({ block: "nearest" });
    element?.focus();
  }, [focusRequest]);
  function focusOn(target: FocusTarget, index?: number) {
    setFocusRequest((previous) => ({
      target,
      index,
      nonce: (previous?.nonce ?? 0) + 1,
    }));
  }

  const action = useAction(true);
  const update = useMutation(OperatorService.method.updateInstanceSettings);
  const canReview =
    !action.pending &&
    draftChanged &&
    !destinationInputDirty &&
    !!draft.requestLimits &&
    !!draft.retention;
  function save() {
    void action.run(
      async () => {
        const response = await update.mutateAsync({
          settings: draft,
          expectedRevision: settings.revision,
        });
        if (response.settings) {
          setSettings(response.settings);
          setDraft(create(InstanceSettingsSchema, response.settings));
        }
      },
      {
        done: () => {
          setReview(false);
          setStatus("");
        },
      },
    );
  }
  // Enter in any field must not bypass the disabled review boundary.
  function submit() {
    if (!canReview) return;
    setReview(true);
  }

  const originValid = validOrigin(origin);
  const duplicate = draft.allowedDestinations.some(
    (d, index) => index !== editingDestination && d.origin === origin,
  );
  const originError = !origin
    ? undefined
    : !originValid
      ? translate("enterAnExactHttpOrHttpsOrigin")
      : duplicate
        ? translate("thisOriginIsAlreadyInTheDraft")
        : undefined;
  const originId = `${ids}-origin`;
  const networksId = `${ids}-networks`;
  const editorHeadingId = `${ids}-editor`;
  const reviewHelpId = `${ids}-review-help`;

  function clearEditor() {
    setOrigin("");
    setNetworks("");
    setEditingDestination(undefined);
  }
  function stage() {
    if (!originValid || duplicate) return;
    // Preserve the stable rule ID and any future fields when editing.
    // Prefix canonicalization, duplicates and mapped addresses are validated by the server.
    const destination = {
      ...create(
        AllowedDestinationSchema,
        editingDestination === undefined
          ? undefined
          : draft.allowedDestinations[editingDestination],
      ),
      origin,
      allowedCidrs: parseNetworks(networks),
    };
    const index =
      editingDestination === undefined
        ? draft.allowedDestinations.length
        : editingDestination;
    setDraft({
      ...draft,
      allowedDestinations:
        editingDestination === undefined
          ? [...draft.allowedDestinations, destination]
          : draft.allowedDestinations.map((d, i) =>
              i === editingDestination ? destination : d,
            ),
    });
    setStatus(
      translate(
        editingDestination === undefined
          ? "addedValueInTheDraftNotSaved"
          : "updatedValueInTheDraftNotSaved",
        { value0: origin },
      ),
    );
    clearEditor();
    focusOn(FocusTarget.RecordHeading, index);
  }

  return (
    <Card className="admin-destinations-card">
      {action.feedback}
      <p role="status" className="admin-visually-hidden">
        {status}
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <div className="admin-policy-heading">
          <h2 ref={cardRef} tabIndex={-1}>
            {translate("policyDraft")}
          </h2>
          <span className="admin-policy-state" data-state={policyState}>
            {policyStateLabel(policyState)}
          </span>
        </div>
        <p className="admin-policy-helper">
          {translate("changesTakeEffectOnlyAfterReviewAnd")}
        </p>
        {draft.allowedDestinations.length > 0 ? (
          <ul className="admin-records admin-destination-list">
            {draft.allowedDestinations.map((destination, index) => (
              <li className="admin-record" key={`${destination.id}:${index}`}>
                <div>
                  <h3
                    tabIndex={-1}
                    ref={(node) => {
                      if (node) headings.current.set(index, node);
                      else headings.current.delete(index);
                    }}
                  >
                    {destination.origin}
                  </h3>
                  {destination.allowedCidrs.length > 0 ? (
                    <>
                      <p className="admin-destination-scope">
                        {translate("restrictedNetworksHeading")}
                      </p>
                      <ul className="admin-cidr-list">
                        {destination.allowedCidrs.map((cidr, i) => (
                          <li key={`${cidr}:${i}`}>{cidr}</li>
                        ))}
                      </ul>
                    </>
                  ) : (
                    <>
                      <p className="admin-destination-scope">
                        {translate("exactOriginApproval")}
                      </p>
                      <p>{translate("noCidrNarrowing")}</p>
                    </>
                  )}
                </div>
                <div className="admin-actions">
                  <button
                    type="button"
                    disabled={destinationInputDirty}
                    aria-label={translate("editApprovalForValue", {
                      value0: destination.origin,
                    })}
                    ref={(node) => {
                      if (node) editButtons.current.set(index, node);
                      else editButtons.current.delete(index);
                    }}
                    onClick={() => {
                      setEditingDestination(index);
                      setOrigin(destination.origin);
                      setNetworks(destination.allowedCidrs.join("\n"));
                      setStatus("");
                      focusOn(FocusTarget.EditorOrigin);
                    }}
                  >
                    {translate("editApproval")}
                  </button>
                  <button
                    type="button"
                    disabled={destinationInputDirty}
                    aria-label={translate("removeApprovalForValue", {
                      value0: destination.origin,
                    })}
                    onClick={() => {
                      setDraft({
                        ...draft,
                        allowedDestinations: draft.allowedDestinations.filter(
                          (_, i) => i !== index,
                        ),
                      });
                      setStatus(
                        translate("removedValueFromTheDraftNotSaved", {
                          value0: destination.origin,
                        }),
                      );
                      focusOn(FocusTarget.Card);
                    }}
                  >
                    {translate("removeApproval")}
                  </button>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <div className="admin-empty">
            <p>{translate("noAdditionalDestinationsApproved")}</p>
            <p>{translate("publicHttpsRemainsAllowedByDefaultPrivateOr")}</p>
          </div>
        )}
        <div
          className="admin-destination-editor"
          ref={editorRef}
          role="group"
          aria-labelledby={editorHeadingId}
        >
          <h3 id={editorHeadingId}>
            {editingDestination === undefined
              ? translate("addDestination")
              : translate("editDestination")}
          </h3>
          {editingDestination !== undefined && (
            <p className="admin-destination-editing">
              {translate("editingValue", {
                value0:
                  draft.allowedDestinations[editingDestination]?.origin ?? "",
              })}
            </p>
          )}
          <div className="admin-destination-field">
            <label htmlFor={originId}>{translate("exactOrigin")}</label>
            <input
              id={originId}
              ref={originRef}
              type="url"
              value={origin}
              placeholder="http://localhost:8080"
              aria-invalid={originError ? true : undefined}
              aria-describedby={[
                `${originId}-help`,
                originError ? `${originId}-error` : undefined,
              ]
                .filter(Boolean)
                .join(" ")}
              onChange={(e) => setOrigin(e.target.value)}
            />
            <p id={`${originId}-help`}>
              {translate("schemeHostnameAndPortOnlyNoPath")}
            </p>
            {originError && (
              <p id={`${originId}-error`} className="admin-destination-error">
                {originError}
              </p>
            )}
          </div>
          <div className="admin-destination-field">
            <label htmlFor={networksId}>
              {translate("allowedNetworksOptional")}
            </label>
            <textarea
              id={networksId}
              rows={3}
              value={networks}
              placeholder="10.0.0.0/8&#10;2001:db8::/32"
              onChange={(e) => setNetworks(e.target.value)}
              aria-describedby={`${networksId}-help ${networksId}-note`}
            />
            <p id={`${networksId}-help`}>
              {translate("cidrRangesOnePerLineOrSeparatedBy")}
            </p>
            <p id={`${networksId}-note`}>
              {translate(
                "leavingNetworksEmptyRemovesNetworkNarrowingIncluding",
              )}
            </p>
          </div>
          <div className="admin-actions">
            <button
              type="button"
              className="admin-primary"
              disabled={!originValid || duplicate}
              onClick={stage}
            >
              {editingDestination === undefined
                ? translate("addToReview")
                : translate("updateReview")}
            </button>
            {destinationInputDirty && (
              <button
                type="button"
                onClick={() => {
                  const index = editingDestination;
                  clearEditor();
                  setStatus(
                    translate("destinationEditCanceledTheDraftIsUnchanged"),
                  );
                  focusOn(
                    index === undefined
                      ? FocusTarget.EditorOrigin
                      : FocusTarget.RecordEdit,
                    index,
                  );
                }}
              >
                {translate("cancelDestinationEdit")}
              </button>
            )}
          </div>
          <p className="admin-destination-note">
            {translate("updatesTheDraftOnly")}
          </p>
        </div>
        <div className="admin-destination-footer">
          {destinationInputDirty && (
            <p id={reviewHelpId} className="admin-destination-note">
              {translate("finishOrCancelTheDestinationEditBefore")}
            </p>
          )}
          <button
            className="admin-primary"
            disabled={!canReview}
            aria-describedby={destinationInputDirty ? reviewHelpId : undefined}
          >
            {translate("reviewAndSaveChanges")}
          </button>
        </div>
      </form>
      {review && (
        <DestinationReview
          saved={settings.allowedDestinations}
          draft={draft.allowedDestinations}
          pending={action.pending}
          error={action.error}
          onClose={() => setReview(false)}
          onSave={save}
        />
      )}
    </Card>
  );
}
