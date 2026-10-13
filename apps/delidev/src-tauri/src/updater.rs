// SPDX-License-Identifier: Apache-2.0
//! Closed native installation infrastructure. Go independently owns signed
//! release verification, immutable download and the durable once-only journal.
use std::ops::ControlFlow;
#[cfg(any(target_os = "macos", target_os = "windows"))]
use std::{thread, time::Instant};

use sha2::{Digest, Sha256};

use super::{
    updater_outcome::{OutcomeRecord, OutcomeStore, StoredEntry},
    *,
};

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum Action {
    Prepare,
    Install,
    Inspect,
}
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Phase {
    Prepared,
    Installing,
    Installed,
    Failed,
    Uncertain,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Prepared {
    version: u32,
    pub operation_id: String,
    pub server_id: String,
    pub generation: String,
    pub release_version: String,
    pub target: String,
    pub phase: Phase,
    artifact_path: PathBuf,
    artifact_sha256: String,
    artifact_size: u64,
    manifest_sha256: String,
}
#[derive(Serialize)]
pub struct PublicResult {
    pub operation_id: String,
    pub release_version: String,
    pub phase: Phase,
}
impl Prepared {
    pub fn public(&self) -> PublicResult {
        PublicResult {
            operation_id: self.operation_id.clone(),
            release_version: self.release_version.clone(),
            phase: self.phase,
        }
    }

    fn validate(&self, root: &Path, id: &str, server: &str, generation: &str) -> Result<()> {
        for value in [&self.operation_id, &self.server_id, &self.generation] {
            canonical_id(value)?;
        }
        let target = format!(
            "{}-{}",
            std::env::consts::OS,
            match std::env::consts::ARCH {
                "x86_64" => "amd64",
                "aarch64" => "arm64",
                _ => return Err(NativeFailure::Incompatible),
            }
        );
        let extension = match std::env::consts::OS {
            "macos" => ".dmg",
            "windows" => ".exe",
            "linux" => ".AppImage",
            _ => return Err(NativeFailure::Incompatible),
        };
        let target = target.replace("macos-", "darwin-");
        let digest = |v: &str| {
            v.len() == 64
                && v.bytes()
                    .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        };
        if self.artifact_size == 0
            || self.artifact_size > 2 << 30
            || self.version != 1
            || self.operation_id != id
            || self.server_id != server
            || self.generation != generation
            || self.target != target
            || !digest(&self.manifest_sha256)
            || !digest(&self.artifact_sha256)
            || self.artifact_path
                != root
                    .join("desktop-updates/downloads")
                    .join(format!("{}{}", self.artifact_sha256, extension))
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(())
    }
}
pub struct DesktopUpdateRequest<'a> {
    pub server: &'a str,
    pub id: &'a str,
    pub revision: u64,
    pub generation: &'a str,
    pub action: &'a str,
    pub outcome: Option<Phase>,
}

// Constructed only by a successful original native-begin, or by an offline
// inspection that proves a lost native-begin response was accepted. The
// renderer cannot supply or change this cleanup authority, including its
// selected saved scope.
pub struct AcceptedInstallation {
    prepared: Prepared,
    revision: u64,
    saved: Option<SavedConnection>,
    outcome: Mutex<Option<Phase>>,
    // Retained begin intent, retired only after the verified settlement.
    intent: Option<OutcomeRecord>,
    // True when ownership was recovered from an inspected journal. Such a
    // token can only settle the original journal; it never reaches an
    // installer.
    settlement_only: bool,
}

impl AcceptedInstallation {
    pub fn matches(&self, server: &str, id: &str, revision: u64) -> bool {
        self.prepared.server_id == server
            && self.prepared.operation_id == id
            && self.revision == revision
    }

    /// Whether this token was recovered from inspection and has no installer
    /// entry point.
    pub fn is_settlement_only(&self) -> bool {
        self.settlement_only
    }

    pub fn install(&self, connector: &Connector) -> Phase {
        self.install_with(connector.exiting.load(Ordering::Acquire), || {
            connector.install_desktop(&self.prepared)
        })
    }

    fn install_with(&self, stopping: bool, installer: impl FnOnce() -> Phase) -> Phase {
        let Ok(mut outcome) = self.outcome.lock() else {
            return Phase::Uncertain;
        };
        if let Some(phase) = *outcome {
            return phase;
        }
        let phase = if self.settlement_only {
            // The original process may already have run the installer, so a
            // recovered token retains uncertainty and never installs.
            Phase::Uncertain
        } else if stopping {
            Phase::Failed
        } else {
            // Catch at the effect boundary while the original outcome and
            // host pending guards remain valid. An installer panic proves no
            // safe terminal effect; retain uncertainty without replay.
            std::panic::catch_unwind(std::panic::AssertUnwindSafe(installer)).unwrap_or_else(|_| {
                tracing::error!(
                    operation = "desktop_update",
                    phase = "installer-panic",
                    state = "original-uncertain"
                );
                Phase::Uncertain
            })
        };
        *outcome = Some(phase);
        phase
    }

    pub fn settle(&self, connector: &Connector, phase: Phase) -> Result<Prepared> {
        terminal_name(phase)?;
        let mut original = self.outcome.lock().map_err(|_| NativeFailure::Busy)?;
        if original.is_some_and(|previous| previous != phase) {
            return Err(NativeFailure::InvalidEvidence);
        }
        *original = Some(phase);
        let p = &self.prepared;
        let settled = settle_original(
            connector,
            &OriginalIdentity {
                server_id: &p.server_id,
                operation_id: &p.operation_id,
                revision: self.revision,
                generation: &p.generation,
                saved_connection: self.saved.as_ref().map(|saved| saved.id.as_str()),
            },
            phase,
        )?;
        // The journal now holds a terminal phase, so the begin intent has no
        // remaining ownership to protect.
        if let Some(intent) = &self.intent
            && let Err(code) = connector.intent_store().retire(intent)
        {
            tracing::warn!(
                operation = "desktop_update_begin_intent",
                state = "retire-failed",
                id = %intent.operation_id,
                ?code
            );
        }
        Ok(settled)
    }

    /// The durable record for the exact outcome this installation observed.
    pub fn outcome_record(&self, phase: Phase) -> Result<OutcomeRecord> {
        let p = &self.prepared;
        OutcomeRecord::outcome(
            &p.server_id,
            &p.operation_id,
            self.revision,
            &p.generation,
            self.saved.as_ref().map(|saved| saved.id.as_str()),
            phase,
        )
    }
}

fn terminal_name(phase: Phase) -> Result<&'static str> {
    match phase {
        Phase::Installed => Ok("installed"),
        Phase::Failed => Ok("failed"),
        Phase::Uncertain => Ok("uncertain"),
        _ => Err(NativeFailure::InvalidInput),
    }
}

// Every identity the offline Go writer verifies against the original journal.
struct OriginalIdentity<'a> {
    server_id: &'a str,
    operation_id: &'a str,
    revision: u64,
    generation: &'a str,
    saved_connection: Option<&'a str>,
}

// Which offline original-journal command one request runs.
#[derive(Clone, Copy)]
enum OfflineCommand {
    // Writes one exact phase to the original Installing journal.
    Outcome(Phase),
    // Reads the original journal without changing it.
    Inspect,
}

// Writes one exact phase to the original Installing journal. It never invokes
// an installer and never changes the journal generation: Go accepts only the
// same phase or the first terminal write. The response must echo the same
// phase before any caller may retire retained evidence.
fn settle_original(
    connector: &Connector,
    original: &OriginalIdentity<'_>,
    phase: Phase,
) -> Result<Prepared> {
    offline_original(
        connector,
        original,
        OfflineCommand::Outcome(phase),
        Duration::from_secs(40),
    )
}

// Reads the exact original journal through the bounded offline writer. It
// never changes the journal and never repeats native-begin or an installer.
fn inspect_original(connector: &Connector, original: &OriginalIdentity<'_>) -> Result<Prepared> {
    offline_original(
        connector,
        original,
        OfflineCommand::Inspect,
        Duration::from_secs(10),
    )
}

fn offline_original(
    connector: &Connector,
    original: &OriginalIdentity<'_>,
    command: OfflineCommand,
    budget: Duration,
) -> Result<Prepared> {
    let (name, outcome) = match command {
        OfflineCommand::Outcome(phase) => ("native-outcome", Some((phase, terminal_name(phase)?))),
        OfflineCommand::Inspect => ("native-inspect", None),
    };
    let mut args: Vec<OsString> = vec![
        "update".into(),
        name.into(),
        "--id".into(),
        original.operation_id.into(),
        "--revision".into(),
        original.revision.to_string().into(),
        "--server-id".into(),
        original.server_id.into(),
        "--native-generation".into(),
        original.generation.into(),
    ];
    if let Some((_, outcome)) = outcome {
        args.extend(["--outcome".into(), outcome.into()]);
    }
    if let Some(saved) = original.saved_connection {
        args.extend(["--saved-connection".into(), saved.into()]);
    }
    // Retry only the exact same offline request. A lost atomic-write reply
    // grants neither another installer invocation nor a new claim.
    let started = std::time::Instant::now();
    let mut last = NativeFailure::TimedOut;
    for _ in 0..4 {
        let remaining = budget.saturating_sub(started.elapsed());
        if remaining.is_zero() {
            break;
        }
        let result = connector
            .short_request_with_input_mode(&args, None, remaining, false)
            .and_then(|value| {
                let result: Prepared =
                    serde_json::from_value(value).map_err(|_| NativeFailure::InvalidEvidence)?;
                // Inspection reports whichever generation the journal holds;
                // the caller decides whether that generation is the original.
                let generation = match command {
                    OfflineCommand::Inspect => result.generation.clone(),
                    OfflineCommand::Outcome(_) => original.generation.to_owned(),
                };
                result.validate(
                    &connector.root,
                    original.operation_id,
                    original.server_id,
                    &generation,
                )?;
                if let OfflineCommand::Outcome(phase) = command
                    && result.phase != phase
                {
                    return Err(NativeFailure::InvalidEvidence);
                }
                Ok(result)
            });
        match result {
            Ok(result) => return Ok(result),
            Err(code) => {
                tracing::warn!(
                    operation = "desktop_update_settlement",
                    command = name,
                    id = original.operation_id,
                    ?code
                );
                last = code;
                if !matches!(
                    code,
                    NativeFailure::Busy | NativeFailure::SidecarFailed | NativeFailure::TimedOut
                ) {
                    break;
                }
                std::thread::sleep(Duration::from_millis(25));
            }
        }
    }
    Err(last)
}

/// Settlement-only authority rebuilt from a validated retained record. It has
/// no installer entry point, so it can finish one original journal write but
/// can never authorize installation.
pub struct SettlementToken {
    record: OutcomeRecord,
}

impl SettlementToken {
    pub fn from_record(record: OutcomeRecord) -> Result<Self> {
        record.validate()?;
        Ok(Self { record })
    }

    pub fn record(&self) -> &OutcomeRecord {
        &self.record
    }

    pub fn settle(&self, connector: &Connector) -> Result<Prepared> {
        let r = &self.record;
        settle_original(
            connector,
            &OriginalIdentity {
                server_id: &r.server_id,
                operation_id: &r.operation_id,
                revision: r.revision,
                generation: &r.generation,
                saved_connection: r.saved_connection.as_deref(),
            },
            r.phase,
        )
    }
}

/// Which retained records one reconciliation pass may settle.
pub enum ReconcileScope<'a> {
    /// Every retained record, on application launch.
    Launch,
    /// Only the exact original server, operation, revision and saved scope.
    Original {
        server: &'a str,
        id: &'a str,
        revision: u64,
        saved_connection: Option<&'a str>,
    },
}

impl ReconcileScope<'_> {
    // Only the launch scope runs after the previous process, and with it the
    // resident runtime that carried any native-begin, has exited.
    fn is_quiesced(&self) -> bool {
        matches!(self, Self::Launch)
    }

    fn admits(&self, record: &OutcomeRecord) -> bool {
        match self {
            Self::Launch => true,
            Self::Original {
                server,
                id,
                revision,
                saved_connection,
            } => {
                record.server_id == *server
                    && record.operation_id == *id
                    && record.revision == *revision
                    && record.saved_connection.as_deref() == *saved_connection
            }
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct ReconcileReport {
    pub settled: usize,
    pub retained: usize,
    pub skipped: usize,
}

impl Connector {
    pub fn outcome_store(&self) -> OutcomeStore {
        OutcomeStore::new(&self.root)
    }

    /// Replays retained exact outcomes into their original Installing
    /// journals. A record is retired only after the settle response echoes the
    /// recorded phase for the same identities; corrupt, foreign or rejected
    /// evidence is kept and reported as retained uncertainty.
    pub fn reconcile_retained_outcomes(
        &self,
        scope: &ReconcileScope<'_>,
        stopping: &dyn Fn() -> bool,
    ) -> ReconcileReport {
        let store = self.outcome_store();
        let mut report = ReconcileReport::default();
        let mut visit = |entry: StoredEntry| {
            if stopping() {
                tracing::info!(operation = "desktop_update_reconcile", state = "stopped");
                return ControlFlow::Break(());
            }
            match entry {
                StoredEntry::Unreadable(name) => {
                    report.retained += 1;
                    tracing::warn!(
                        operation = "desktop_update_reconcile",
                        state = "evidence-unreadable-retained",
                        id = name.as_deref().unwrap_or("invalid-name")
                    );
                }
                StoredEntry::Record(record) if !scope.admits(&record) => report.skipped += 1,
                StoredEntry::Record(record) => {
                    let id = record.operation_id.clone();
                    let phase = record.phase;
                    let Ok(token) = SettlementToken::from_record(record) else {
                        report.retained += 1;
                        return ControlFlow::Continue(());
                    };
                    match token.settle(self) {
                        Ok(_) => match store.retire(token.record()) {
                            Ok(()) => {
                                report.settled += 1;
                                tracing::info!(
                                    operation = "desktop_update_reconcile",
                                    state = "settled",
                                    id,
                                    ?phase
                                );
                            }
                            Err(code) => {
                                report.settled += 1;
                                tracing::warn!(
                                    operation = "desktop_update_reconcile",
                                    state = "retire-failed",
                                    id,
                                    ?code
                                );
                            }
                        },
                        Err(code) => {
                            report.retained += 1;
                            tracing::warn!(
                                operation = "desktop_update_reconcile",
                                state = "settlement-uncertain-retained",
                                id,
                                ?phase,
                                ?code
                            );
                        }
                    }
                }
            }
            ControlFlow::Continue(())
        };
        // The launch scope streams every entry in one directory pass, so
        // retained records that stay unresolved never hide later ones and
        // Quit is honored between entries. An exact original-scope inspection
        // reads only its own operation's record, so unrelated evidence cannot
        // starve it either.
        let walked = match scope {
            ReconcileScope::Launch => store.for_each(None, &mut visit),
            ReconcileScope::Original { id, .. } => store.load(id).map(|entry| {
                if let Some(entry) = entry {
                    let _ = visit(entry);
                }
            }),
        };
        if let Err(code) = walked {
            tracing::warn!(
                operation = "desktop_update_reconcile",
                state = "scan-failed",
                ?code
            );
        }
        report
    }

    pub fn intent_store(&self) -> OutcomeStore {
        OutcomeStore::intents(&self.root)
    }

    /// Submits the original native-begin after durably retaining its identity.
    /// A canceled or lost response never discards ownership: the exact
    /// original journal is read through the bounded offline inspection and
    /// native-begin is never submitted a second time.
    pub fn begin_desktop_installation(
        &self,
        expected: Option<&SavedConnection>,
        mut request: DesktopUpdateRequest<'_>,
    ) -> BeginOutcome {
        request.action = "native-begin";
        request.outcome = None;
        let revision = request.revision;
        let intent = match OutcomeRecord::begin_intent(
            request.server,
            request.id,
            revision,
            request.generation,
            expected.map(|saved| saved.id.as_str()),
        ) {
            Ok(intent) => intent,
            Err(code) => return BeginOutcome::Rejected(code),
        };
        let store = self.intent_store();
        // An intent for this exact request that is still retained means an
        // earlier native-begin was canceled or lost and has not been
        // reconciled. Its resident runtime request may still land, so the
        // request must not be submitted a second time: a retry would race the
        // original. Re-inspect the original journal instead; the begin stays
        // fenced until that proof resolves the retained intent.
        match store.find(&intent) {
            Ok(None) => {}
            Ok(Some(StoredEntry::Record(existing))) if existing == intent => {
                tracing::warn!(
                    operation = "desktop_update_begin_intent",
                    state = "fenced-by-retained-intent",
                    id = %intent.operation_id
                );
                return self.resolve_lost_begin(
                    &store,
                    intent,
                    expected,
                    revision,
                    NativeFailure::Busy,
                );
            }
            Ok(Some(_)) => return BeginOutcome::Rejected(NativeFailure::InvalidEvidence),
            Err(code) => return BeginOutcome::Rejected(code),
        }
        // Fail closed: a begin whose identity cannot be retained is never
        // submitted.
        if let Err(code) = store.persist(&intent) {
            tracing::error!(
                operation = "desktop_update_begin_intent",
                state = "persist-failed",
                id = %intent.operation_id,
                ?code
            );
            return BeginOutcome::Rejected(code);
        }
        let sent = AtomicBool::new(false);
        let cause = match self.desktop_update_tracked(expected, request, &sent) {
            Ok(prepared) if prepared.phase == Phase::Installing => {
                return BeginOutcome::Accepted(AcceptedInstallation {
                    prepared,
                    revision,
                    saved: expected.cloned(),
                    outcome: Mutex::new(None),
                    intent: Some(intent),
                    settlement_only: false,
                });
            }
            Ok(_) => NativeFailure::InvalidEvidence,
            Err(code) => code,
        };
        if !sent.load(Ordering::Acquire) {
            // The request was rejected before it reached the runtime, so the
            // journal cannot have changed.
            self.retire_intent(&store, &intent, "not-sent");
            return BeginOutcome::Rejected(cause);
        }
        tracing::warn!(
            operation = "desktop_update_begin_intent",
            state = "response-lost",
            id = %intent.operation_id,
            ?cause
        );
        self.resolve_lost_begin(&store, intent, expected, revision, cause)
    }

    // Classifies a begin whose acceptance is unknown. The resident runtime
    // that carried the request may still be alive in this process, so a
    // Prepared journal cannot yet prove non-acceptance.
    fn resolve_lost_begin(
        &self,
        store: &OutcomeStore,
        intent: OutcomeRecord,
        expected: Option<&SavedConnection>,
        revision: u64,
        cause: NativeFailure,
    ) -> BeginOutcome {
        match self.prove_begin(&intent, false) {
            BeginProof::Installing(prepared) => {
                tracing::warn!(
                    operation = "desktop_update_begin_intent",
                    state = "accepted-recovered",
                    id = %intent.operation_id
                );
                BeginOutcome::Recovered {
                    installation: AcceptedInstallation {
                        prepared: *prepared,
                        revision,
                        saved: expected.cloned(),
                        outcome: Mutex::new(None),
                        intent: Some(intent),
                        settlement_only: true,
                    },
                    cause,
                }
            }
            BeginProof::Terminal(phase) => {
                self.retire_intent(store, &intent, "already-settled");
                BeginOutcome::Settled { phase, cause }
            }
            BeginProof::NotAccepted => {
                self.retire_intent(store, &intent, "not-accepted");
                BeginOutcome::Rejected(cause)
            }
            BeginProof::Unproven(code) => {
                tracing::warn!(
                    operation = "desktop_update_begin_intent",
                    state = "uncertain-retained",
                    id = %intent.operation_id,
                    ?code
                );
                BeginOutcome::Unresolved(cause)
            }
        }
    }

    fn retire_intent(&self, store: &OutcomeStore, intent: &OutcomeRecord, reason: &str) {
        match store.retire(intent) {
            Ok(()) => tracing::info!(
                operation = "desktop_update_begin_intent",
                state = "retired",
                id = %intent.operation_id,
                reason
            ),
            Err(code) => tracing::warn!(
                operation = "desktop_update_begin_intent",
                state = "retire-failed",
                id = %intent.operation_id,
                ?code
            ),
        }
    }

    // Reads the exact original journal and classifies what the retained
    // intent can still mean. `quiesced` states that the runtime request that
    // carried native-begin can no longer land, which is the only way a
    // Prepared journal proves non-acceptance.
    fn prove_begin(&self, intent: &OutcomeRecord, quiesced: bool) -> BeginProof {
        let journal = match inspect_original(
            self,
            &OriginalIdentity {
                server_id: &intent.server_id,
                operation_id: &intent.operation_id,
                revision: intent.revision,
                generation: &intent.generation,
                saved_connection: intent.saved_connection.as_deref(),
            },
        ) {
            Ok(journal) => journal,
            Err(code) => return BeginProof::Unproven(code),
        };
        if journal.generation != intent.generation {
            // native-begin only accepts the generation it carries, so a
            // journal owned by another generation can never be this begin.
            return BeginProof::NotAccepted;
        }
        match journal.phase {
            Phase::Installing => BeginProof::Installing(Box::new(journal)),
            Phase::Installed | Phase::Failed | Phase::Uncertain => {
                BeginProof::Terminal(journal.phase)
            }
            Phase::Prepared if quiesced => BeginProof::NotAccepted,
            Phase::Prepared => BeginProof::Unproven(NativeFailure::Busy),
        }
    }

    /// Resolves retained begin intents left by a canceled or lost response.
    /// An Installing journal proves acceptance and is settled as Uncertain
    /// through the offline writer without any installer call. A Prepared
    /// journal retires the intent only for the launch scope, where the process
    /// that carried the request has exited. Unreadable or foreign evidence is
    /// kept.
    pub fn reconcile_begin_intents(
        &self,
        scope: &ReconcileScope<'_>,
        stopping: &dyn Fn() -> bool,
    ) -> ReconcileReport {
        let store = self.intent_store();
        let outcomes = self.outcome_store();
        let mut report = ReconcileReport::default();
        let mut visit = |entry: StoredEntry| {
            if stopping() {
                tracing::info!(
                    operation = "desktop_update_reconcile_intents",
                    state = "stopped"
                );
                return ControlFlow::Break(());
            }
            let StoredEntry::Record(intent) = entry else {
                report.retained += 1;
                tracing::warn!(
                    operation = "desktop_update_reconcile_intents",
                    state = "evidence-unreadable-retained"
                );
                return ControlFlow::Continue(());
            };
            if !scope.admits(&intent) {
                report.skipped += 1;
                return ControlFlow::Continue(());
            }
            // A retained outcome is more exact than an inferred Uncertain, so
            // its operation must settle first. Look the exact record up rather
            // than scanning a bounded prefix, and fail closed when it cannot
            // be read.
            match outcomes.load(&intent.operation_id) {
                Ok(None) => {}
                Ok(Some(_)) | Err(_) => {
                    report.retained += 1;
                    tracing::warn!(
                        operation = "desktop_update_reconcile_intents",
                        state = "outcome-evidence-pending",
                        id = %intent.operation_id
                    );
                    return ControlFlow::Continue(());
                }
            }
            match self.prove_begin(&intent, scope.is_quiesced()) {
                BeginProof::Installing(_) => {
                    let identity = OriginalIdentity {
                        server_id: &intent.server_id,
                        operation_id: &intent.operation_id,
                        revision: intent.revision,
                        generation: &intent.generation,
                        saved_connection: intent.saved_connection.as_deref(),
                    };
                    match settle_original(self, &identity, Phase::Uncertain) {
                        Ok(_) => {
                            report.settled += 1;
                            self.retire_intent(&store, &intent, "settled-uncertain");
                        }
                        Err(code) => {
                            report.retained += 1;
                            tracing::warn!(
                                operation = "desktop_update_reconcile_intents",
                                state = "settlement-uncertain-retained",
                                id = %intent.operation_id,
                                ?code
                            );
                        }
                    }
                }
                BeginProof::Terminal(_) => {
                    report.settled += 1;
                    self.retire_intent(&store, &intent, "already-settled");
                }
                BeginProof::NotAccepted => {
                    report.settled += 1;
                    self.retire_intent(&store, &intent, "not-accepted");
                }
                BeginProof::Unproven(code) => {
                    report.retained += 1;
                    tracing::warn!(
                        operation = "desktop_update_reconcile_intents",
                        state = "uncertain-retained",
                        id = %intent.operation_id,
                        ?code
                    );
                }
            }
            ControlFlow::Continue(())
        };
        // The launch scope walks every page so intents that stay unresolved
        // never hide later ones; an original scope lists only the entries of
        // its own operation, so unrelated evidence cannot starve it.
        let walked = match scope {
            ReconcileScope::Launch => store.for_each(None, &mut visit),
            ReconcileScope::Original { id, .. } => store.for_each(Some(id), &mut visit),
        };
        if let Err(code) = walked {
            tracing::warn!(
                operation = "desktop_update_reconcile_intents",
                state = "scan-failed",
                ?code
            );
        }
        report
    }
}

/// What the original native-begin request can still mean.
pub enum BeginOutcome {
    /// The response arrived and the journal entered Installing.
    Accepted(AcceptedInstallation),
    /// The response was canceled or lost, but the inspected journal is
    /// Installing for the original generation. The settlement-only token owns
    /// the original journal and can never reach an installer.
    Recovered {
        installation: AcceptedInstallation,
        cause: NativeFailure,
    },
    /// The response was lost and the journal already holds a terminal phase.
    Settled { phase: Phase, cause: NativeFailure },
    /// The begin is positively known not to have been accepted.
    Rejected(NativeFailure),
    /// Acceptance can be neither proved nor disproved yet; the intent stays
    /// retained for reconciliation across Quit and restart.
    Unresolved(NativeFailure),
}

impl BeginOutcome {
    /// The failure to report when no installation is owned.
    pub fn failure(&self) -> Option<NativeFailure> {
        match self {
            Self::Accepted(_) => None,
            Self::Recovered { cause, .. } | Self::Settled { cause, .. } => Some(*cause),
            Self::Rejected(cause) | Self::Unresolved(cause) => Some(*cause),
        }
    }
}

enum BeginProof {
    Installing(Box<Prepared>),
    Terminal(Phase),
    NotAccepted,
    Unproven(NativeFailure),
}

impl Connector {
    pub fn desktop_update(
        &self,
        expected: Option<&SavedConnection>,
        request: DesktopUpdateRequest<'_>,
    ) -> Result<Prepared> {
        self.desktop_update_tracked(expected, request, &AtomicBool::new(false))
    }

    // `sent` turns true once the resident pipe accepted the request frame.
    // Until then a failure proves the journal was not touched.
    fn desktop_update_tracked(
        &self,
        expected: Option<&SavedConnection>,
        request: DesktopUpdateRequest<'_>,
        sent: &AtomicBool,
    ) -> Result<Prepared> {
        let DesktopUpdateRequest {
            server,
            id,
            revision,
            generation,
            action,
            outcome,
        } = request;
        canonical_id(server)?;
        canonical_id(id)?;
        canonical_id(generation)?;
        if revision == 0
            || revision >= 1 << 63
            || ![
                "native-prepare",
                "native-verify",
                "native-begin",
                "native-outcome",
                "native-inspect",
            ]
            .contains(&action)
        {
            return Err(NativeFailure::InvalidInput);
        }
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        if let Some(profile) = expected {
            self.check_saved_profile(profile)?;
            if profile.server_id != server {
                return Err(NativeFailure::InvalidEvidence);
            }
        }
        let mut args: Vec<OsString> = vec![
            "update".into(),
            action.into(),
            "--id".into(),
            id.into(),
            "--revision".into(),
            revision.to_string().into(),
            "--server-id".into(),
            server.into(),
            "--native-generation".into(),
            generation.into(),
        ];
        if let Some(profile) = expected {
            args.extend(["--saved-connection".into(), profile.id.as_str().into()]);
        }
        if let Some(phase) = outcome {
            args.extend([
                "--outcome".into(),
                match phase {
                    Phase::Installed => "installed",
                    Phase::Failed => "failed",
                    Phase::Uncertain => "uncertain",
                    _ => return Err(NativeFailure::InvalidInput),
                }
                .into(),
            ]);
        }
        // `sent` turns true only once the resident pipe accepted the frame. A
        // failure before that (no session, broken or exiting pipe, full
        // pending map, rejected frame) proves the request never left this
        // process, so its retained intent must not fence a retry.
        let value =
            self.resident_request_observed(&args, None, Duration::from_secs(660), &|| {
                sent.store(true, Ordering::Release)
            })?;
        let prepared: Prepared =
            serde_json::from_value(value).map_err(|_| NativeFailure::InvalidEvidence)?;
        prepared.validate(
            &self.root,
            id,
            server,
            if action == "native-inspect" {
                &prepared.generation
            } else {
                generation
            },
        )?;
        Ok(prepared)
    }

    pub fn install_desktop(&self, prepared: &Prepared) -> Phase {
        tracing::info!(
            operation = "desktop_update",
            phase = "installer-start",
            target = prepared.target
        );
        let result = match stage_verified(prepared) {
            Ok(staged) => install(&staged, &self.exiting),
            Err(_) => Phase::Failed,
        };
        tracing::info!(operation="desktop_update",phase=?result,target=prepared.target);
        result
    }
}

// Stream once from the opened original signed artifact into native-owned,
// exclusive private staging. Hash the exact bytes the OS installer consumes.
fn stage_verified(p: &Prepared) -> Result<Prepared> {
    if !no_symlink_ancestors(&p.artifact_path) {
        return Err(NativeFailure::InvalidEvidence);
    }
    let mut source = File::open(&p.artifact_path).map_err(|_| NativeFailure::StorageUnavailable)?;
    if source
        .metadata()
        .map_err(|_| NativeFailure::StorageUnavailable)?
        .len()
        != p.artifact_size
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    let root = p
        .artifact_path
        .parent()
        .and_then(|v| v.parent())
        .ok_or(NativeFailure::InvalidEvidence)?
        .join("native");
    let created = match fs::create_dir(&root) {
        Ok(()) => true,
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => false,
        Err(_) => return Err(NativeFailure::StorageUnavailable),
    };
    if !no_symlink_ancestors(&root) {
        return Err(NativeFailure::InvalidEvidence);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::{MetadataExt, PermissionsExt};
        let m = fs::metadata(&root).map_err(|_| NativeFailure::StorageUnavailable)?;
        let owner = fs::metadata(root.parent().ok_or(NativeFailure::InvalidEvidence)?)
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .uid();
        if m.uid() != owner {
            return Err(NativeFailure::PermissionDenied);
        }
        if m.permissions().mode() & 0o077 != 0 {
            if !created {
                return Err(NativeFailure::PermissionDenied);
            };
            fs::set_permissions(&root, fs::Permissions::from_mode(0o700))
                .map_err(|_| NativeFailure::StorageUnavailable)?;
        }
    }
    let extension = p
        .artifact_path
        .extension()
        .ok_or(NativeFailure::InvalidEvidence)?;
    let path = root.join(format!("{}.{}", p.generation, extension.to_string_lossy()));
    let mut options = fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut dest = options
        .open(&path)
        .map_err(|_| NativeFailure::StorageUnavailable)?;
    let mut hash = Sha256::new();
    let mut count = 0u64;
    let mut buffer = [0u8; 65536];
    loop {
        let n = source
            .read(&mut buffer)
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        if n == 0 {
            break;
        };
        count = count
            .checked_add(n as u64)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if count > p.artifact_size {
            return Err(NativeFailure::InvalidEvidence);
        };
        dest.write_all(&buffer[..n])
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        hash.update(&buffer[..n]);
    }
    if count != p.artifact_size
        || hash
            .finalize()
            .iter()
            .map(|v| format!("{v:02x}"))
            .collect::<String>()
            != p.artifact_sha256
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    dest.sync_all()
        .map_err(|_| NativeFailure::StorageUnavailable)?;
    let mut staged = p.clone();
    staged.artifact_path = path;
    Ok(staged)
}

// Commands and destinations are native-selected; no argument, environment or
// path from the renderer reaches an OS installer. Unknown completion is
// retained.
#[cfg(any(target_os = "macos", target_os = "windows"))]
fn command(program: &Path, args: &[OsString], exiting: &AtomicBool, seconds: u64) -> Result<()> {
    let mut cmd = Command::new(program);
    cmd.args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .env_clear();
    for key in [
        "HOME",
        "TMPDIR",
        "TEMP",
        "TMP",
        "SystemRoot",
        "WINDIR",
        "USERPROFILE",
        "LOCALAPPDATA",
        "APPDATA",
    ] {
        if let Some(value) = std::env::var_os(key) {
            cmd.env(key, value);
        }
    }
    #[cfg(unix)]
    cmd.env("PATH", "/usr/bin:/bin:/usr/sbin:/sbin");
    let mut child = cmd.spawn().map_err(|_| NativeFailure::SidecarFailed)?;
    let start = Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                return if status.success() {
                    Ok(())
                } else {
                    Err(NativeFailure::SidecarFailed)
                };
            }
            Ok(None)
                if !exiting.load(Ordering::Acquire)
                    && start.elapsed() < Duration::from_secs(seconds) =>
            {
                thread::sleep(Duration::from_millis(25))
            }
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(NativeFailure::TimedOut);
            }
        }
    }
}
#[cfg(any(target_os = "windows", target_os = "linux"))]
fn regular(path: &Path) -> bool {
    fs::symlink_metadata(path).is_ok_and(|m| m.is_file() && !m.file_type().is_symlink())
}
pub(crate) fn no_symlink_ancestors(path: &Path) -> bool {
    path.ancestors()
        .all(|p| fs::symlink_metadata(p).is_ok_and(|m| !m.file_type().is_symlink()))
}
#[cfg(target_os = "macos")]
fn exchange(a: &Path, b: &Path) -> Result<()> {
    use std::{ffi::CString, os::unix::ffi::OsStrExt};
    unsafe extern "C" {
        fn renamex_np(a: *const std::ffi::c_char, b: *const std::ffi::c_char, flags: u32) -> i32;
    }
    let a = CString::new(a.as_os_str().as_bytes()).map_err(|_| NativeFailure::InvalidInput)?;
    let b = CString::new(b.as_os_str().as_bytes()).map_err(|_| NativeFailure::InvalidInput)?;
    // SAFETY: live NUL-terminated sibling paths. macOS SDK RENAME_SWAP=2
    // atomically exchanges same-volume directories, preserving the old bundle.
    if unsafe { renamex_np(a.as_ptr(), b.as_ptr(), 2) } == 0 {
        Ok(())
    } else {
        Err(NativeFailure::StorageUnavailable)
    }
}
#[cfg(target_os = "macos")]
fn plist_is(path: &Path, key: &str, value: &str, exiting: &AtomicBool) -> bool {
    // PlistBuddy's output remains private, bounded and never logged. The fixed
    // key is compared inside the OS command without invoking a shell.
    let mut cmd = Command::new("/usr/libexec/PlistBuddy");
    cmd.args(["-c", &format!("Print :{key}")])
        .arg(path)
        .env_clear()
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
    let Ok(mut child) = cmd.spawn() else {
        return false;
    };
    let Some(out) = child.stdout.take() else {
        return false;
    };
    let reader = thread::spawn(move || read_bounded(out, 4096));
    let start = Instant::now();
    let success = loop {
        match child.try_wait() {
            Ok(Some(s)) => break s.success(),
            Ok(None)
                if !exiting.load(Ordering::Acquire)
                    && start.elapsed() < Duration::from_secs(10) =>
            {
                thread::sleep(Duration::from_millis(25))
            }
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                break false;
            }
        }
    };
    success
        && reader
            .join()
            .ok()
            .and_then(|v| v.ok())
            .is_some_and(|v| String::from_utf8(v).is_ok_and(|v| v.trim() == value))
}
#[cfg(target_os = "macos")]
fn install(p: &Prepared, exiting: &AtomicBool) -> Phase {
    let Ok(exe) = std::env::current_exe() else {
        return Phase::Failed;
    };
    let Some(bundle) = exe
        .ancestors()
        .find(|path| path.extension().is_some_and(|v| v == "app"))
    else {
        return Phase::Failed;
    };
    if exe != bundle.join("Contents/MacOS/delidev-desktop")
        || !no_symlink_ancestors(bundle)
        || !plist_is(
            &bundle.join("Contents/Info.plist"),
            "CFBundleIdentifier",
            "io.delino.delidev",
            exiting,
        )
    {
        return Phase::Failed;
    }
    let Some(parent) = bundle.parent() else {
        return Phase::Failed;
    };
    let backup = parent.join(format!(".DeliDev.previous-{}.app", p.operation_id));
    if fs::symlink_metadata(&backup).is_ok() {
        return Phase::Uncertain;
    }
    let mount = std::env::temp_dir().join(format!("delidev-update-{}", p.generation));
    if fs::create_dir(&mount).is_err() {
        return Phase::Failed;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if fs::set_permissions(&mount, fs::Permissions::from_mode(0o700)).is_err() {
            return Phase::Failed;
        }
    }
    let attach = command(
        Path::new("/usr/bin/hdiutil"),
        &[
            "attach".into(),
            "-readonly".into(),
            "-nobrowse".into(),
            "-noautoopen".into(),
            "-mountpoint".into(),
            mount.as_os_str().into(),
            p.artifact_path.as_os_str().into(),
        ],
        exiting,
        60,
    );
    let mut result = Phase::Failed;
    if attach.is_ok() {
        let app = mount.join("DeliDev.app");
        let plist = app.join("Contents/Info.plist");
        let valid = fs::symlink_metadata(&app)
            .is_ok_and(|m| m.is_dir() && !m.file_type().is_symlink())
            && plist_is(&plist, "CFBundleIdentifier", "io.delino.delidev", exiting)
            && plist_is(
                &plist,
                "CFBundleShortVersionString",
                &p.release_version,
                exiting,
            )
            && command(
                Path::new("/usr/bin/codesign"),
                &[
                    "--verify".into(),
                    "--deep".into(),
                    "--strict".into(),
                    app.as_os_str().into(),
                ],
                exiting,
                60,
            )
            .is_ok();
        if valid
            && command(
                Path::new("/usr/bin/ditto"),
                &[app.as_os_str().into(), backup.as_os_str().into()],
                exiting,
                120,
            )
            .is_ok()
            && !exiting.load(Ordering::Acquire)
            && exchange(bundle, &backup).is_ok()
        {
            // The old bundle remains at backup; live server/harness processes
            // are neither restarted nor overwritten by installation.
            result = if File::open(parent).and_then(|f| f.sync_all()).is_ok() {
                Phase::Installed
            } else {
                Phase::Uncertain
            };
        }
    } else {
        result = Phase::Uncertain
    }
    let cleanup = AtomicBool::new(false);
    if command(
        Path::new("/usr/bin/hdiutil"),
        &["detach".into(), mount.as_os_str().into()],
        &cleanup,
        30,
    )
    .is_err()
    {
        tracing::warn!(
            operation = "desktop_update",
            phase = "mount-cleanup",
            code = "unconfirmed"
        );
    }
    let _ = fs::remove_dir(&mount);
    result
}
#[cfg(target_os = "windows")]
fn install(p: &Prepared, exiting: &AtomicBool) -> Phase {
    if !regular(&p.artifact_path) {
        return Phase::Failed;
    }
    // NSIS can commit files before a nonzero/timeout result. Never claim an old
    // version rollback after invoking it; retain restart-only uncertainty.
    if command(&p.artifact_path, &[], exiting, 300).is_ok() {
        Phase::Installed
    } else {
        Phase::Uncertain
    }
}
#[cfg(target_os = "linux")]
fn install(p: &Prepared, exiting: &AtomicBool) -> Phase {
    use std::os::unix::fs::OpenOptionsExt;
    let Some(image) = std::env::var_os("APPIMAGE").map(PathBuf::from) else {
        return Phase::Failed;
    };
    let Some(dir) = std::env::var_os("APPDIR").map(PathBuf::from) else {
        return Phase::Failed;
    };
    let Ok(exe) = std::env::current_exe() else {
        return Phase::Failed;
    };
    if !image.is_absolute()
        || !dir.is_absolute()
        || !exe.starts_with(&dir)
        || !regular(&image)
        || !no_symlink_ancestors(&image)
    {
        return Phase::Failed;
    }
    // Correlate the mounted runtime with the actual backing AppImage through
    // the kernel mount inventory, in addition to the environment/executable.
    let Ok(mounts) = fs::read_to_string("/proc/self/mountinfo") else {
        return Phase::Failed;
    };
    if !appimage_mount_matches(&mounts, &dir, &image, &exe) {
        return Phase::Failed;
    }
    let Some(parent) = image.parent() else {
        return Phase::Failed;
    };
    let backup = parent.join(format!(".DeliDev.previous-{}.AppImage", p.operation_id));
    let staged = parent.join(format!(".DeliDev.pending-{}.AppImage", p.operation_id));
    if fs::symlink_metadata(&backup).is_ok() || fs::symlink_metadata(&staged).is_ok() {
        return Phase::Uncertain;
    }
    let copied = (|| -> std::io::Result<()> {
        let mut source = File::open(&p.artifact_path)?;
        let mut dest = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o700)
            .open(&staged)?;
        std::io::copy(&mut source, &mut dest)?;
        dest.sync_all()?;
        Ok(())
    })();
    if copied.is_err() || exiting.load(Ordering::Acquire) {
        let _ = fs::remove_file(&staged);
        return Phase::Failed;
    }
    if fs::hard_link(&image, &backup).is_err() {
        return Phase::Failed;
    }
    if File::open(parent).and_then(|f| f.sync_all()).is_err() {
        return Phase::Uncertain;
    }
    if fs::rename(&staged, &image).is_err() {
        return Phase::Failed;
    }
    if File::open(parent).and_then(|f| f.sync_all()).is_err() {
        return Phase::Uncertain;
    }
    Phase::Installed
}

#[cfg(any(target_os = "linux", test))]
fn decode_mountinfo_path(field: &str) -> Option<PathBuf> {
    let mut decoded = Vec::with_capacity(field.len());
    let mut bytes = field.bytes();
    while let Some(byte) = bytes.next() {
        // Linux seq_path_root/seq_escape encode these characters as octal;
        // mount sources also escape '#'. Decode once so a literal "\\040"
        // remains distinct from a space. Reject every other escape.
        let byte = if byte == b'\\' {
            match [bytes.next()?, bytes.next()?, bytes.next()?] {
                [b'0', b'4', b'0'] => b' ',
                [b'0', b'1', b'1'] => b'\t',
                [b'0', b'1', b'2'] => b'\n',
                [b'1', b'3', b'4'] => b'\\',
                [b'0', b'4', b'3'] => b'#',
                _ => return None,
            }
        } else if byte == 0 {
            return None;
        } else {
            byte
        };
        decoded.push(byte);
    }
    Some(PathBuf::from(String::from_utf8(decoded).ok()?))
}

#[cfg(any(target_os = "linux", all(test, unix)))]
fn appimage_mount_matches(mounts: &str, dir: &Path, image: &Path, exe: &Path) -> bool {
    if mounts.len() > 1 << 20 || !image.is_absolute() || !dir.is_absolute() || !exe.starts_with(dir)
    {
        return false;
    }
    mounts.lines().any(|line| {
        let Some((mount, source)) = line.split_once(" - ") else {
            return false;
        };
        mount
            .split_ascii_whitespace()
            .nth(4)
            .and_then(decode_mountinfo_path)
            .is_some_and(|path| path == dir)
            && source
                .split_ascii_whitespace()
                .nth(1)
                .and_then(decode_mountinfo_path)
                .is_some_and(|path| path == image)
    })
}

#[cfg(test)]
mod tests {
    use sha2::{Digest, Sha256};

    use super::*;

    #[cfg(unix)]
    #[test]
    fn original_outcomes_settle_offline_after_quit_and_contention_without_reinstallation() {
        use std::os::unix::fs::PermissionsExt;
        for (phase, installer_panic) in [
            (Phase::Installed, false),
            (Phase::Failed, false),
            (Phase::Uncertain, false),
            (Phase::Uncertain, true),
        ] {
            let temp = tempfile::tempdir().unwrap();
            let sidecar = temp.path().join("sidecar");
            fs::write(
                &sidecar,
                "#!/bin/sh\nprintf x >> \"$2/invocations\"\nif [ ! -e \"$2/written\" ]; then \
                 touch \"$2/written\"; printf \
                 '{\"version\":1,\"error\":{\"code\":\"unavailable\"}}'; exit 1; fi\nexec \
                 /bin/cat \"$2/response.json\"\n",
            )
            .unwrap();
            fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
            let connector = Connector::new(sidecar, temp.path().to_path_buf()).unwrap();
            let id = uuid::Uuid::now_v7().to_string();
            let server = uuid::Uuid::now_v7().to_string();
            let generation = uuid::Uuid::now_v7().to_string();
            let target = format!(
                "{}-{}",
                std::env::consts::OS,
                if cfg!(target_arch = "aarch64") {
                    "arm64"
                } else {
                    "amd64"
                }
            )
            .replace("macos-", "darwin-");
            let extension = if cfg!(target_os = "macos") {
                ".dmg"
            } else {
                ".AppImage"
            };
            let descriptor = serde_json::json!({"version":1,"operation_id":id,"server_id":server,"generation":generation,"release_version":"0.2.0","target":target,"phase":phase,"artifact_path":connector.root.join("desktop-updates/downloads").join(format!("{}{}","a".repeat(64),extension)),"artifact_sha256":"a".repeat(64),"artifact_size":8,"manifest_sha256":"b".repeat(64)});
            fs::write(
                temp.path().join("response.json"),
                serde_json::to_vec(&serde_json::json!({"version":1,"result":descriptor})).unwrap(),
            )
            .unwrap();
            let mut installing: Prepared = serde_json::from_value(descriptor).unwrap();
            installing.phase = Phase::Installing;
            let accepted = AcceptedInstallation {
                prepared: installing,
                revision: 7,
                saved: None,
                outcome: Mutex::new(None),
                intent: None,
                settlement_only: false,
            };
            let effects = std::sync::atomic::AtomicUsize::new(0);
            assert_eq!(
                accepted.install_with(false, || {
                    effects.fetch_add(1, Ordering::AcqRel);
                    if installer_panic {
                        panic!("original installer effect became uncertain");
                    }
                    phase
                }),
                phase
            );
            assert_eq!(
                accepted.install_with(false, || panic!("installation must not replay")),
                phase
            );
            assert_eq!(effects.load(Ordering::Acquire), 1);
            connector.exiting.store(true, Ordering::Release);
            let contention = connector.gate.lock().unwrap();
            assert_eq!(accepted.settle(&connector, phase).unwrap().phase, phase);
            assert_eq!(
                fs::read(temp.path().join("invocations")).unwrap(),
                b"xx",
                "lost reply retries only original offline writer"
            );
            assert_eq!(
                accepted.install(&connector),
                phase,
                "retained result forbids another installer effect"
            );
            assert!(matches!(
                accepted.settle(
                    &connector,
                    if phase == Phase::Failed {
                        Phase::Installed
                    } else {
                        Phase::Failed
                    }
                ),
                Err(NativeFailure::InvalidEvidence)
            ));
            assert_eq!(fs::read(temp.path().join("invocations")).unwrap(), b"xx");
            drop(contention);
            assert!(matches!(
                connector.desktop_update(
                    None,
                    DesktopUpdateRequest {
                        server: &server,
                        id: &id,
                        revision: 7,
                        generation: &generation,
                        action: "native-prepare",
                        outcome: None
                    }
                ),
                Err(NativeFailure::Stopped)
            ));
        }
    }

    #[test]
    fn mountinfo_decodes_kernel_path_escapes_once() {
        for (encoded, decoded) in [
            ("/home/user/DeliDev.AppImage", "/home/user/DeliDev.AppImage"),
            (
                r"/home/user/My\040Apps/DeliDev.AppImage",
                "/home/user/My Apps/DeliDev.AppImage",
            ),
            (
                r"/tmp/tab\011line\012slash\134hash\043",
                "/tmp/tab\tline\nslash\\hash#",
            ),
            (r"/tmp/literal\134040", r"/tmp/literal\040"),
            (
                "/home/Caf\u{e9}/DeliDev.AppImage",
                "/home/Caf\u{e9}/DeliDev.AppImage",
            ),
        ] {
            assert_eq!(decode_mountinfo_path(encoded), Some(PathBuf::from(decoded)));
        }
    }

    #[test]
    fn mountinfo_rejects_malformed_or_non_kernel_escapes() {
        for field in [
            "/tmp/trailing\\",
            r"/tmp/\0",
            r"/tmp/\04",
            r"/tmp/\08x",
            r"/tmp/\400",
            r"/tmp/\777",
            r"/tmp/\000",
            r"/tmp/\141",
            r"/tmp/\x20",
            "/tmp/nul\0",
        ] {
            assert!(decode_mountinfo_path(field).is_none());
        }
    }

    #[cfg(unix)]
    #[test]
    fn appimage_mount_matches_decoded_mount_and_source_in_the_same_record() {
        for (mount, source, dir, image) in [
            (
                "/tmp/.mount_DeliDev",
                "/home/user/DeliDev.AppImage",
                "/tmp/.mount_DeliDev",
                "/home/user/DeliDev.AppImage",
            ),
            (
                "/tmp/.mount_DeliDev",
                r"/home/user/My\040Apps/DeliDev.AppImage",
                "/tmp/.mount_DeliDev",
                "/home/user/My Apps/DeliDev.AppImage",
            ),
            (
                r"/tmp/mount\040with\011tab\012line\134slash",
                r"/home/user/tab\011line\012slash\134hash\043.AppImage",
                "/tmp/mount with\ttab\nline\\slash",
                "/home/user/tab\tline\nslash\\hash#.AppImage",
            ),
            (
                r"/tmp/literal\134040",
                r"/home/user/literal\134040.AppImage",
                r"/tmp/literal\040",
                r"/home/user/literal\040.AppImage",
            ),
        ] {
            let mounts =
                format!("36 35 0:42 / {mount} ro shared:7 unknown:1 - fuse.AppImage {source} ro\n");
            let dir = Path::new(dir);
            let image = Path::new(image);
            let exe = dir.join("usr/bin/delidev-desktop");
            assert!(appimage_mount_matches(&mounts, dir, image, &exe));
            assert!(!appimage_mount_matches(
                &mounts,
                Path::new("/tmp/other"),
                image,
                &exe
            ));
            assert!(!appimage_mount_matches(
                &mounts,
                dir,
                Path::new("/home/user/other.AppImage"),
                &exe
            ));
            assert!(!appimage_mount_matches(
                &mounts,
                dir,
                image,
                Path::new("/tmp/other/usr/bin/delidev-desktop")
            ));
        }
    }

    #[cfg(unix)]
    #[test]
    fn appimage_mount_rejects_invalid_inventory_and_unrelated_authority() {
        let dir = Path::new("/tmp/.mount_DeliDev");
        let image = Path::new("/home/user/My Apps/DeliDev.AppImage");
        let exe = dir.join("usr/bin/delidev-desktop");
        for mounts in [
            "",
            "36 35 0:42 / /tmp/.mount_DeliDev ro fuse.AppImage \
             /home/user/My\\040Apps/DeliDev.AppImage ro",
            "36 35 0:42 / /tmp/.mount_DeliDev ro - fuse.AppImage",
            "36 35 0:42 / /tmp/.mount_DeliDev\\04 ro - fuse.AppImage \
             /home/user/My\\040Apps/DeliDev.AppImage ro",
            "36 35 0:42 / /tmp/.mount_DeliDev ro - fuse.AppImage \
             /home/user/My\\04Apps/DeliDev.AppImage ro",
            "36 35 0:42 / /tmp/.mount_DeliDev ro - fuse.AppImage /home/user/other.AppImage ro\n37 \
             35 0:43 / /tmp/other ro - fuse.AppImage /home/user/My\\040Apps/DeliDev.AppImage ro",
        ] {
            assert!(!appimage_mount_matches(mounts, dir, image, &exe));
        }
        let mounts = "36 35 0:42 / /tmp/.mount_DeliDev ro - fuse.AppImage \
                      /home/user/My\\040Apps/DeliDev.AppImage ro\n";
        assert!(!appimage_mount_matches(
            mounts,
            dir,
            image,
            Path::new("/tmp/.mount_DeliDev-other/delidev-desktop")
        ));
        assert!(!appimage_mount_matches(
            mounts,
            Path::new("tmp/.mount_DeliDev"),
            image,
            &exe
        ));
        assert!(!appimage_mount_matches(
            mounts,
            dir,
            Path::new("DeliDev.AppImage"),
            &exe
        ));
        let oversized = format!("{mounts}{}", " ".repeat(1 << 20));
        assert!(!appimage_mount_matches(&oversized, dir, image, &exe));
    }

    #[test]
    fn descriptor_cannot_select_paths_or_another_authority() {
        let root = Path::new("/private/product");
        let id = uuid::Uuid::now_v7().to_string();
        let server = uuid::Uuid::now_v7().to_string();
        let generation = uuid::Uuid::now_v7().to_string();
        let target = format!(
            "{}-{}",
            std::env::consts::OS,
            if cfg!(target_arch = "aarch64") {
                "arm64"
            } else {
                "amd64"
            }
        )
        .replace("macos-", "darwin-");
        let ext = if cfg!(target_os = "macos") {
            ".dmg"
        } else if cfg!(windows) {
            ".exe"
        } else {
            ".AppImage"
        };
        let mut p = Prepared {
            version: 1,
            operation_id: id.clone(),
            server_id: server.clone(),
            generation: generation.clone(),
            release_version: "0.2.0".into(),
            target,
            phase: Phase::Prepared,
            artifact_path: root.join("desktop-updates/downloads").join(format!(
                "{}{}",
                "a".repeat(64),
                ext
            )),
            artifact_sha256: "a".repeat(64),
            artifact_size: 1,
            manifest_sha256: "b".repeat(64),
        };
        assert!(p.validate(root, &id, &server, &generation).is_ok());
        p.artifact_path = PathBuf::from("/tmp/external");
        assert!(p.validate(root, &id, &server, &generation).is_err());
        p.operation_id = uuid::Uuid::now_v7().to_string();
        assert!(p.validate(root, &id, &server, &generation).is_err());
    }
    #[test]
    fn native_staging_checks_actual_bytes_and_never_replaces_a_generation() {
        let root = tempfile::tempdir().unwrap();
        // macOS exposes the temporary directory through `/var`, which is a
        // symlink to `/private/var`. Use the physical path so this fixture
        // exercises the staging checks instead of rejecting the platform's
        // stable system alias as an untrusted ancestor.
        let root_path = root.path().canonicalize().unwrap();
        let downloads = root_path.join("downloads");
        fs::create_dir(&downloads).unwrap();
        let source = downloads.join("source.dmg");
        fs::write(&source, b"verified").unwrap();
        let p = Prepared {
            version: 1,
            operation_id: uuid::Uuid::now_v7().to_string(),
            server_id: uuid::Uuid::now_v7().to_string(),
            generation: uuid::Uuid::now_v7().to_string(),
            release_version: "0.2.0".into(),
            target: "darwin-arm64".into(),
            phase: Phase::Installing,
            artifact_path: source,
            artifact_size: 8,
            artifact_sha256: Sha256::digest(b"verified")
                .iter()
                .map(|v| format!("{v:02x}"))
                .collect::<String>(),
            manifest_sha256: "a".repeat(64),
        };
        let staged = stage_verified(&p).unwrap();
        assert_eq!(fs::read(&staged.artifact_path).unwrap(), b"verified");
        assert!(stage_verified(&p).is_err());
        let mut bad = p.clone();
        bad.generation = uuid::Uuid::now_v7().to_string();
        bad.artifact_sha256 = "b".repeat(64);
        assert!(stage_verified(&bad).is_err());
    }
    #[cfg(target_os = "macos")]
    #[test]
    fn exchange_preserves_both_native_generations() {
        let root = tempfile::tempdir().unwrap();
        let a = root.path().join("a");
        let b = root.path().join("b");
        fs::create_dir(&a).unwrap();
        fs::create_dir(&b).unwrap();
        fs::write(a.join("old"), b"old").unwrap();
        fs::write(b.join("new"), b"new").unwrap();
        exchange(&a, &b).unwrap();
        assert_eq!(fs::read(a.join("new")).unwrap(), b"new");
        assert_eq!(fs::read(b.join("old")).unwrap(), b"old");
        exchange(&a, &b).unwrap();
        assert!(a.join("old").exists());
    }
}

#[cfg(all(test, unix))]
mod retained_outcome_tests {
    use std::os::unix::fs::PermissionsExt;

    use super::*;

    const FIXTURE: &str = r#"#!/usr/bin/python3
import sys, json, os
root = sys.argv[2]
open(os.path.join(root, 'calls'), 'a').write('x')
args = sys.argv[3:]
def fail(code):
    print(json.dumps({'version': 1, 'error': {'code': code}}))
    sys.exit(1)
mode_path = os.path.join(root, 'mode')
mode = open(mode_path).read().strip() if os.path.exists(mode_path) else 'ok'
assert args[0:2] == ['update', 'native-outcome'], args
options = dict(zip(args[2::2], args[3::2]))
if mode == 'down':
    fail('unavailable')
journal_path = os.path.join(root, 'journal.json')
if not os.path.exists(journal_path):
    fail('recovery_required')
journal = json.load(open(journal_path))
if (options['--id'] != journal['operation_id']
        or options['--server-id'] != journal['server_id']
        or options['--revision'] != str(journal['revision'])
        or options['--native-generation'] != journal['generation']
        or options.get('--saved-connection') != journal.get('saved')):
    fail('recovery_required')
outcome = options['--outcome']
if journal['phase'] not in ('installing', outcome):
    fail('recovery_required')
journal['phase'] = outcome
json.dump(journal, open(journal_path, 'w'))
lost = os.path.join(root, 'lost-once')
if mode == 'lost-reply-once' and not os.path.exists(lost):
    open(lost, 'w').write('x')
    fail('unavailable')
descriptor = json.load(open(os.path.join(root, 'descriptor.json')))
descriptor['phase'] = outcome
print(json.dumps({'version': 1, 'result': descriptor}))
"#;

    struct Fixture {
        _temp: tempfile::TempDir,
        root: PathBuf,
        connector: Connector,
        server: String,
        id: String,
        generation: String,
        saved: Option<String>,
    }

    impl Fixture {
        fn new(saved: Option<&str>, journal: Option<&str>) -> Self {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().canonicalize().unwrap();
            let sidecar = root.join("sidecar");
            fs::write(&sidecar, FIXTURE).unwrap();
            fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
            fs::create_dir_all(root.join("desktop-updates")).unwrap();
            fs::set_permissions(
                root.join("desktop-updates"),
                fs::Permissions::from_mode(0o700),
            )
            .unwrap();
            let connector = Connector::new(sidecar, root.clone()).unwrap();
            let id = uuid::Uuid::now_v7().to_string();
            let server = uuid::Uuid::now_v7().to_string();
            let generation = uuid::Uuid::now_v7().to_string();
            let target = format!(
                "{}-{}",
                std::env::consts::OS,
                if cfg!(target_arch = "aarch64") {
                    "arm64"
                } else {
                    "amd64"
                }
            )
            .replace("macos-", "darwin-");
            let extension = if cfg!(target_os = "macos") {
                ".dmg"
            } else {
                ".AppImage"
            };
            let descriptor = serde_json::json!({"version":1,"operation_id":id,"server_id":server,"generation":generation,"release_version":"0.2.0","target":target,"phase":"installing","artifact_path":root.join("desktop-updates/downloads").join(format!("{}{}","a".repeat(64),extension)),"artifact_sha256":"a".repeat(64),"artifact_size":8,"manifest_sha256":"b".repeat(64)});
            fs::write(
                root.join("descriptor.json"),
                serde_json::to_vec(&descriptor).unwrap(),
            )
            .unwrap();
            if let Some(phase) = journal {
                fs::write(
                    root.join("journal.json"),
                    serde_json::to_vec(&serde_json::json!({
                        "operation_id": id, "server_id": server, "revision": 7,
                        "generation": generation, "saved": saved, "phase": phase,
                    }))
                    .unwrap(),
                )
                .unwrap();
            }
            Self {
                _temp: temp,
                root,
                connector,
                server,
                id,
                generation,
                saved: saved.map(str::to_owned),
            }
        }

        fn record(&self, phase: Phase) -> OutcomeRecord {
            OutcomeRecord::outcome(
                &self.server,
                &self.id,
                7,
                &self.generation,
                self.saved.as_deref(),
                phase,
            )
            .unwrap()
        }

        fn store(&self) -> OutcomeStore {
            self.connector.outcome_store()
        }

        fn calls(&self) -> usize {
            fs::read(self.root.join("calls"))
                .map(|v| v.len())
                .unwrap_or(0)
        }

        fn journal(&self) -> serde_json::Value {
            serde_json::from_slice(&fs::read(self.root.join("journal.json")).unwrap()).unwrap()
        }

        fn marker(&self, id: &str) -> PathBuf {
            self.root
                .join(crate::updater_outcome::STORE_DIRECTORY)
                .join(format!("{id}.json"))
        }

        fn launch(&self) -> ReconcileReport {
            self.connector
                .reconcile_retained_outcomes(&ReconcileScope::Launch, &|| false)
        }
    }

    #[test]
    fn each_phase_replays_once_into_the_original_journal_and_retires() {
        for (phase, name) in [
            (Phase::Installed, "installed"),
            (Phase::Failed, "failed"),
            (Phase::Uncertain, "uncertain"),
        ] {
            let saved = uuid::Uuid::now_v7().to_string();
            let fixture = Fixture::new(Some(&saved), Some("installing"));
            fixture.store().persist(&fixture.record(phase)).unwrap();
            let marker = fixture.marker(&fixture.id);
            let mode = fs::metadata(&marker).unwrap().permissions().mode() & 0o777;
            assert_eq!(mode, 0o600, "marker is owner-only");
            let report = fixture.launch();
            assert_eq!(
                report,
                ReconcileReport {
                    settled: 1,
                    retained: 0,
                    skipped: 0
                }
            );
            assert!(!marker.exists(), "retired after matching settlement");
            assert_eq!(fixture.journal()["phase"], name);
            assert_eq!(fixture.journal()["generation"], fixture.generation);
            assert_eq!(fixture.calls(), 1, "one offline write and no installer");
            assert_eq!(fixture.launch(), ReconcileReport::default());
            assert_eq!(fixture.calls(), 1, "a retired record is never replayed");
        }
    }

    #[test]
    fn local_scope_without_saved_connection_replays() {
        let fixture = Fixture::new(None, Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        assert_eq!(fixture.launch().settled, 1);
        assert_eq!(fixture.journal()["phase"], "installed");
    }

    #[test]
    fn lost_reply_retries_the_same_phase_and_retires() {
        let fixture = Fixture::new(None, Some("installing"));
        fs::write(fixture.root.join("mode"), "lost-reply-once").unwrap();
        fixture
            .store()
            .persist(&fixture.record(Phase::Failed))
            .unwrap();
        assert_eq!(fixture.launch().settled, 1);
        assert_eq!(fixture.calls(), 2, "second same-phase write sees the first");
        assert_eq!(fixture.journal()["phase"], "failed");
        assert!(!fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn failed_settlement_retains_the_record_until_a_later_pass_succeeds() {
        let fixture = Fixture::new(None, Some("installing"));
        fs::write(fixture.root.join("mode"), "down").unwrap();
        let record = fixture.record(Phase::Installed);
        fixture.store().persist(&record).unwrap();
        let report = fixture.launch();
        assert_eq!((report.settled, report.retained), (0, 1));
        assert!(fixture.marker(&fixture.id).exists());
        assert_eq!(fixture.journal()["phase"], "installing");
        assert_eq!(
            fixture.store().scan().unwrap(),
            vec![StoredEntry::Record(record)]
        );
        fs::write(fixture.root.join("mode"), "ok").unwrap();
        assert_eq!(fixture.launch().settled, 1);
        assert_eq!(fixture.journal()["phase"], "installed");
        assert!(!fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn foreign_identity_or_scope_is_rejected_and_retained() {
        let saved = uuid::Uuid::now_v7().to_string();
        let other = uuid::Uuid::now_v7().to_string();
        for variant in ["saved", "no-saved", "server", "generation", "revision"] {
            let fixture = Fixture::new(Some(&saved), Some("installing"));
            let mut record = fixture.record(Phase::Installed);
            match variant {
                "saved" => record.saved_connection = Some(other.clone()),
                "no-saved" => record.saved_connection = None,
                "server" => record.server_id = other.clone(),
                "generation" => record.generation = other.clone(),
                _ => record.revision = 8,
            }
            fixture.store().persist(&record).unwrap();
            let report = fixture.launch();
            assert_eq!((report.settled, report.retained), (0, 1), "{variant}");
            assert_eq!(fixture.journal()["phase"], "installing", "{variant}");
            assert_eq!(
                fixture.store().scan().unwrap(),
                vec![StoredEntry::Record(record)],
                "{variant}"
            );
        }
    }

    #[test]
    fn original_scope_skips_other_records_without_contacting_the_sidecar() {
        let saved = uuid::Uuid::now_v7().to_string();
        let fixture = Fixture::new(Some(&saved), Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Failed))
            .unwrap();
        let other = uuid::Uuid::now_v7().to_string();
        let skipped = |server: &str, id: &str, revision: u64, scope: Option<&str>| {
            fixture.connector.reconcile_retained_outcomes(
                &ReconcileScope::Original {
                    server,
                    id,
                    revision,
                    saved_connection: scope,
                },
                &|| false,
            )
        };
        for report in [
            skipped(&other, &fixture.id, 7, Some(&saved)),
            skipped(&fixture.server, &fixture.id, 8, Some(&saved)),
            skipped(&fixture.server, &fixture.id, 7, None),
            skipped(&fixture.server, &fixture.id, 7, Some(&other)),
        ] {
            assert_eq!(report.skipped, 1);
            assert_eq!(report.settled, 0);
        }
        // Another operation has no record of its own, so nothing is read.
        assert_eq!(
            skipped(&fixture.server, &other, 7, Some(&saved)),
            ReconcileReport::default()
        );
        assert_eq!(fixture.calls(), 0);
        let exact = skipped(&fixture.server, &fixture.id, 7, Some(&saved));
        assert_eq!(exact.settled, 1);
        assert_eq!(fixture.journal()["phase"], "failed");
    }

    #[test]
    fn corrupt_or_unparsable_evidence_is_retained_and_never_deleted() {
        let fixture = Fixture::new(None, Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        let directory = fixture.root.join(crate::updater_outcome::STORE_DIRECTORY);
        let good = uuid::Uuid::now_v7().to_string();
        let mut files = vec![
            (format!("{good}.json"), b"{not json".to_vec()),
            (
                format!("{}.json", uuid::Uuid::now_v7()),
                vec![b' '; (4096 + 1) as usize],
            ),
            ("not-a-uuid.json".to_string(), b"{}".to_vec()),
        ];
        // A syntactically valid record stored under another operation's name.
        let misnamed = uuid::Uuid::now_v7().to_string();
        files.push((
            format!("{misnamed}.json"),
            serde_json::to_vec(&fixture.record(Phase::Installed)).unwrap(),
        ));
        // Unknown fields and a non-terminal phase are corrupt evidence too.
        let mut unknown = serde_json::to_value(fixture.record(Phase::Installed)).unwrap();
        unknown["install"] = serde_json::json!(true);
        let unknown_id = uuid::Uuid::now_v7().to_string();
        unknown["operation_id"] = serde_json::json!(unknown_id);
        files.push((
            format!("{unknown_id}.json"),
            serde_json::to_vec(&unknown).unwrap(),
        ));
        let mut prepared = serde_json::to_value(fixture.record(Phase::Installed)).unwrap();
        let prepared_id = uuid::Uuid::now_v7().to_string();
        prepared["operation_id"] = serde_json::json!(prepared_id);
        prepared["phase"] = serde_json::json!("prepared");
        files.push((
            format!("{prepared_id}.json"),
            serde_json::to_vec(&prepared).unwrap(),
        ));
        for (name, bytes) in &files {
            fs::write(directory.join(name), bytes).unwrap();
        }
        let report = fixture.launch();
        assert_eq!(report.settled, 1, "valid evidence still settles");
        assert_eq!(report.retained, files.len());
        assert_eq!(fixture.calls(), 1, "corrupt evidence never reaches Go");
        for (name, bytes) in files {
            assert_eq!(fs::read(directory.join(name)).unwrap(), bytes);
        }
    }

    /// Many unreadable entries, all sorting before the
    /// valid record (UUID-v7 names start with a digit after `0-`).
    fn write_unreadable_prefix(fixture: &Fixture) -> Vec<PathBuf> {
        let directory = fixture.root.join(crate::updater_outcome::STORE_DIRECTORY);
        (0..40)
            .map(|index| {
                let path = directory.join(format!("0-corrupt-{index:02}.json"));
                fs::write(&path, b"{not json").unwrap();
                path
            })
            .collect()
    }

    #[test]
    fn unreadable_entries_never_starve_launch_replay() {
        let fixture = Fixture::new(None, Some("installing"));
        let record = fixture.record(Phase::Installed);
        fixture.store().persist(&record).unwrap();
        let corrupt = write_unreadable_prefix(&fixture);
        let report = fixture.launch();
        assert_eq!((report.settled, report.retained), (1, corrupt.len()));
        assert_eq!(fixture.journal()["phase"], "installed");
        assert!(!fixture.marker(&fixture.id).exists());
        for path in corrupt {
            assert_eq!(fs::read(path).unwrap(), b"{not json");
        }
    }

    #[test]
    fn repeatedly_rejected_records_never_starve_later_launch_replay() {
        let fixture = Fixture::new(None, Some("installing"));
        let record = fixture.record(Phase::Installed);
        fixture.store().persist(&record).unwrap();
        // Many syntactically valid records, all sorting before the
        // valid one, whose journals Go rejects on every launch.
        let rejected: Vec<OutcomeRecord> = (0..40)
            .map(|index| {
                let id = format!("00000000-0000-7000-8000-{index:012}");
                let rejected =
                    OutcomeRecord::outcome(&id, &id, 7, &id, None, Phase::Failed).unwrap();
                fixture.store().persist(&rejected).unwrap();
                rejected
            })
            .collect();
        let first = fixture.launch();
        assert_eq!((first.settled, first.retained), (1, rejected.len()));
        assert_eq!(fixture.journal()["phase"], "installed");
        assert!(!fixture.marker(&fixture.id).exists());
        let second = fixture.launch();
        assert_eq!((second.settled, second.retained), (0, rejected.len()));
        // Rejected evidence is retained untouched on every pass.
        for stored in &rejected {
            assert_eq!(
                fixture.store().load(&stored.operation_id).unwrap(),
                Some(StoredEntry::Record(stored.clone()))
            );
        }
        assert_eq!(fixture.calls(), 2 * rejected.len() + 1);
    }

    #[test]
    fn unreadable_entries_never_starve_exact_original_inspection() {
        let saved = uuid::Uuid::now_v7().to_string();
        let fixture = Fixture::new(Some(&saved), Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Failed))
            .unwrap();
        let corrupt = write_unreadable_prefix(&fixture);
        let report = fixture.connector.reconcile_retained_outcomes(
            &ReconcileScope::Original {
                server: &fixture.server,
                id: &fixture.id,
                revision: 7,
                saved_connection: Some(&saved),
            },
            &|| false,
        );
        assert_eq!(
            report,
            ReconcileReport {
                settled: 1,
                retained: 0,
                skipped: 0
            }
        );
        assert_eq!(fixture.journal()["phase"], "failed");
        assert!(corrupt.iter().all(|path| path.exists()));
    }

    #[test]
    fn corrupt_exact_record_is_retained_without_contacting_the_sidecar() {
        let fixture = Fixture::new(None, Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Failed))
            .unwrap();
        fs::write(fixture.marker(&fixture.id), b"{not json").unwrap();
        let report = fixture.connector.reconcile_retained_outcomes(
            &ReconcileScope::Original {
                server: &fixture.server,
                id: &fixture.id,
                revision: 7,
                saved_connection: None,
            },
            &|| false,
        );
        assert_eq!((report.settled, report.retained), (0, 1));
        assert_eq!(fixture.calls(), 0);
        assert_eq!(fs::read(fixture.marker(&fixture.id)).unwrap(), b"{not json");
    }

    #[test]
    fn absent_journal_never_infers_installed_and_keeps_the_record() {
        let fixture = Fixture::new(None, None);
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        let report = fixture.launch();
        assert_eq!((report.settled, report.retained), (0, 1));
        assert!(fixture.marker(&fixture.id).exists());
        assert!(!fixture.root.join("journal.json").exists());
    }

    #[test]
    fn conflicting_settled_phase_is_not_overwritten_and_keeps_the_record() {
        let fixture = Fixture::new(None, Some("failed"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        let report = fixture.launch();
        assert_eq!((report.settled, report.retained), (0, 1));
        assert_eq!(fixture.journal()["phase"], "failed");
        assert!(fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn already_settled_same_phase_is_idempotent_and_retires() {
        let fixture = Fixture::new(None, Some("uncertain"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Uncertain))
            .unwrap();
        assert_eq!(fixture.launch().settled, 1);
        assert_eq!(fixture.journal()["phase"], "uncertain");
        assert!(!fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn stopping_prevents_new_settlements_and_keeps_records() {
        let fixture = Fixture::new(None, Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        let report = fixture
            .connector
            .reconcile_retained_outcomes(&ReconcileScope::Launch, &|| true);
        assert_eq!(report, ReconcileReport::default());
        assert_eq!(fixture.calls(), 0);
        assert!(fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn settlement_token_requires_a_terminal_valid_record() {
        let fixture = Fixture::new(None, Some("installing"));
        let mut record = fixture.record(Phase::Installed);
        record.phase = Phase::Prepared;
        assert!(matches!(
            SettlementToken::from_record(record),
            Err(NativeFailure::InvalidEvidence)
        ));
        let mut record = fixture.record(Phase::Installed);
        record.operation_id = "not-an-id".into();
        assert!(SettlementToken::from_record(record).is_err());
    }
}

#[cfg(all(test, unix))]
mod begin_intent_tests {
    use std::{os::unix::fs::PermissionsExt, sync::Arc};

    use super::*;

    // One fixture plays both the resident desktop-host (native-begin) and the
    // offline original-journal commands (native-inspect, native-outcome). The
    // journal lives in journal.json and `begins`/`calls` count each request.
    const SIDECAR: &str = r#"#!/usr/bin/python3
import sys, json, os, time
root = sys.argv[2]
journal_path = os.path.join(root, 'journal.json')

def read(name, default=''):
    path = os.path.join(root, name)
    return open(path).read().strip() if os.path.exists(path) else default

def journal_matches(options, generation_checked):
    if not os.path.exists(journal_path):
        return None
    journal = json.load(open(journal_path))
    if (options['--id'] != journal['operation_id']
            or options['--server-id'] != journal['server_id']
            or options['--revision'] != str(journal['revision'])
            or options.get('--saved-connection') != journal.get('saved')):
        return None
    if generation_checked and options['--native-generation'] != journal['generation']:
        return None
    return journal

def descriptor(journal):
    value = json.load(open(os.path.join(root, 'descriptor.json')))
    value['phase'] = journal['phase']
    value['generation'] = journal['generation']
    return value

if sys.argv[3] == 'server':
    print(json.dumps({'version': 2, 'result': {'endpoint': 'http://127.0.0.1:51234', 'generation': '019c2381-9300-7000-8000-000000000001', 'key': 'CQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQkJCQk'}}), flush=True)
    for line in sys.stdin:
        request = json.loads(line)
        op = request['operation']
        if op == 'runtime.shutdown':
            break
        if op in ('runtime.cancel', 'runtime.fence'):
            continue
        assert op == 'update.native-begin', op
        open(os.path.join(root, 'begins'), 'a').write('x')
        args = request['arguments']
        options = dict(zip(args[0::2], args[1::2]))
        mode = read('begin_mode', 'accept')
        journal = journal_matches(options, True)
        if journal is None or journal['phase'] != 'prepared':
            print(json.dumps({'version': 2, 'id': request['id'], 'error': {'code': 'recovery_required'}}), flush=True)
            continue
        if mode == 'lost':
            continue
        if mode == 'reject':
            print(json.dumps({'version': 2, 'id': request['id'], 'error': {'code': 'recovery_required'}}), flush=True)
            continue
        journal['phase'] = 'installing'
        json.dump(journal, open(journal_path, 'w'))
        if mode == 'accept-lost':
            continue
        if mode == 'accept-late':
            while not os.path.exists(os.path.join(root, 'release')):
                time.sleep(0.01)
        print(json.dumps({'version': 2, 'id': request['id'], 'result': descriptor(journal)}), flush=True)
else:
    open(os.path.join(root, 'calls'), 'a').write(sys.argv[4][7:8])
    args = sys.argv[3:]
    command = args[1]
    assert args[0] == 'update' and command in ('native-inspect', 'native-outcome'), args
    options = dict(zip(args[2::2], args[3::2]))
    def fail(code):
        print(json.dumps({'version': 1, 'error': {'code': code}}))
        sys.exit(1)
    if read('mode') == 'down':
        fail('unavailable')
    journal = journal_matches(options, command == 'native-outcome')
    if journal is None:
        fail('recovery_required')
    if command == 'native-outcome':
        outcome = options['--outcome']
        if journal['phase'] not in ('installing', outcome):
            fail('recovery_required')
        journal['phase'] = outcome
        json.dump(journal, open(journal_path, 'w'))
    print(json.dumps({'version': 1, 'result': descriptor(journal)}))
"#;

    struct Fixture {
        _temp: tempfile::TempDir,
        root: PathBuf,
        connector: Arc<Connector>,
        server: String,
        id: String,
        generation: String,
    }

    impl Fixture {
        fn new(journal_phase: &str, scope: bool) -> Self {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().canonicalize().unwrap();
            let sidecar = root.join("sidecar");
            fs::write(&sidecar, SIDECAR).unwrap();
            fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
            if scope {
                fs::create_dir_all(root.join("desktop-updates")).unwrap();
                fs::set_permissions(
                    root.join("desktop-updates"),
                    fs::Permissions::from_mode(0o700),
                )
                .unwrap();
            }
            let connector = Arc::new(Connector::new(sidecar, root.clone()).unwrap());
            let id = uuid::Uuid::now_v7().to_string();
            let server = uuid::Uuid::now_v7().to_string();
            let generation = uuid::Uuid::now_v7().to_string();
            let target = format!(
                "{}-{}",
                std::env::consts::OS,
                if cfg!(target_arch = "aarch64") {
                    "arm64"
                } else {
                    "amd64"
                }
            )
            .replace("macos-", "darwin-");
            let extension = if cfg!(target_os = "macos") {
                ".dmg"
            } else {
                ".AppImage"
            };
            let descriptor = serde_json::json!({"version":1,"operation_id":id,"server_id":server,"generation":generation,"release_version":"0.2.0","target":target,"phase":"prepared","artifact_path":root.join("desktop-updates/downloads").join(format!("{}{}","a".repeat(64),extension)),"artifact_sha256":"a".repeat(64),"artifact_size":8,"manifest_sha256":"b".repeat(64)});
            fs::write(
                root.join("descriptor.json"),
                serde_json::to_vec(&descriptor).unwrap(),
            )
            .unwrap();
            let fixture = Self {
                _temp: temp,
                root,
                connector,
                server,
                id,
                generation,
            };
            fixture.set_journal(journal_phase, &fixture.generation.clone());
            fixture
        }

        fn set_journal(&self, phase: &str, generation: &str) {
            fs::write(
                self.root.join("journal.json"),
                serde_json::to_vec(&serde_json::json!({
                    "operation_id": self.id, "server_id": self.server, "revision": 7,
                    "generation": generation, "saved": null, "phase": phase,
                }))
                .unwrap(),
            )
            .unwrap();
        }

        fn journal(&self) -> serde_json::Value {
            serde_json::from_slice(&fs::read(self.root.join("journal.json")).unwrap()).unwrap()
        }

        fn mode(&self, mode: &str) {
            fs::write(self.root.join("begin_mode"), mode).unwrap();
        }

        fn begins(&self) -> usize {
            fs::read(self.root.join("begins")).map_or(0, |v| v.len())
        }

        // Offline requests: i = inspect, o = outcome.
        fn calls(&self) -> String {
            String::from_utf8(fs::read(self.root.join("calls")).unwrap_or_default()).unwrap()
        }

        fn intent(&self) -> OutcomeRecord {
            OutcomeRecord::begin_intent(&self.server, &self.id, 7, &self.generation, None).unwrap()
        }

        fn intents(&self) -> Vec<StoredEntry> {
            self.connector.intent_store().scan().unwrap()
        }

        fn begin(&self) -> BeginOutcome {
            begin(&self.connector, self)
        }

        // Starts native-begin on a worker and cancels it once the runtime has
        // received the request, the way Quit does.
        fn begin_then_quit(&self) -> BeginOutcome {
            let connector = Arc::clone(&self.connector);
            let (server, id, generation) = (
                self.server.clone(),
                self.id.clone(),
                self.generation.clone(),
            );
            let worker = std::thread::spawn(move || {
                connector.begin_desktop_installation(
                    None,
                    DesktopUpdateRequest {
                        server: &server,
                        id: &id,
                        revision: 7,
                        generation: &generation,
                        action: "native-begin",
                        outcome: None,
                    },
                )
            });
            let waiting = std::time::Instant::now();
            while self.begins() == 0 {
                assert!(waiting.elapsed() < Duration::from_secs(30));
                std::thread::sleep(Duration::from_millis(10));
            }
            self.connector.exiting.store(true, Ordering::Release);
            worker.join().unwrap()
        }
    }

    fn begin(connector: &Connector, f: &Fixture) -> BeginOutcome {
        connector.begin_desktop_installation(
            None,
            DesktopUpdateRequest {
                server: &f.server,
                id: &f.id,
                revision: 7,
                generation: &f.generation,
                action: "native-begin",
                outcome: None,
            },
        )
    }

    // A later process: same root, a fresh connector with no pending state.
    fn restarted(f: &Fixture) -> Connector {
        Connector::new(f.root.join("sidecar"), f.root.clone()).unwrap()
    }

    fn launch(connector: &Connector) -> ReconcileReport {
        connector.reconcile_begin_intents(&ReconcileScope::Launch, &|| false)
    }

    #[test]
    fn accepted_begin_keeps_normal_behavior_and_retires_the_intent_on_settlement() {
        let f = Fixture::new("prepared", true);
        f.mode("accept");
        let BeginOutcome::Accepted(accepted) = f.begin() else {
            panic!("an accepted response must stay Accepted");
        };
        assert!(!accepted.is_settlement_only());
        assert_eq!(f.intents().len(), 1, "intent is retained until settlement");
        assert_eq!(
            accepted.install_with(false, || Phase::Installed),
            Phase::Installed
        );
        accepted.settle(&f.connector, Phase::Installed).unwrap();
        assert_eq!(f.journal()["phase"], "installed");
        assert!(f.intents().is_empty());
        assert_eq!((f.begins(), f.calls().as_str()), (1, "o"));
        f.connector.shutdown_owned().unwrap();
    }

    #[test]
    fn a_begin_whose_identity_cannot_be_retained_is_never_submitted() {
        let f = Fixture::new("prepared", false);
        assert!(matches!(
            f.begin(),
            BeginOutcome::Rejected(NativeFailure::StorageUnavailable)
        ));
        assert_eq!((f.begins(), f.calls().as_str()), (0, ""));
        assert_eq!(f.journal()["phase"], "prepared");
    }

    #[test]
    fn a_begin_rejected_before_it_is_sent_retires_its_intent() {
        let f = Fixture::new("prepared", true);
        let held = f.connector.gate.lock().unwrap();
        assert!(matches!(
            f.begin(),
            BeginOutcome::Rejected(NativeFailure::Busy)
        ));
        drop(held);
        assert!(f.intents().is_empty());
        assert_eq!((f.begins(), f.calls().as_str()), (0, ""));
    }

    #[test]
    fn a_begin_refused_before_the_resident_pipe_accepts_it_does_not_fence_a_retry() {
        let f = Fixture::new("prepared", true);
        // Quit is already in progress, so the resident request is refused
        // before its frame is queued, even though the begin reached the
        // resident request path.
        f.connector.exiting.store(true, Ordering::Release);
        assert!(matches!(
            f.begin(),
            BeginOutcome::Rejected(NativeFailure::Stopped)
        ));
        assert!(f.intents().is_empty(), "a request that never left retires");
        assert_eq!((f.begins(), f.calls().as_str()), (0, ""));
        assert_eq!(f.journal()["phase"], "prepared");
        // Nothing is fenced: once the refusal clears, the same begin is
        // submitted and accepted.
        f.connector.exiting.store(false, Ordering::Release);
        assert!(matches!(f.begin(), BeginOutcome::Accepted(_)));
        assert_eq!(f.begins(), 1);
        f.connector.exiting.store(true, Ordering::Release);
        f.connector.shutdown_owned().unwrap();
    }

    #[test]
    fn canceled_response_after_acceptance_is_recovered_without_replaying_begin_or_installer() {
        let f = Fixture::new("prepared", true);
        f.mode("accept-lost");
        let BeginOutcome::Recovered {
            installation,
            cause,
        } = f.begin_then_quit()
        else {
            panic!("an inspected Installing journal must recover ownership");
        };
        assert_eq!(cause, NativeFailure::Stopped);
        assert!(installation.is_settlement_only());
        assert!(installation.matches(&f.server, &f.id, 7));
        assert_eq!(f.journal()["phase"], "installing");
        // The recovered token can never reach an installer.
        assert_eq!(
            installation.install_with(false, || panic!("installer must not run")),
            Phase::Uncertain
        );
        assert_eq!(f.intents().len(), 1);
        assert_eq!(
            installation
                .settle(&f.connector, Phase::Uncertain)
                .unwrap()
                .phase,
            Phase::Uncertain
        );
        assert_eq!(f.journal()["phase"], "uncertain");
        assert_eq!(f.journal()["generation"], f.generation);
        assert!(f.intents().is_empty());
        assert_eq!(f.begins(), 1, "native-begin is never repeated");
        assert_eq!(f.calls(), "io");
        f.connector.shutdown_owned().unwrap();
    }

    #[test]
    fn lost_response_before_acceptance_stays_unresolved_in_process_and_retires_after_restart() {
        let f = Fixture::new("prepared", true);
        f.mode("lost");
        assert!(matches!(
            f.begin_then_quit(),
            BeginOutcome::Unresolved(NativeFailure::Stopped)
        ));
        // The resident runtime may still land the request, so the Prepared
        // journal does not yet prove non-acceptance.
        assert_eq!(f.intents().len(), 1);
        assert_eq!(f.journal()["phase"], "prepared");
        f.connector.shutdown_owned().unwrap();
        // A later process starts after that runtime exited.
        let next = restarted(&f);
        assert_eq!(
            launch(&next),
            ReconcileReport {
                settled: 1,
                retained: 0,
                skipped: 0
            }
        );
        assert!(f.intents().is_empty());
        assert_eq!(f.journal()["phase"], "prepared");
        assert_eq!((f.begins(), f.calls().as_str()), (1, "ii"));
    }

    #[test]
    fn rejected_response_with_a_prepared_journal_is_retained_not_assumed_unaccepted() {
        let f = Fixture::new("prepared", true);
        f.mode("reject");
        // A typed rejection cannot be told apart from a transport failure by
        // its error code alone, so the Prepared journal stays unproven until
        // the runtime that carried the request has exited.
        let outcome = f.begin();
        assert!(outcome.failure().is_some());
        assert!(matches!(outcome, BeginOutcome::Unresolved(_)));
        assert_eq!(f.begins(), 1);
        assert_eq!(launch(&restarted(&f)).settled, 1);
        assert!(f.intents().is_empty());
        f.connector.shutdown_owned().unwrap();
    }

    #[test]
    fn a_retry_never_resubmits_begin_while_the_original_intent_is_unresolved() {
        let f = Fixture::new("prepared", true);
        f.mode("reject");
        assert!(matches!(f.begin(), BeginOutcome::Unresolved(_)));
        assert_eq!(f.begins(), 1);
        // The original request may still land, so every retry stays fenced and
        // only re-reads the journal.
        for _ in 0..2 {
            assert!(matches!(
                f.begin(),
                BeginOutcome::Unresolved(NativeFailure::Busy)
            ));
        }
        assert_eq!(f.begins(), 1, "native-begin is never submitted again");
        assert_eq!(f.calls(), "iii");
        assert_eq!(f.intents().len(), 1);
        assert_eq!(f.journal()["phase"], "prepared");
        f.connector.shutdown_owned().unwrap();
    }

    #[test]
    fn a_fenced_retry_recovers_ownership_when_the_original_begin_lands_late() {
        let f = Fixture::new("prepared", true);
        f.mode("reject");
        assert!(matches!(f.begin(), BeginOutcome::Unresolved(_)));
        // The original request was accepted after the first inspection.
        f.set_journal("installing", &f.generation.clone());
        let BeginOutcome::Recovered {
            installation,
            cause,
        } = f.begin()
        else {
            panic!("the retained intent must recover the late acceptance");
        };
        assert_eq!(cause, NativeFailure::Busy);
        assert!(installation.is_settlement_only());
        assert_eq!(f.begins(), 1, "the retry never races the original begin");
        installation.settle(&f.connector, Phase::Uncertain).unwrap();
        assert_eq!(f.journal()["phase"], "uncertain");
        assert!(f.intents().is_empty());
        f.connector.shutdown_owned().unwrap();
    }

    #[test]
    fn a_retry_after_the_intent_is_resolved_is_admitted_again() {
        let f = Fixture::new("prepared", true);
        f.mode("reject");
        assert!(matches!(f.begin(), BeginOutcome::Unresolved(_)));
        // A later process proves the runtime exited and retires the intent.
        assert_eq!(launch(&restarted(&f)).settled, 1);
        assert!(f.intents().is_empty());
        f.mode("accept");
        assert!(matches!(f.begin(), BeginOutcome::Accepted(_)));
        assert_eq!(f.begins(), 2);
        f.connector.shutdown_owned().unwrap();
    }

    #[test]
    fn an_unreadable_retained_intent_for_the_request_rejects_the_begin() {
        let f = Fixture::new("prepared", true);
        let intent = f.intent();
        f.connector.intent_store().persist(&intent).unwrap();
        let path = f
            .root
            .join(crate::updater_outcome::INTENT_DIRECTORY)
            .join(format!("{}.{}.json", f.id, f.generation));
        fs::write(&path, b"{").unwrap();
        assert!(matches!(
            f.begin(),
            BeginOutcome::Rejected(NativeFailure::InvalidEvidence)
        ));
        assert_eq!((f.begins(), f.calls().as_str()), (0, ""));
        assert_eq!(fs::read(&path).unwrap(), b"{");
    }

    #[test]
    fn late_original_response_after_recovery_changes_nothing() {
        let f = Fixture::new("prepared", true);
        f.mode("accept-late");
        let BeginOutcome::Recovered { installation, .. } = f.begin_then_quit() else {
            panic!("Installing journal must recover ownership");
        };
        // The original response arrives after native gave up on it.
        fs::write(f.root.join("release"), b"x").unwrap();
        std::thread::sleep(Duration::from_millis(200));
        assert_eq!(f.journal()["phase"], "installing");
        installation.settle(&f.connector, Phase::Uncertain).unwrap();
        assert_eq!(f.journal()["phase"], "uncertain");
        assert_eq!(f.begins(), 1);
        assert_eq!(f.calls(), "io");
        f.connector.shutdown_owned().unwrap();
    }

    #[test]
    fn restart_settles_an_installing_journal_as_uncertain_without_an_installer() {
        let f = Fixture::new("installing", true);
        f.connector.intent_store().persist(&f.intent()).unwrap();
        assert_eq!(
            launch(&f.connector),
            ReconcileReport {
                settled: 1,
                retained: 0,
                skipped: 0
            }
        );
        assert_eq!(f.journal()["phase"], "uncertain");
        assert_eq!(f.journal()["generation"], f.generation);
        assert!(f.intents().is_empty());
        assert_eq!((f.begins(), f.calls().as_str()), (0, "io"));
        assert_eq!(launch(&f.connector), ReconcileReport::default());
    }

    #[test]
    fn restart_retires_an_intent_whose_journal_is_already_terminal() {
        for phase in ["installed", "failed", "uncertain"] {
            let f = Fixture::new(phase, true);
            f.connector.intent_store().persist(&f.intent()).unwrap();
            assert_eq!(launch(&f.connector).settled, 1);
            assert_eq!(f.journal()["phase"], phase);
            assert_eq!((f.begins(), f.calls().as_str()), (0, "i"));
            assert!(f.intents().is_empty());
        }
    }

    #[test]
    fn foreign_generation_or_scope_never_settles() {
        // Another generation owns the journal: this begin can never match it.
        let f = Fixture::new("installing", true);
        let foreign = uuid::Uuid::now_v7().to_string();
        f.set_journal("installing", &foreign);
        f.connector.intent_store().persist(&f.intent()).unwrap();
        launch(&f.connector);
        assert_eq!(f.journal()["phase"], "installing");
        assert_eq!(f.journal()["generation"], foreign);
        assert_eq!(f.calls(), "i", "inspection only, no outcome write");
        assert!(f.intents().is_empty());
        // A different saved scope is rejected by the original journal read
        // and the intent stays retained.
        let f = Fixture::new("installing", true);
        let saved = uuid::Uuid::now_v7().to_string();
        let scoped =
            OutcomeRecord::begin_intent(&f.server, &f.id, 7, &f.generation, Some(&saved)).unwrap();
        f.connector.intent_store().persist(&scoped).unwrap();
        assert_eq!(launch(&f.connector).retained, 1);
        assert_eq!(f.journal()["phase"], "installing");
        assert_eq!(f.intents().len(), 1);
    }

    #[test]
    fn unreadable_inspection_keeps_the_intent_across_restarts() {
        let f = Fixture::new("installing", true);
        f.connector.intent_store().persist(&f.intent()).unwrap();
        fs::write(f.root.join("mode"), "down").unwrap();
        for _ in 0..2 {
            let next = restarted(&f);
            assert_eq!(launch(&next).retained, 1);
            assert_eq!(f.intents().len(), 1);
            assert_eq!(f.journal()["phase"], "installing");
        }
        fs::remove_file(f.root.join("mode")).unwrap();
        assert_eq!(launch(&restarted(&f)).settled, 1);
        assert_eq!(f.journal()["phase"], "uncertain");
        assert!(f.intents().is_empty());
    }

    #[test]
    fn an_exact_retained_outcome_settles_before_an_inferred_uncertain() {
        let f = Fixture::new("installing", true);
        f.connector.intent_store().persist(&f.intent()).unwrap();
        let outcome =
            OutcomeRecord::outcome(&f.server, &f.id, 7, &f.generation, None, Phase::Installed)
                .unwrap();
        f.connector.outcome_store().persist(&outcome).unwrap();
        // While the outcome cannot settle, the intent must not write Uncertain.
        fs::write(f.root.join("mode"), "down").unwrap();
        assert_eq!(launch(&f.connector).retained, 1);
        assert_eq!(f.journal()["phase"], "installing");
        fs::remove_file(f.root.join("mode")).unwrap();
        f.connector
            .reconcile_retained_outcomes(&ReconcileScope::Launch, &|| false);
        assert_eq!(f.journal()["phase"], "installed");
        launch(&f.connector);
        assert_eq!(f.journal()["phase"], "installed");
        assert!(f.intents().is_empty());
        assert!(f.connector.outcome_store().scan().unwrap().is_empty());
    }

    #[test]
    fn original_scope_inspection_only_resolves_its_own_intent_and_never_retires_prepared() {
        let f = Fixture::new("prepared", true);
        f.connector.intent_store().persist(&f.intent()).unwrap();
        let other = ReconcileScope::Original {
            server: &f.server,
            id: &uuid::Uuid::now_v7().to_string(),
            revision: 7,
            saved_connection: None,
        };
        // Another operation's scope never lists, reads or contacts anything.
        assert_eq!(
            f.connector.reconcile_begin_intents(&other, &|| false),
            ReconcileReport::default()
        );
        assert_eq!(f.calls(), "");
        let own = ReconcileScope::Original {
            server: &f.server,
            id: &f.id,
            revision: 7,
            saved_connection: None,
        };
        // In-process Prepared is not proof while the runtime may be alive.
        assert_eq!(
            f.connector
                .reconcile_begin_intents(&own, &|| false)
                .retained,
            1
        );
        assert_eq!(f.intents().len(), 1);
        f.set_journal("installing", &f.generation.clone());
        assert_eq!(
            f.connector.reconcile_begin_intents(&own, &|| false).settled,
            1
        );
        assert_eq!(f.journal()["phase"], "uncertain");
    }

    // Intents of other operations that sort before the fixture's own record
    // and stay retained because the journal cannot prove them. UUID-v7 names
    // made now sort after the all-zero timestamp prefix.
    fn write_unresolved_prefix(f: &Fixture, count: usize) {
        for index in 0..count {
            let id = format!("00000000-0000-7000-8000-{index:012}");
            let decoy =
                OutcomeRecord::begin_intent(&f.server, &id, 7, &f.generation, None).unwrap();
            f.connector.intent_store().persist(&decoy).unwrap();
        }
    }

    #[test]
    fn retained_intents_never_starve_launch_reconciliation_beyond_the_scan_bound() {
        let f = Fixture::new("installing", true);
        // Many unresolved intents sort before the fixture's own record.
        write_unresolved_prefix(&f, 40);
        f.connector.intent_store().persist(&f.intent()).unwrap();
        for _ in 0..2 {
            let report = launch(&restarted(&f));
            assert_eq!(report.retained, 40, "unprovable decoys stay retained");
            assert_eq!(f.intents().len(), 40, "retained intents are never deleted");
        }
        assert_eq!(f.journal()["phase"], "uncertain");
        assert_eq!(f.journal()["generation"], f.generation);
        assert!(
            !f.root
                .join(crate::updater_outcome::INTENT_DIRECTORY)
                .join(format!("{}.{}.json", f.id, f.generation))
                .exists()
        );
        assert_eq!(f.begins(), 0);
    }

    #[test]
    fn an_exact_original_inspection_reaches_its_intent_behind_unrelated_ones() {
        let f = Fixture::new("installing", true);
        write_unresolved_prefix(&f, 40);
        f.connector.intent_store().persist(&f.intent()).unwrap();
        let own = ReconcileScope::Original {
            server: &f.server,
            id: &f.id,
            revision: 7,
            saved_connection: None,
        };
        assert_eq!(
            f.connector.reconcile_begin_intents(&own, &|| false),
            ReconcileReport {
                settled: 1,
                retained: 0,
                skipped: 0
            }
        );
        assert_eq!(f.journal()["phase"], "uncertain");
        assert_eq!(f.calls(), "io", "unrelated intents are never inspected");
    }

    #[test]
    fn a_pending_outcome_beyond_the_scan_bound_still_settles_before_an_inferred_uncertain() {
        let f = Fixture::new("installing", true);
        f.connector.intent_store().persist(&f.intent()).unwrap();
        // 40 earlier outcomes push the exact record past the first page.
        for index in 0..40 {
            let id = format!("00000000-0000-7000-8000-{index:012}");
            let decoy =
                OutcomeRecord::outcome(&f.server, &id, 7, &f.generation, None, Phase::Failed)
                    .unwrap();
            f.connector.outcome_store().persist(&decoy).unwrap();
        }
        let outcome =
            OutcomeRecord::outcome(&f.server, &f.id, 7, &f.generation, None, Phase::Installed)
                .unwrap();
        f.connector.outcome_store().persist(&outcome).unwrap();
        let report = launch(&f.connector);
        assert_eq!((report.settled, report.retained), (0, 1));
        assert_eq!(f.journal()["phase"], "installing", "no inferred Uncertain");
        assert_eq!(f.calls(), "");
        // Replaying the outcomes walks past the first page to the exact one.
        f.connector
            .reconcile_retained_outcomes(&ReconcileScope::Launch, &|| false);
        assert_eq!(f.journal()["phase"], "installed");
        assert!(matches!(f.connector.outcome_store().load(&f.id), Ok(None)));
    }

    #[test]
    fn stopping_prevents_new_intent_inspection_and_keeps_intents() {
        let f = Fixture::new("installing", true);
        f.connector.intent_store().persist(&f.intent()).unwrap();
        let report = f
            .connector
            .reconcile_begin_intents(&ReconcileScope::Launch, &|| true);
        assert_eq!(report, ReconcileReport::default());
        assert_eq!(f.calls(), "");
        assert_eq!(f.intents().len(), 1);
    }
}
