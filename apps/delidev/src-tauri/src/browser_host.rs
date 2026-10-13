//! Raw CEF children deliberately bypass Tauri's scripts, schemes and IPC
//! handler.
use std::{
    collections::BTreeMap,
    fs,
    path::PathBuf,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    thread,
    time::{Duration, Instant},
};

use cef::*;
use delidev_desktop::{
    Connector, NativeFailure, SavedConnection,
    browser::{self, Policy, ProfileRecord, ProfileState, Tab, Tabs},
    browser_storage::{BrowserStorage, BrowserStorageMode},
    canonical_id,
};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Emitter, Manager, WebviewWindow};
use tauri_runtime_cef::CefRuntime;
type Result<T> = std::result::Result<T, NativeFailure>;
#[derive(Clone, Copy, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Bounds {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}
impl Bounds {
    fn valid(&self) -> bool {
        [self.x, self.y, self.width, self.height]
            .iter()
            .all(|v| v.is_finite() && *v >= 0.0 && *v <= 32768.0)
            && self.width >= 1.0
            && self.height >= 1.0
    }
}
#[derive(Clone, Copy, Debug, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Action {
    Navigate,
    Back,
    Forward,
    Reload,
    SelectTab,
    Resize,
    Hide,
}
#[derive(Clone, Serialize)]
pub struct BrowserState {
    pub tabs: Tabs,
    pub removal_pending: bool,
}
/// Native-local page operations. Create and Close are explicit resource
/// mutations; Inventory and Observe are read-only and never add a page.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum PageAction {
    Inventory,
    Create,
    Close,
    Observe,
}
#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PageRequest {
    pub action: PageAction,
    #[serde(default)]
    pub operation_id: Option<String>,
    #[serde(default)]
    pub url: Option<String>,
    #[serde(default)]
    pub page_id: Option<String>,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum PagePhase {
    Accepted,
    CleanupPending,
    Completed,
    Failed,
}
/// The original operation's retained result. `metadata_committed` and
/// `native_cleanup_complete` are separate facts: a committed removal can still
/// await native child cleanup.
#[derive(Clone, Debug, Serialize)]
pub struct PageReceipt {
    pub operation_id: String,
    pub page_id: String,
    pub phase: PagePhase,
    pub metadata_committed: bool,
    pub native_cleanup_complete: bool,
    pub failure: Option<NativeFailure>,
}
#[derive(Clone, Serialize)]
pub struct PageInventory {
    pub state: BrowserState,
    pub receipt: Option<PageReceipt>,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PageAdmission {
    Admitted,
    Replayed,
}
#[derive(Clone)]
struct PageOperation {
    request: PageRequest,
    receipt: PageReceipt,
    // Inventory after the operation, for a profile that was never loaded.
    tabs: Tabs,
    order: u64,
    // A directory fsync failed after replacement. Observation retries only
    // the durability step, never the mutation.
    durability: Option<PathBuf>,
}
const PAGE_LIMIT: usize = 16;
const OPERATION_LIMIT: usize = 64;
fn operation_key(owner: &str, profile: &str, operation_id: &str) -> String {
    format!("{owner}|{profile}|{operation_id}")
}
pub struct Control {
    pub profile: String,
    pub view_id: String,
    pub action: Action,
    pub url: Option<String>,
    pub prepared_revision: Option<u64>,
    pub bounds: Option<Bounds>,
}
#[derive(Clone)]
struct ViewRequest {
    window: String,
    parent: usize,
    bounds: Bounds,
    scale: f64,
    tab: String,
    generation: u64,
}
struct RuntimeProfile {
    record: ProfileRecord,
    // Shared tab metadata stays in the original directory. Only CEF data uses
    // the mode-specific cache directory; deletion retains both identities.
    path: PathBuf,
    cache_path: PathBuf,
    policy: Arc<Mutex<Policy>>,
    scope: Option<SavedConnection>,
    tabs: Tabs,
    context: Option<RequestContext>,
    ready: bool,
    pending: Vec<ViewRequest>,
    removing: bool,
    storage_revision: u64,
}
#[derive(Clone)]
struct View {
    profile: String,
    generation: u64,
    request: ViewRequest,
    browser: Option<Browser>,
    failure: Option<NativeFailure>,
    creation_pending: bool,
    closing: bool,
    view_id: String,
}
#[derive(Default)]
struct TabReplacement {
    requests: Vec<ViewRequest>,
    closing: Vec<Browser>,
}
#[derive(Default)]
struct State {
    profiles: BTreeMap<String, RuntimeProfile>,
    views: BTreeMap<String, View>,
    generation: u64,
    reservations: BTreeMap<String, String>,
    live: usize,
    exit_code: Option<i32>,
    discovery_pending: bool,
    // Native-local page operations are retained by original window owner,
    // profile and operation identity. At most one mutation per profile is in
    // flight.
    page_operations: BTreeMap<String, PageOperation>,
    page_busy: BTreeMap<String, String>,
    page_order: u64,
    tab_shortcuts: BTreeMap<String, (String, String, u64, u8, Instant, String)>,
}
impl State {
    fn select_tab(
        &mut self,
        profile: &str,
        selected: &str,
        mut unmap: impl FnMut(&View) -> Result<()>,
    ) -> Result<TabReplacement> {
        let labels: Vec<String> = self
            .views
            .iter()
            .filter(|(_, v)| v.profile == profile && !v.closing && v.request.tab != selected)
            .map(|(label, _)| label.clone())
            .collect();
        // CloseBrowser is asynchronous. Confirm every superseded child is
        // invisible before dropping any handle or advancing a generation; a
        // failed native unmap keeps the original views available to Hide.
        for label in &labels {
            unmap(&self.views[label])?;
        }
        let mut replacement = TabReplacement::default();
        // Closing a background tab or selecting the current tab must preserve
        // the selected child's page state and native navigation stack.
        for label in labels {
            self.generation = self
                .generation
                .checked_add(1)
                .ok_or(NativeFailure::Stopped)?;
            let generation = self.generation;
            let view = self.views.get_mut(&label).unwrap();
            if let Some(browser) = view.browser.take() {
                replacement.closing.push(browser);
            }
            view.generation = generation;
            view.failure = None;
            view.creation_pending = false;
            view.closing = false;
            view.request.generation = generation;
            view.request.tab = selected.into();
            if !selected.is_empty() {
                replacement.requests.push(view.request.clone());
            }
        }
        Ok(replacement)
    }

    fn exit_when_ready(&self) -> Option<i32> {
        if self.live == 0 && !self.discovery_pending {
            self.exit_code
        } else {
            None
        }
    }
}
#[derive(Clone, Copy)]
enum DiscoveryPass {
    Poll,
    Exit { deadline: Instant },
}
#[derive(Default)]
struct ReservationAttempt {
    cancelled: Mutex<bool>,
}
impl ReservationAttempt {
    fn run(&self, callback: impl FnOnce() -> Result<()>) -> Result<()> {
        let cancelled = self.cancelled.lock().map_err(|_| NativeFailure::Busy)?;
        if *cancelled {
            return Err(NativeFailure::Stopped);
        }
        callback()
    }

    fn wait(
        &self,
        receiver: std::sync::mpsc::Receiver<Result<()>>,
        timeout: Duration,
    ) -> Result<()> {
        match receiver.recv_timeout(timeout) {
            Ok(result) => result,
            Err(_) => {
                *self.cancelled.lock().map_err(|_| NativeFailure::Busy)? = true;
                Err(NativeFailure::Stopped)
            }
        }
    }
}
pub struct BrowserHost {
    root: PathBuf,
    profile_storage: BrowserStorage,
    connector: Arc<Connector>,
    observer: Arc<Connector>,
    state: Mutex<State>,
    stopping: AtomicBool,
    join: Mutex<Option<thread::JoinHandle<()>>>,
    // Disk serialization is independent of the short native-state lock. No UI
    // callback waits for directory reads, atomic replacement or fsync.
    storage: Mutex<()>,
    // Workers fence the final replacement against UI reservation publication.
    // The UI callback never acquires this gate or waits for filesystem work.
    publication: Mutex<()>,
    addresses: Mutex<BTreeMap<String, (String, ViewRequest, String)>>,
    address_running: AtomicBool,
    address_join: Mutex<Option<thread::JoinHandle<()>>>,
}
impl BrowserHost {
    pub fn new(root: PathBuf, connector: Arc<Connector>, mode: BrowserStorageMode) -> Result<Self> {
        let profile_storage = BrowserStorage::open(root.clone(), mode)?;
        browser::private_dir(&root.join("profiles"))?;
        browser::private_dir(&root.join("removals"))?;
        browser::private_dir(&root.join("forgotten"))?;
        Ok(Self {
            root,
            profile_storage,
            observer: Arc::new(connector.browser_observer()?),
            connector,
            state: Mutex::new(State::default()),
            stopping: AtomicBool::new(false),
            join: Mutex::new(None),
            storage: Mutex::new(()),
            publication: Mutex::new(()),
            addresses: Mutex::new(BTreeMap::new()),
            address_running: AtomicBool::new(false),
            address_join: Mutex::new(None),
        })
    }

    // UI loop only: release the superseded child before asynchronous authority
    // reads can fail. Removing its view also rejects late creation callbacks.
    fn reserve(&self, window: &str, view_id: &str) -> Result<()> {
        self.reserve_with(window, view_id, unmap_view)
    }

    fn reserve_with(
        &self,
        window: &str,
        view_id: &str,
        mut unmap: impl FnMut(&View) -> Result<()>,
    ) -> Result<()> {
        canonical_id(view_id)?;
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        if let Some(previous) = state.views.get(window) {
            unmap(previous)?;
        }
        state.reservations.insert(window.into(), view_id.into());
        let previous = state.views.remove(window);
        drop(state);
        if let Some(browser) = previous.and_then(|view| view.browser)
            && let Some(host) = browser.host()
        {
            host.close_browser(1);
        }
        Ok(())
    }

    pub fn cache_root(&self) -> PathBuf {
        self.profile_storage.cache_root()
    }

    // Blocking worker only. Hold the publication fence until the UI callback
    // accepts the reservation, before starting asynchronous authority reads.
    pub fn reserve_on_worker(
        self: &Arc<Self>,
        app: AppHandle<CefRuntime>,
        window: String,
        view_id: String,
    ) -> Result<()> {
        let _publication = self.publication.lock().map_err(|_| NativeFailure::Busy)?;
        let host = Arc::clone(self);
        let (send, receive) = std::sync::mpsc::sync_channel(1);
        let attempt = Arc::new(ReservationAttempt::default());
        let callback = Arc::clone(&attempt);
        app.run_on_main_thread(move || {
            let _ = send.send(callback.run(|| host.reserve(&window, &view_id)));
        })
        .map_err(|_| NativeFailure::SidecarFailed)?;
        // Exit may stop the UI loop before this callback runs. Release the
        // worker fence within a bound and invalidate that late callback, so the
        // address-worker join cannot wait forever after native shutdown.
        attempt.wait(receive, Duration::from_secs(5))
    }

    pub fn begin_exit(&self, code: i32) -> bool {
        // Close address acceptance under its short queue gate. Already accepted
        // callbacks remain owned by the tracked worker; the UI never waits for
        // I/O.
        {
            let _acceptance = self.addresses.lock();
            self.stopping.store(true, Ordering::Release);
        }
        if let Ok(mut state) = self.state.lock() {
            state.exit_code = Some(code);
        }
        self.close_all();
        self.state
            .lock()
            .map(|s| s.live > 0 || s.discovery_pending)
            .unwrap_or(true)
    }

    pub fn start(self: &Arc<Self>, app: AppHandle<CefRuntime>) {
        self.state.lock().unwrap().discovery_pending = true;
        let host = Arc::clone(self);
        let thread = thread::spawn(move || {
            let close_scope = |scope: SavedConnection| {
                let copy = Arc::clone(&host);
                let _ = app.run_on_main_thread(move || copy.close_scope(&scope));
            };
            let close_profile = |id: String| {
                let copy = Arc::clone(&host);
                let _ = app.run_on_main_thread(move || copy.close_profile(&id));
            };
            while !host.stopping.load(Ordering::Acquire) {
                host.discover_removals(DiscoveryPass::Poll, close_scope, close_profile);
                for _ in 0..50 {
                    if host.stopping.load(Ordering::Acquire) {
                        break;
                    }
                    thread::sleep(Duration::from_millis(100));
                }
            }
            // Quit may arrive in the polling sleep after a new account
            // deletion. Keep the event loop alive through one final
            // worker discovery pass.
            host.discover_removals(
                DiscoveryPass::Exit {
                    deadline: Instant::now() + Duration::from_secs(8),
                },
                close_scope,
                close_profile,
            );
            if let Some(code) = host.finish_discovery() {
                app.exit(code);
            }
        });
        *self.join.lock().unwrap() = Some(thread);
    }

    fn can_discover(&self, pass: DiscoveryPass) -> bool {
        match pass {
            DiscoveryPass::Poll => !self.stopping.load(Ordering::Acquire),
            DiscoveryPass::Exit { deadline } => Instant::now() < deadline,
        }
    }

    fn discover_removals(
        &self,
        pass: DiscoveryPass,
        mut close_scope: impl FnMut(SavedConnection),
        mut close_profile: impl FnMut(String),
    ) {
        // Completed offline connection tombstones preserve their original scope
        // after credential destruction, including removals initiated by the
        // CLI.
        let mut after = String::new();
        for _ in 0..16 {
            if !self.can_discover(pass) {
                break;
            }
            let Ok(page) = self.observer.removed_connections(&after) else {
                break;
            };
            for scope in page.connections {
                if !self.can_discover(pass) {
                    break;
                }
                if let Err(code) = self.prepare_forget(&scope) {
                    tracing::warn!(operation = "browser_scope_removal", ?code);
                    continue;
                }
                close_scope(scope);
            }
            after = page.next_after;
            if after.is_empty() {
                break;
            }
        }
        // Include saved clients whose windows have never been opened. Each
        // child retains the observer's two-second deadline; no new read
        // starts after the final pass's budget expires, even when
        // endpoints remain offline.
        let mut scopes = vec![None];
        if self.can_discover(pass)
            && let Ok(saved) = self.observer.saved_connections()
        {
            scopes.extend(saved.into_iter().map(Some));
        }
        for scope in scopes {
            let mut page = String::new();
            for _ in 0..100 {
                if !self.can_discover(pass) {
                    break;
                }
                let result = self.observer.browser_query(
                    scope.as_ref(),
                    &[
                        "list".into(),
                        "--page-size".into(),
                        "50".into(),
                        "--page-token".into(),
                        page.clone().into(),
                    ],
                );
                let Ok(result) = result else { break };
                let Some(profiles) = result.get("profiles").and_then(|v| v.as_array()) else {
                    break;
                };
                for value in profiles {
                    if !self.can_discover(pass) {
                        break;
                    }
                    let Ok(record) = serde_json::from_value::<ProfileRecord>(value.clone()) else {
                        continue;
                    };
                    if record.validate().is_err()
                        || record.data.state != ProfileState::RemovalPending
                    {
                        continue;
                    }
                    if let Err(code) = self.prepare_removal(record.clone(), scope.clone()) {
                        tracing::warn!(operation = "browser_removal", ?code);
                        continue;
                    }
                    close_profile(record.id);
                }
                page = result
                    .get("next_page_token")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string();
                if page.is_empty() {
                    break;
                }
            }
        }
        if matches!(pass, DiscoveryPass::Exit { .. }) {
            tracing::info!(
                operation = "browser_removal_discovery",
                state = if self.can_discover(pass) {
                    "finished"
                } else {
                    "budget-deferred"
                }
            );
        }
    }

    fn finish_discovery(&self) -> Option<i32> {
        let mut state = self.state.lock().ok()?;
        state.discovery_pending = false;
        state.exit_when_ready()
    }

    pub fn stop(&self) {
        self.stopping.store(true, Ordering::Release);
        if let Some(join) = self.join.lock().unwrap().take() {
            let _ = join.join();
        }
    }

    #[cfg(test)]
    fn prepare_fixture_open(
        &self,
        window: &str,
        view_id: &str,
        scope: Option<SavedConnection>,
        record: ProfileRecord,
        url: &str,
    ) -> Result<()> {
        // Fixtures request their page explicitly, as production does through
        // Create; presentation and restoration never do.
        let request = PageRequest {
            action: PageAction::Create,
            operation_id: Some(uuid::Uuid::now_v7().to_string()),
            url: Some(url.into()),
            page_id: None,
        };
        self.prepare_pages(
            "fixture-owner",
            scope.clone(),
            &record,
            &request,
            |staged, committed| staged.publish_with_commit(committed),
        )?;
        let key = operation_key(
            "fixture-owner",
            &record.id,
            request.operation_id.as_deref().unwrap(),
        );
        let page_id = {
            let mut state = self.state.lock().unwrap();
            state.page_busy.remove(&record.id);
            state.page_operations[&key].receipt.page_id.clone()
        };
        self.prepare_open(window, view_id, scope, record, &page_id)
    }

    /// Prepares presentation of one retained page. Presentation never creates
    /// a page: an unknown page or an empty inventory is rejected, and the
    /// shared profile selection moves to the requested page.
    pub fn prepare_open(
        &self,
        window: &str,
        view_id: &str,
        scope: Option<SavedConnection>,
        record: ProfileRecord,
        page_id: &str,
    ) -> Result<()> {
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        record.validate()?;
        canonical_id(page_id)?;
        if self.stopping.load(Ordering::Acquire) || record.data.state != ProfileState::Active {
            return Err(NativeFailure::Stopped);
        }
        self.check_profile_retained(&record)?;
        let local_endpoint = self.connector.current_runtime_endpoint()?;
        let endpoint = scope
            .as_ref()
            .map(|s| s.endpoint.as_str())
            .unwrap_or(&local_endpoint);
        let mut policy = Policy::empty(endpoint)?;
        policy.protect_local_runtime(&local_endpoint)?;
        let existing = {
            let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            if state.reservations.get(window).map(String::as_str) != Some(view_id) {
                return Err(NativeFailure::Stopped);
            }
            if let Some(profile) = state.profiles.get(&record.id) {
                if profile.removing
                    || profile
                        .scope
                        .as_ref()
                        .map(|s| (&s.server_id, &s.device_id, &s.endpoint))
                        != scope
                            .as_ref()
                            .map(|s| (&s.server_id, &s.device_id, &s.endpoint))
                {
                    return Err(NativeFailure::Stopped);
                }
                policy = profile
                    .policy
                    .lock()
                    .map_err(|_| NativeFailure::Busy)?
                    .clone();
                Some((profile.path.clone(), profile.tabs.clone()))
            } else {
                if state.profiles.len() >= 64 {
                    return Err(NativeFailure::Busy);
                }
                None
            }
        };
        let (path, mut tabs) = if let Some(existing) = existing {
            existing
        } else {
            let path = browser::inspect_profile_path(&self.root.join("profiles"), &record)?;
            let tabs = if path.join("tabs.json").exists() {
                read_json(&path.join("tabs.json"))?
            } else {
                Tabs::default()
            };
            (path, tabs)
        };
        for tab in &tabs.tabs {
            policy = policy.with_explicit(&tab.url)?;
        }
        tabs.validate(&policy)?;
        if !tabs.tabs.iter().any(|tab| tab.id == page_id) {
            return Err(NativeFailure::InvalidInput);
        }
        // Only a retained page can reach here, so its private directories
        // exist.
        browser::profile_path(&self.root.join("profiles"), &record)?;
        // Moving the shared selection is the only metadata a presentation may
        // write; it never adds a page.
        let staged = if tabs.selected != page_id {
            tabs.selected = page_id.into();
            Some(browser::stage_private(&path.join("tabs.json"), &tabs)?)
        } else {
            None
        };
        let cache_path = self.profile_storage.prepare_profile(&record)?;
        let _publication = self.publication.lock().map_err(|_| NativeFailure::Busy)?;
        let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopping.load(Ordering::Acquire)
            || state.reservations.get(window).map(String::as_str) != Some(view_id)
        {
            return Err(NativeFailure::Stopped);
        }
        if state
            .profiles
            .get(&record.id)
            .is_some_and(|profile| profile.removing)
        {
            return Err(NativeFailure::Stopped);
        }
        drop(state);
        if let Some(staged) = staged {
            staged.publish()?;
        }
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if let Some(profile) = state.profiles.get_mut(&record.id) {
            if profile.removing {
                return Err(NativeFailure::Stopped);
            }
            profile.tabs = tabs;
            *profile.policy.lock().map_err(|_| NativeFailure::Busy)? = policy;
            profile.storage_revision = profile
                .storage_revision
                .checked_add(1)
                .ok_or(NativeFailure::Stopped)?;
        } else {
            state.profiles.insert(
                record.id.clone(),
                RuntimeProfile {
                    record,
                    path,
                    cache_path,
                    policy: Arc::new(Mutex::new(policy)),
                    scope,
                    tabs,
                    context: None,
                    ready: false,
                    pending: vec![],
                    removing: false,
                    storage_revision: 1,
                },
            );
        }
        Ok(())
    }

    // A forgotten scope or a persisted removal intent permanently denies
    // restoration, presentation and page operations for the profile.
    fn check_profile_retained(&self, record: &ProfileRecord) -> Result<()> {
        if self
            .forgotten_path(&record.data.server_id, &record.data.device_id)?
            .exists()
            || self
                .root
                .join("removals")
                .join(format!("{}.json", record.id))
                .exists()
        {
            return Err(NativeFailure::Stopped);
        }
        Ok(())
    }

    // Native-local page operations. They never present a child: Inventory and
    // Observe read, Create and Close change the profile's local page metadata
    // under one serialized, receipt-backed operation.
    fn page_policy(&self, scope: Option<&SavedConnection>, tabs: &Tabs) -> Result<Policy> {
        let local_endpoint = self.connector.current_runtime_endpoint()?;
        let endpoint = scope
            .map(|s| s.endpoint.as_str())
            .unwrap_or(&local_endpoint);
        let mut policy = Policy::empty(endpoint)?;
        policy.protect_local_runtime(&local_endpoint)?;
        for tab in &tabs.tabs {
            policy = policy.with_explicit(&tab.url)?;
        }
        tabs.validate(&policy)?;
        Ok(policy)
    }

    fn same_scope(profile: &RuntimeProfile, scope: Option<&SavedConnection>) -> bool {
        profile
            .scope
            .as_ref()
            .map(|s| (&s.server_id, &s.device_id, &s.endpoint))
            == scope.map(|s| (&s.server_id, &s.device_id, &s.endpoint))
    }

    fn inventory_state(
        &self,
        scope: Option<&SavedConnection>,
        record: &ProfileRecord,
    ) -> Result<BrowserState> {
        {
            let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            if let Some(profile) = state.profiles.get(&record.id) {
                if !Self::same_scope(profile, scope) {
                    return Err(NativeFailure::Stopped);
                }
                return Ok(BrowserState {
                    tabs: profile.tabs.clone(),
                    removal_pending: profile.removing,
                });
            }
        }
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        self.check_profile_retained(record)?;
        // Restoration is read-only: it neither creates directories nor seeds
        // an empty inventory.
        let path = browser::inspect_profile_path(&self.root.join("profiles"), record)?;
        let file = path.join("tabs.json");
        let tabs = if file.exists() {
            read_json(&file)?
        } else {
            Tabs::default()
        };
        self.page_policy(scope, &tabs)?;
        Ok(BrowserState {
            tabs,
            removal_pending: false,
        })
    }

    fn page_receipt(&self, key: &str) -> Option<PageReceipt> {
        self.state
            .lock()
            .ok()?
            .page_operations
            .get(key)
            .map(|operation| operation.receipt.clone())
    }

    fn settle_page_operation(operation: &mut PageOperation) {
        let receipt = &mut operation.receipt;
        if !receipt.metadata_committed {
            receipt.phase = PagePhase::Failed;
        } else if operation.durability.is_none() && receipt.native_cleanup_complete {
            receipt.phase = PagePhase::Completed;
            receipt.failure = None;
        } else {
            receipt.phase = PagePhase::CleanupPending;
        }
    }

    // Retry only the directory fsync that followed a successful replacement.
    fn settle_durability(&self, key: &str) {
        let Some(path) = self.state.lock().ok().and_then(|state| {
            state
                .page_operations
                .get(key)
                .and_then(|operation| operation.durability.clone())
        }) else {
            return;
        };
        if browser::sync_private_parent(&path).is_ok()
            && let Ok(mut state) = self.state.lock()
            && let Some(operation) = state.page_operations.get_mut(key)
        {
            operation.durability = None;
            Self::settle_page_operation(operation);
        }
    }

    /// Blocking worker only. Inventory reads local pages without creating
    /// storage; Observe also returns the original operation's retained receipt
    /// and never repeats its mutation.
    pub fn read_pages(
        &self,
        owner: &str,
        scope: Option<&SavedConnection>,
        record: &ProfileRecord,
        request: &PageRequest,
    ) -> Result<PageInventory> {
        record.validate()?;
        if self.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let key = match request.action {
            PageAction::Inventory => None,
            PageAction::Observe => {
                let operation = request
                    .operation_id
                    .as_deref()
                    .ok_or(NativeFailure::InvalidInput)?;
                canonical_id(operation)?;
                Some(operation_key(owner, &record.id, operation))
            }
            PageAction::Create | PageAction::Close => return Err(NativeFailure::InvalidInput),
        };
        if let Some(key) = &key {
            self.settle_durability(key);
        }
        let state = if record.data.state == ProfileState::Active {
            self.inventory_state(scope, record)?
        } else {
            BrowserState {
                tabs: Tabs::default(),
                removal_pending: true,
            }
        };
        Ok(PageInventory {
            state,
            receipt: key.and_then(|key| self.page_receipt(&key)),
        })
    }

    /// Blocking worker only. Admission records the original operation before
    /// any storage work; every later failure is retained on its receipt, so a
    /// lost reply is settled by observation rather than a repeated mutation.
    /// `publish` is called under the native publication fence.
    pub fn prepare_pages(
        &self,
        owner: &str,
        scope: Option<SavedConnection>,
        record: &ProfileRecord,
        request: &PageRequest,
        publish: impl FnOnce(browser::PrivateWrite, &mut bool) -> Result<()>,
    ) -> Result<PageAdmission> {
        record.validate()?;
        let operation_id = request
            .operation_id
            .as_deref()
            .ok_or(NativeFailure::InvalidInput)?;
        canonical_id(operation_id)?;
        match request.action {
            PageAction::Create => {
                if request.page_id.is_some()
                    || request
                        .url
                        .as_deref()
                        .is_none_or(|url| url.is_empty() || url.len() > 8192)
                {
                    return Err(NativeFailure::InvalidInput);
                }
            }
            PageAction::Close => {
                if request.url.is_some() {
                    return Err(NativeFailure::InvalidInput);
                }
                canonical_id(
                    request
                        .page_id
                        .as_deref()
                        .ok_or(NativeFailure::InvalidInput)?,
                )?;
            }
            PageAction::Inventory | PageAction::Observe => {
                return Err(NativeFailure::InvalidInput);
            }
        }
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopping.load(Ordering::Acquire) || record.data.state != ProfileState::Active {
            return Err(NativeFailure::Stopped);
        }
        self.check_profile_retained(record)?;
        let key = operation_key(owner, &record.id, operation_id);
        let loaded = {
            let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            if let Some(existing) = state.page_operations.get(&key) {
                if existing.request != *request {
                    return Err(NativeFailure::InvalidInput);
                }
                return Ok(PageAdmission::Replayed);
            }
            match state.profiles.get(&record.id) {
                Some(profile) if profile.removing || !Self::same_scope(profile, scope.as_ref()) => {
                    return Err(NativeFailure::Stopped);
                }
                Some(profile) => Some((
                    profile.path.clone(),
                    profile.tabs.clone(),
                    profile
                        .policy
                        .lock()
                        .map_err(|_| NativeFailure::Busy)?
                        .clone(),
                )),
                None => None,
            }
        };
        let (path, mut tabs, mut policy) = if let Some(loaded) = loaded {
            loaded
        } else {
            // Only an explicit Create may initialize storage.
            let profiles = self.root.join("profiles");
            let path = if request.action == PageAction::Create {
                browser::profile_path(&profiles, record)?
            } else {
                browser::inspect_profile_path(&profiles, record)?
            };
            let file = path.join("tabs.json");
            let tabs = if file.exists() {
                read_json(&file)?
            } else {
                Tabs::default()
            };
            let policy = self.page_policy(scope.as_ref(), &tabs)?;
            (path, tabs, policy)
        };
        let page_id = request
            .page_id
            .clone()
            .unwrap_or_else(|| uuid::Uuid::now_v7().to_string());
        {
            let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            if state.page_busy.contains_key(&record.id) {
                return Err(NativeFailure::Busy);
            }
            if state.page_operations.len() >= OPERATION_LIMIT {
                let oldest = state
                    .page_operations
                    .iter()
                    .filter(|(_, operation)| {
                        matches!(
                            operation.receipt.phase,
                            PagePhase::Completed | PagePhase::Failed
                        )
                    })
                    .min_by_key(|(_, operation)| operation.order)
                    .map(|(key, _)| key.clone())
                    .ok_or(NativeFailure::Busy)?;
                state.page_operations.remove(&oldest);
            }
            state.page_order = state
                .page_order
                .checked_add(1)
                .ok_or(NativeFailure::Stopped)?;
            let order = state.page_order;
            state.page_operations.insert(
                key.clone(),
                PageOperation {
                    request: request.clone(),
                    receipt: PageReceipt {
                        operation_id: operation_id.into(),
                        page_id: page_id.clone(),
                        phase: PagePhase::Accepted,
                        metadata_committed: false,
                        native_cleanup_complete: false,
                        failure: None,
                    },
                    tabs: tabs.clone(),
                    order,
                    durability: None,
                },
            );
            state.page_busy.insert(record.id.clone(), key.clone());
        }
        let outcome = self.mutate_pages(
            &record.id,
            request,
            &page_id,
            &path,
            &mut tabs,
            &mut policy,
            publish,
        );
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let committed = outcome.as_ref().is_ok_and(|committed| committed.is_some());
        if committed
            && let Some(profile) = state.profiles.get_mut(&record.id)
            && !profile.removing
        {
            profile.tabs = tabs.clone();
            if let Ok(mut current) = profile.policy.lock() {
                *current = policy;
            }
            profile.storage_revision = profile.storage_revision.saturating_add(1);
        }
        if let Some(operation) = state.page_operations.get_mut(&key) {
            match outcome {
                Ok(durability) => {
                    operation.receipt.metadata_committed = true;
                    if let Some((target, code)) = durability.flatten() {
                        operation.durability = Some(target);
                        operation.receipt.failure = Some(code);
                    }
                    operation.tabs = tabs;
                }
                Err(code) => operation.receipt.failure = Some(code),
            }
            Self::settle_page_operation(operation);
        }
        Ok(PageAdmission::Admitted)
    }

    // Returns Ok(Some(None)) for a replaced file, Ok(Some(Some(..))) when the
    // directory fsync failed after replacement, and Ok(None) for an idempotent
    // Close of an already absent page (committed, nothing to write).
    #[allow(
        clippy::type_complexity,
        clippy::too_many_arguments,
        reason = "Private helper keeps the three publication outcomes explicit"
    )]
    fn mutate_pages(
        &self,
        profile: &str,
        request: &PageRequest,
        page_id: &str,
        path: &std::path::Path,
        tabs: &mut Tabs,
        policy: &mut Policy,
        publish: impl FnOnce(browser::PrivateWrite, &mut bool) -> Result<()>,
    ) -> Result<Option<Option<(PathBuf, NativeFailure)>>> {
        match request.action {
            PageAction::Create => {
                if tabs.tabs.len() >= PAGE_LIMIT {
                    return Err(NativeFailure::InvalidInput);
                }
                let url = request.url.as_deref().ok_or(NativeFailure::InvalidInput)?;
                *policy = policy.with_explicit(url)?;
                tabs.tabs.push(Tab {
                    id: page_id.into(),
                    url: url.into(),
                });
                tabs.selected = page_id.into();
            }
            PageAction::Close => {
                let Some(index) = tabs.tabs.iter().position(|tab| tab.id == page_id) else {
                    // Closure is idempotent: the page is already absent.
                    return Ok(None);
                };
                tabs.tabs.remove(index);
                if tabs.selected == page_id {
                    tabs.selected = tabs
                        .tabs
                        .get(index.saturating_sub(1))
                        .map(|tab| tab.id.clone())
                        .unwrap_or_default();
                }
            }
            PageAction::Inventory | PageAction::Observe => {
                return Err(NativeFailure::InvalidInput);
            }
        }
        let file = path.join("tabs.json");
        let staged = browser::stage_private(&file, tabs)?;
        let _publication = self.publication.lock().map_err(|_| NativeFailure::Busy)?;
        {
            let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            if self.stopping.load(Ordering::Acquire)
                || state.profiles.get(profile).is_some_and(|p| p.removing)
            {
                return Err(NativeFailure::Stopped);
            }
        }
        let mut committed = false;
        match publish(staged, &mut committed) {
            Ok(()) => Ok(Some(None)),
            Err(code) if committed => Ok(Some(Some((file, code)))),
            Err(code) => Err(code),
        }
    }

    /// UI loop only. Reconciles live shared children with the committed page
    /// selection and settles the original receipt. It performs no filesystem
    /// operation and never repeats the metadata mutation.
    pub fn finish_pages(
        self: &Arc<Self>,
        app: &AppHandle<CefRuntime>,
        owner: &str,
        profile: &str,
        operation_id: &str,
    ) -> Result<PageInventory> {
        let key = operation_key(owner, profile, operation_id);
        let committed = self
            .state
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .page_operations
            .get(&key)
            .ok_or(NativeFailure::InvalidEvidence)?
            .receipt
            .metadata_committed;
        let cleanup = if committed {
            self.reconcile_children(app, profile)
        } else {
            Ok(())
        };
        self.complete_page_cleanup(&key, profile, committed, cleanup)
    }

    // Records the result of native child reconciliation on the original
    // receipt, releases the profile's mutation slot and returns the inventory.
    fn complete_page_cleanup(
        &self,
        key: &str,
        profile: &str,
        committed: bool,
        cleanup: Result<()>,
    ) -> Result<PageInventory> {
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if state.page_busy.get(profile).map(String::as_str) == Some(key) {
            state.page_busy.remove(profile);
        }
        let removing = state.profiles.get(profile).is_some_and(|p| p.removing);
        let loaded = state.profiles.get(profile).map(|p| p.tabs.clone());
        let operation = state
            .page_operations
            .get_mut(key)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if committed {
            operation.receipt.native_cleanup_complete = cleanup.is_ok();
            if let Err(code) = cleanup {
                operation.receipt.failure = Some(code);
            }
            Self::settle_page_operation(operation);
        }
        Ok(PageInventory {
            state: BrowserState {
                tabs: loaded.unwrap_or_else(|| operation.tabs.clone()),
                removal_pending: removing,
            },
            receipt: Some(operation.receipt.clone()),
        })
    }

    /// Releases the profile's mutation slot when the UI loop is unavailable. A
    /// committed operation keeps its receipt and awaits native cleanup.
    pub fn release_pages(&self, owner: &str, profile: &str, operation_id: &str) {
        let key = operation_key(owner, profile, operation_id);
        let Ok(mut state) = self.state.lock() else {
            return;
        };
        if state.page_busy.get(profile) == Some(&key) {
            state.page_busy.remove(profile);
        }
        if let Some(operation) = state.page_operations.get_mut(&key)
            && operation.receipt.phase == PagePhase::Accepted
        {
            operation.receipt.failure = Some(NativeFailure::SidecarFailed);
            Self::settle_page_operation(operation);
        }
    }

    // Replace only the children that no longer show the shared selection; a
    // child already on it keeps its page state. An inactive page has no child.
    fn reconcile_children(
        self: &Arc<Self>,
        app: &AppHandle<CefRuntime>,
        profile: &str,
    ) -> Result<()> {
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let Some(p) = state.profiles.get(profile).filter(|p| !p.removing) else {
            return Ok(());
        };
        let selected = p.tabs.selected.clone();
        let context = p.context.clone();
        if !state
            .views
            .values()
            .any(|v| v.profile == profile && !v.closing && v.request.tab != selected)
        {
            return Ok(());
        }
        let context = context.ok_or(NativeFailure::Busy)?;
        let replacement = state.select_tab(profile, &selected, unmap_view)?;
        drop(state);
        for browser in replacement.closing {
            if let Some(host) = browser.host() {
                host.close_browser(1);
            }
        }
        self.recreate_children(profile, &replacement.requests, |request| {
            create_child(self, app, profile.into(), request.clone(), context.clone())
        })
    }

    // Called on the UI thread only after worker preparation and a second
    // trusted document check. This path performs no filesystem operations.
    pub fn open(
        self: &Arc<Self>,
        app: &AppHandle<CefRuntime>,
        window: &WebviewWindow<CefRuntime>,
        scope: Option<SavedConnection>,
        record: ProfileRecord,
        bounds: Bounds,
        view_id: String,
    ) -> Result<BrowserState> {
        if self.stopping.load(Ordering::Acquire) || !bounds.valid() {
            return Err(NativeFailure::InvalidInput);
        }
        record.validate()?;
        if record.data.state != ProfileState::Active {
            return Err(NativeFailure::Stopped);
        }
        let parent = parent(window)?;
        let scale = window
            .scale_factor()
            .map_err(|_| NativeFailure::SidecarFailed)?;
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if state.reservations.get(window.label()) != Some(&view_id) {
            return Err(NativeFailure::Stopped);
        }
        if let Some(view) = state.views.get(window.label()) {
            unmap_view(view)?;
        }
        let previous = state
            .views
            .remove(window.label())
            .and_then(|view| view.browser);
        drop(state);
        if let Some(browser) = previous
            && let Some(host) = browser.host()
        {
            host.close_browser(1);
        }
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let p = state
            .profiles
            .get_mut(&record.id)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if p.scope
            .as_ref()
            .map(|s| (&s.server_id, &s.device_id, &s.endpoint))
            != scope
                .as_ref()
                .map(|s| (&s.server_id, &s.device_id, &s.endpoint))
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        if p.removing {
            return Err(NativeFailure::Stopped);
        };
        let tab = p.tabs.selected.clone();
        // Presentation restores an existing page; an empty inventory has none.
        if tab.is_empty() || !p.tabs.tabs.iter().any(|page| page.id == tab) {
            return Err(NativeFailure::InvalidInput);
        }
        state.generation = state
            .generation
            .checked_add(1)
            .ok_or(NativeFailure::Stopped)?;
        let generation = state.generation;
        let request = ViewRequest {
            window: window.label().into(),
            parent,
            bounds,
            scale,
            tab,
            generation,
        };
        state.views.insert(
            window.label().into(),
            View {
                profile: record.id.clone(),
                generation,
                request: request.clone(),
                browser: None,
                failure: None,
                creation_pending: false,
                closing: false,
                view_id,
            },
        );
        let p = state.profiles.get_mut(&record.id).unwrap();
        let result = BrowserState {
            tabs: p.tabs.clone(),
            removal_pending: false,
        };
        if p.ready {
            let context = p.context.clone().ok_or(NativeFailure::InvalidEvidence)?;
            drop(state);
            create(self, app, record.id.clone(), request, context)?;
        } else {
            p.pending.push(request);
            if p.context.is_none() {
                let settings = RequestContextSettings {
                    cache_path: p.cache_path.to_string_lossy().as_ref().into(),
                    persist_session_cookies: 1,
                    ..Default::default()
                };
                let mut handler =
                    ContextReady::new(Arc::clone(self), app.clone(), record.id.clone());
                p.context = request_context_create_context(Some(&settings), Some(&mut handler));
                if p.context.is_none() {
                    return Err(NativeFailure::SidecarFailed);
                }
            }
        }
        tracing::info!(operation = "browser_view", state = "opening");
        Ok(result)
    }

    pub fn prepare_control(
        &self,
        window: &str,
        profile: &str,
        view_id: &str,
        action: Action,
        url: Option<&str>,
        tab: Option<&str>,
    ) -> Result<u64> {
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        let (path, mut tabs, mut policy) = {
            let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            let view = state
                .views
                .get(window)
                .ok_or(NativeFailure::InvalidEvidence)?;
            if view.profile != profile
                || view.view_id != view_id
                || state.reservations.get(window).map(String::as_str) != Some(view_id)
            {
                return Err(NativeFailure::Stopped);
            }
            if let Some(code) = view.failure {
                return Err(code);
            }
            if view.closing {
                return Err(NativeFailure::Stopped);
            }
            let p = state
                .profiles
                .get(profile)
                .ok_or(NativeFailure::InvalidEvidence)?;
            if p.removing || self.stopping.load(Ordering::Acquire) {
                return Err(NativeFailure::Stopped);
            }
            (
                p.path.clone(),
                p.tabs.clone(),
                p.policy.lock().map_err(|_| NativeFailure::Busy)?.clone(),
            )
        };
        match action {
            Action::Navigate => {
                let url = url.ok_or(NativeFailure::InvalidInput)?;
                policy = policy.with_explicit(url)?;
            }
            Action::SelectTab => {
                let id = tab.ok_or(NativeFailure::InvalidInput)?;
                if !tabs.tabs.iter().any(|t| t.id == id) {
                    return Err(NativeFailure::InvalidInput);
                }
                tabs.selected = id.into();
            }
            // Page creation and closure are explicit receipt-backed
            // operations; no presentation control can add or remove a page.
            _ => {}
        }
        let staged = if matches!(action, Action::SelectTab) {
            Some(browser::stage_private(&path.join("tabs.json"), &tabs)?)
        } else {
            None
        };
        let _publication = self.publication.lock().map_err(|_| NativeFailure::Busy)?;
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if state
            .views
            .get(window)
            .is_none_or(|v| v.profile != profile || v.view_id != view_id)
            || state.reservations.get(window).map(String::as_str) != Some(view_id)
            || self.stopping.load(Ordering::Acquire)
        {
            return Err(NativeFailure::Stopped);
        }
        let p = state
            .profiles
            .get_mut(profile)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if p.removing {
            return Err(NativeFailure::Stopped);
        }
        let revision = p
            .storage_revision
            .checked_add(1)
            .ok_or(NativeFailure::Stopped)?;
        drop(state);
        if let Some(staged) = staged {
            staged.publish()?;
        }
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let p = state
            .profiles
            .get_mut(profile)
            .ok_or(NativeFailure::Stopped)?;
        p.tabs = tabs;
        *p.policy.lock().map_err(|_| NativeFailure::Busy)? = policy;
        p.storage_revision = revision;
        Ok(p.storage_revision)
    }

    pub fn control(
        self: &Arc<Self>,
        app: &AppHandle<CefRuntime>,
        window: &WebviewWindow<CefRuntime>,
        control: Control,
    ) -> Result<BrowserState> {
        let Control {
            profile,
            view_id,
            action,
            url,
            prepared_revision,
            bounds,
        } = control;
        let profile = profile.as_str();
        let view_id = view_id.as_str();
        tracing::debug!(operation = "browser_control", ?action);
        if matches!(action, Action::Hide) {
            return self.hide(window.label(), profile, view_id);
        }
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let view = state
            .views
            .get(window.label())
            .ok_or(NativeFailure::InvalidEvidence)?;
        if view.profile != profile
            || view.view_id != view_id
            || state
                .reservations
                .get(window.label())
                .is_some_and(|id| id != view_id)
        {
            return Err(NativeFailure::PermissionDenied);
        }
        if view.closing {
            return Err(NativeFailure::Stopped);
        }
        let p = state
            .profiles
            .get(profile)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if p.removing {
            return Ok(BrowserState {
                tabs: p.tabs.clone(),
                removal_pending: true,
            });
        }
        if !matches!(action, Action::Hide)
            && let Some(code) = view.failure
        {
            return Err(code);
        }
        if prepared_revision.is_some_and(|revision| revision != p.storage_revision) {
            return Err(NativeFailure::Stopped);
        }
        let browser = view.browser.clone();
        match action {
            Action::Back => {
                if let Some(b) = browser {
                    b.go_back();
                }
            }
            Action::Forward => {
                if let Some(b) = browser {
                    b.go_forward();
                }
            }
            Action::Reload => {
                if let Some(b) = browser {
                    b.reload();
                }
            }
            Action::Navigate => {
                let url = url.ok_or(NativeFailure::InvalidInput)?;
                if let Some(b) = browser {
                    if let Some(f) = b.main_frame() {
                        f.load_url(Some(&url.as_str().into()));
                    }
                } else {
                    return Err(NativeFailure::Busy);
                }
            }
            Action::Resize => {
                let bounds = bounds
                    .filter(|b| b.valid())
                    .ok_or(NativeFailure::InvalidInput)?;
                let view = state.views.get_mut(window.label()).unwrap();
                view.request.bounds = bounds;
                if let Some(b) = &view.browser
                    && let Some(h) = b.host()
                {
                    position(h.window_handle(), bounds, view.request.scale, true)?;
                }
            }
            Action::Hide => {
                unreachable!("Hide uses exact idempotent cleanup before ordinary controls")
            }
            Action::SelectTab => {
                let p = state.profiles.get_mut(profile).unwrap();
                let context = p.context.clone().ok_or(NativeFailure::Busy)?;
                let selected = p.tabs.selected.clone();
                // Profile tabs are shared: every live user observes the same
                // selected tab.
                let replacement = state.select_tab(profile, &selected, unmap_view)?;
                drop(state);
                for browser in replacement.closing {
                    if let Some(host) = browser.host() {
                        host.close_browser(1);
                    }
                }
                self.recreate_children(profile, &replacement.requests, |request| {
                    create_child(self, app, profile.into(), request.clone(), context.clone())
                })?;
                state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            }
        }
        let p = state
            .profiles
            .get(profile)
            .ok_or(NativeFailure::InvalidEvidence)?;
        Ok(BrowserState {
            tabs: p.tabs.clone(),
            removal_pending: p.removing,
        })
    }

    fn hide(&self, window: &str, profile: &str, view_id: &str) -> Result<BrowserState> {
        self.hide_with(window, profile, view_id, unmap_view)
    }

    fn hide_with(
        &self,
        window: &str,
        profile: &str,
        view_id: &str,
        mut unmap: impl FnMut(&View) -> Result<()>,
    ) -> Result<BrowserState> {
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        // A superseding reservation already unmaps the previous child. Retried
        // cleanup proves that old view is absent without touching its
        // replacement.
        if let Some(view) = state
            .views
            .get_mut(window)
            .filter(|v| v.profile == profile && v.view_id == view_id)
        {
            view.closing = true;
            if view.creation_pending {
                return Err(NativeFailure::Busy);
            }
            // CloseBrowser is asynchronous. Keep ownership on unmap failure;
            // only synchronous native invisibility permits immediate success.
            let hidden = unmap(view);
            let browser = view.browser.clone();
            if hidden.is_ok() {
                state.views.remove(window);
            }
            drop(state);
            if let Some(host) = browser.and_then(|browser| browser.host()) {
                host.close_browser(1);
            }
            hidden?;
        }
        // Cleanup returns no browsing data, including when the supplied profile
        // belongs to another window or the old presentation is already absent.
        Ok(BrowserState {
            tabs: Tabs::default(),
            removal_pending: false,
        })
    }

    fn child_created(&self, profile: &str, request: &ViewRequest, browser: &Browser) -> Result<()> {
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let allowed = !self.stopping.load(Ordering::Acquire)
            && state.profiles.get(profile).is_some_and(|p| !p.removing);
        if allowed
            && let Some(view) = state
                .views
                .get_mut(&request.window)
                .filter(|v| v.profile == profile && v.generation == request.generation)
        {
            view.creation_pending = false;
            view.browser = Some(browser.clone());
            if view.closing {
                let view_id = view.view_id.clone();
                drop(state);
                return self.hide(&request.window, profile, &view_id).map(|_| ());
            }
            let host = browser.host().ok_or(NativeFailure::InvalidEvidence)?;
            return position(
                host.window_handle(),
                view.request.bounds,
                view.request.scale,
                true,
            );
        }
        drop(state);
        hide_browser(browser, request)
    }

    fn child_closed(&self, request: &ViewRequest) -> Option<i32> {
        let mut state = self.state.lock().ok()?;
        if let Some(view) = state
            .views
            .get_mut(&request.window)
            .filter(|v| v.generation == request.generation)
        {
            view.browser = None;
            view.creation_pending = false;
            if view.closing {
                state.views.remove(&request.window);
            }
        }
        state.live = state.live.saturating_sub(1);
        state.exit_when_ready()
    }

    fn finish_child_created(
        &self,
        profile: &str,
        request: &ViewRequest,
        result: Result<()>,
        close_child: impl FnOnce(),
    ) {
        // Initial native geometry can fail after the child has been accepted.
        // Retain its exact failure before asynchronous closure clears the
        // handle.
        if let Err(code) = self.creation_completed(profile, request, result) {
            close_child();
            tracing::warn!(operation = "browser_geometry", ?code);
        }
    }

    pub fn tab_shortcuts(
        &self,
        window: &str,
        profile: &str,
        view_id: &str,
        count: u8,
        token: &str,
    ) -> Result<()> {
        canonical_id(token)?;
        if count > 9 || self.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::InvalidInput);
        }
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let view = state
            .views
            .get(window)
            .filter(|v| {
                v.profile == profile && v.view_id == view_id && !v.closing && v.failure.is_none()
            })
            .ok_or(NativeFailure::InvalidInput)?;
        let generation = view.generation;
        state.tab_shortcuts.insert(
            window.into(),
            (
                profile.into(),
                view_id.into(),
                generation,
                count,
                Instant::now(),
                token.into(),
            ),
        );
        Ok(())
    }

    fn numeric_selection(
        &self,
        profile: &str,
        request: &ViewRequest,
        position: u8,
    ) -> Option<(String, String)> {
        if self.stopping.load(Ordering::Acquire) {
            return None;
        }
        let state = self.state.lock().ok()?;
        let view = state.views.get(&request.window)?;
        let (original_profile, view_id, generation, count, time, token) =
            state.tab_shortcuts.get(&request.window)?;
        if view.closing
            || view.failure.is_some()
            || view.profile != profile
            || original_profile != profile
            || view.view_id != *view_id
            || view.generation != *generation
            || request.generation != *generation
            || request.tab != view.request.tab
            || position == 0
            || position > *count
            || time.elapsed() > Duration::from_millis(750)
            || state.reservations.get(&request.window) != Some(view_id)
        {
            return None;
        }
        Some((view_id.clone(), token.clone()))
    }

    pub fn status(&self, window: &str, profile: &str, view_id: &str) -> Result<BrowserState> {
        let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if state
            .views
            .get(window)
            .is_none_or(|v| v.profile != profile || v.view_id != view_id)
        {
            return Err(NativeFailure::InvalidEvidence);
        };
        let p = state
            .profiles
            .get(profile)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if !p.removing
            && let Some(code) = state.views[window].failure
        {
            return Err(code);
        }
        Ok(BrowserState {
            tabs: p.tabs.clone(),
            removal_pending: p.removing,
        })
    }

    fn recreate_children(
        &self,
        profile: &str,
        requests: &[ViewRequest],
        mut create_child: impl FnMut(&ViewRequest) -> Result<()>,
    ) -> Result<()> {
        let mut first_failure = None;
        // Every shared user has already lost its old child. One failed creation
        // cannot prevent later users from receiving a child or an exact
        // failure.
        for request in requests {
            if let Err(code) = self.creation_completed(profile, request, create_child(request)) {
                first_failure.get_or_insert(code);
            }
        }
        first_failure.map_or(Ok(()), Err)
    }

    fn creation_completed(
        &self,
        profile: &str,
        request: &ViewRequest,
        result: Result<()>,
    ) -> Result<()> {
        if let Err(code) = result
            && code != NativeFailure::Stopped
        {
            tracing::warn!(operation = "browser_create", ?code);
            if let Ok(mut state) = self.state.lock()
                && !self.stopping.load(Ordering::Acquire)
                && state.profiles.get(profile).is_some_and(|p| !p.removing)
                && state.views.get(&request.window).is_some_and(|view| {
                    view.profile == profile
                        && view.generation == request.generation
                        && state.reservations.get(&request.window) == Some(&view.view_id)
                })
            {
                state.views.get_mut(&request.window).unwrap().failure = Some(code);
            }
        }
        result
    }

    fn prepare_removal(&self, record: ProfileRecord, scope: Option<SavedConnection>) -> Result<()> {
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        record.validate()?;
        if record.data.state != ProfileState::RemovalPending
            || scope.as_ref().is_some_and(|saved| {
                saved.server_id != record.data.server_id || saved.device_id != record.data.device_id
            })
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        let path = browser::profile_path(&self.root.join("profiles"), &record)?;
        let intent = self
            .root
            .join("removals")
            .join(format!("{}.json", record.id));
        if !intent.exists() {
            browser::write_private(
                &intent,
                &Removal {
                    record: record.clone(),
                    scope,
                    request_id: uuid::Uuid::now_v7().to_string(),
                    shutdown_confirmed: false,
                },
            )?;
        }
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if let Some(p) = state.profiles.get_mut(&record.id) {
            p.removing = true;
            p.record = record.clone();
            p.pending.clear();
        }
        let _ = path;
        Ok(())
    }

    fn close_profile(&self, profile: &str) {
        if let Err(code) = self.close_profile_with(profile, unmap_view) {
            tracing::warn!(operation = "browser_profile_close", ?code);
        }
    }

    fn close_profile_with(
        &self,
        profile: &str,
        unmap: impl FnMut(&View) -> Result<()>,
    ) -> Result<()> {
        self.close_profiles_with(&[profile.to_owned()], unmap)
    }

    fn close_profiles_with(
        &self,
        profiles: &[String],
        mut unmap: impl FnMut(&View) -> Result<()>,
    ) -> Result<()> {
        let views: Vec<_> = {
            let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            state
                .views
                .values_mut()
                .filter(|view| profiles.contains(&view.profile))
                .map(|view| {
                    view.closing = true;
                    view.clone()
                })
                .collect()
        };
        let mut first_failure = None;
        // Removal must synchronously hide every credential-bearing child before
        // asynchronous CEF closure. Keep the exact handles for Hide retries and
        // close callbacks, including when one native unmap fails. Native calls
        // use retained snapshots outside state so callbacks can acquire it.
        for view in &views {
            if let Err(code) = unmap(view) {
                first_failure.get_or_insert(code);
            }
        }
        for view in views {
            if let Some(host) = view.browser.and_then(|browser| browser.host()) {
                host.close_browser(1);
            }
        }
        first_failure.map_or(Ok(()), Err)
    }

    fn forgotten_path(&self, server: &str, device: &str) -> Result<PathBuf> {
        canonical_id(server)?;
        canonical_id(device)?;
        Ok(self
            .root
            .join("forgotten")
            .join(format!("{server}-{device}.json")))
    }

    // Persist before native connection removal drops its original client
    // credential. No server authorization or browser contents are retained in
    // this local intent.
    pub fn prepare_forget(&self, scope: &SavedConnection) -> Result<()> {
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        scope.validate()?;
        if scope.device_id.is_empty() {
            return Ok(());
        }
        let intent = ForgottenScope::from_connection(scope)?;
        let path = self.forgotten_path(&scope.server_id, &scope.device_id)?;
        if path.exists() {
            let original: ForgottenScope = read_json(&path)?;
            if original != intent {
                return Err(NativeFailure::InvalidEvidence);
            }
        } else {
            browser::write_private(&path, &intent)?;
        }
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        for profile in state.profiles.values_mut() {
            if profile.record.data.server_id == scope.server_id
                && profile.record.data.device_id == scope.device_id
            {
                profile.removing = true;
                profile.pending.clear();
            }
        }
        Ok(())
    }

    // A fresh unchanged paired record proves that Go did not accept this
    // removal. Drop only its exact staged intent; account-deletion denial wins.
    pub fn cancel_unaccepted_forget(&self, planned: &SavedConnection) -> Result<()> {
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        let intent = ForgottenScope::from_connection(planned)?;
        self.discard_forget(&intent)?;
        let profiles: Vec<_> = self
            .state
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .profiles
            .iter()
            .filter(|(_, p)| {
                p.record.data.server_id == intent.server_id
                    && p.record.data.device_id == intent.device_id
            })
            .map(|(id, p)| (id.clone(), p.record.data.state != ProfileState::Active))
            .collect();
        let denial: Vec<_> = profiles
            .into_iter()
            .map(|(id, pending)| {
                let removing = pending
                    || self
                        .root
                        .join("removals")
                        .join(format!("{id}.json"))
                        .exists();
                (id, removing)
            })
            .collect();
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        for (id, removing) in denial {
            if let Some(profile) = state.profiles.get_mut(&id) {
                profile.removing = removing;
            }
        }
        Ok(())
    }

    fn discard_forget(&self, intent: &ForgottenScope) -> Result<()> {
        let path = self.forgotten_path(&intent.server_id, &intent.device_id)?;
        if !path.exists() {
            return Ok(());
        }
        let original: ForgottenScope = read_json(&path)?;
        if original != *intent {
            return Err(NativeFailure::InvalidEvidence);
        }
        fs::remove_file(&path).map_err(|_| NativeFailure::StorageUnavailable)?;
        #[cfg(unix)]
        fs::File::open(path.parent().unwrap())
            .and_then(|f| f.sync_all())
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        Ok(())
    }

    pub fn close_scope(&self, scope: &SavedConnection) {
        if let Err(code) = self.close_scope_with(scope, unmap_view) {
            tracing::warn!(operation = "browser_scope_close", ?code);
        }
    }

    fn close_scope_with(
        &self,
        scope: &SavedConnection,
        unmap: impl FnMut(&View) -> Result<()>,
    ) -> Result<()> {
        let profiles = {
            let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            state
                .profiles
                .iter_mut()
                .filter(|(_, profile)| {
                    profile.record.data.server_id == scope.server_id
                        && profile.record.data.device_id == scope.device_id
                        && profile.removing
                })
                .map(|(id, profile)| {
                    profile.pending.clear();
                    id.clone()
                })
                .collect::<Vec<_>>()
        };
        self.close_profiles_with(&profiles, unmap)
    }

    fn finish_forgotten(&self, started: Instant) -> Result<()> {
        for (processed, entry) in fs::read_dir(self.root.join("forgotten"))
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .enumerate()
        {
            if processed >= 64 || started.elapsed() >= Duration::from_secs(45) {
                tracing::warn!(
                    operation = "browser_scope_removal",
                    state = "deferred",
                    code = "cleanup-budget"
                );
                break;
            }
            let path = entry.map_err(|_| NativeFailure::StorageUnavailable)?.path();
            if path.extension().and_then(|v| v.to_str()) != Some("json") {
                continue;
            }
            let scope: ForgottenScope = read_json(&path)?;
            if path != self.forgotten_path(&scope.server_id, &scope.device_id)? {
                return Err(NativeFailure::InvalidEvidence);
            }
            // A native staging marker cannot authorize destructive cleanup.
            // Independently require Go's retained original removal receipt,
            // which is readable locally after its credential has been deleted.
            let observed = self.observer.inspect_saved(&scope.connection_id);
            match observed {
                Ok(current) if scope.accepted_by(&current) => {}
                Ok(current) if scope.unaccepted_by(&current) => {
                    self.discard_forget(&scope)?;
                    continue;
                }
                _ => {
                    tracing::warn!(
                        operation = "browser_scope_removal",
                        state = "acceptance-deferred"
                    );
                    continue;
                }
            }
            self.profile_storage
                .remove_device(&scope.server_id, &scope.device_id)?;
            fs::remove_file(path).map_err(|_| NativeFailure::StorageUnavailable)?;
            tracing::info!(
                operation = "browser_scope_removal",
                state = "locally-purged"
            );
        }
        Ok(())
    }

    // Native UI loop only. Window destruction cannot rely on renderer cleanup.
    // Invalidate pending preparation/creation before requesting raw-child
    // close; retain profiles and callback accounting until actual CEF
    // teardown.
    pub fn close_window(&self, window: &str) -> Result<()> {
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let hidden = if let Some(view) = state.views.get_mut(window) {
            view.closing = true;
            unmap_view(view)
        } else {
            Ok(())
        };
        state.reservations.remove(window);
        for profile in state.profiles.values_mut() {
            profile.pending.retain(|request| request.window != window);
        }
        let browser = state
            .views
            .get(window)
            .and_then(|view| view.browser.clone());
        if hidden.is_ok() {
            state.views.remove(window);
        }
        drop(state);
        if let Some(browser) = browser
            && let Some(host) = browser.host()
        {
            host.close_browser(1);
        }
        hidden
    }

    pub fn close_all(&self) {
        if let Ok(mut state) = self.state.lock() {
            let closing: Vec<_> = state
                .views
                .values()
                .filter_map(|view| view.browser.clone())
                .collect();
            for p in state.profiles.values_mut() {
                p.pending.clear();
                p.context.take();
            }
            drop(state);
            for browser in closing {
                if let Some(host) = browser.host() {
                    host.close_browser(1);
                }
            }
        }
    }

    fn queue_address(self: &Arc<Self>, profile: String, request: ViewRequest, url: String) {
        if self.stopping.load(Ordering::Acquire) || url.len() > 8192 {
            return;
        }
        let Ok(mut pending) = self.addresses.lock() else {
            return;
        };
        if self.stopping.load(Ordering::Acquire) {
            return;
        }
        // At most one latest address per tab, bounded by 64 profiles x 16 tabs.
        let key = format!("{}:{}", profile, request.tab);
        if pending.len() >= 1024 && !pending.contains_key(&key) {
            return;
        }
        pending.insert(key, (profile, request, url));
        if self.address_running.swap(true, Ordering::AcqRel) {
            return;
        }
        let host = Arc::clone(self);
        let join = thread::spawn(move || {
            loop {
                let batch = {
                    let Ok(mut pending) = host.addresses.lock() else {
                        break;
                    };
                    if pending.is_empty() {
                        host.address_running.store(false, Ordering::Release);
                        break;
                    }
                    std::mem::take(&mut *pending)
                };
                for (_, (profile, request, url)) in batch {
                    if let Err(code) = host.persist_address(&profile, &request, &url)
                        && code != NativeFailure::Stopped
                    {
                        tracing::warn!(operation = "browser_tabs", ?code);
                    }
                }
            }
        });
        if let Ok(mut value) = self.address_join.lock() {
            *value = Some(join);
        }
    }

    fn persist_address(&self, profile: &str, request: &ViewRequest, url: &str) -> Result<()> {
        // Only pre-stop accepted callbacks reach this worker during exit. Drain
        // them with the same generation/reservation/removal publication checks;
        // stopping rejects new callbacks, not these already owned writes.
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        let (path, mut tabs) = {
            let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            if state.views.get(&request.window).is_none_or(|v| {
                v.profile != profile
                    || v.generation != request.generation
                    || state.reservations.get(&request.window) != Some(&v.view_id)
            }) {
                return Err(NativeFailure::Stopped);
            }
            let p = state.profiles.get(profile).ok_or(NativeFailure::Stopped)?;
            if p.removing || !p.policy.lock().is_ok_and(|policy| policy.navigation(url)) {
                return Err(NativeFailure::Stopped);
            }
            (p.path.clone(), p.tabs.clone())
        };
        let tab = tabs
            .tabs
            .iter_mut()
            .find(|t| t.id == request.tab)
            .ok_or(NativeFailure::Stopped)?;
        tab.url = url.into();
        let staged = browser::stage_private(&path.join("tabs.json"), &tabs)?;
        let _publication = self.publication.lock().map_err(|_| NativeFailure::Busy)?;
        let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if state.views.get(&request.window).is_none_or(|v| {
            v.profile != profile
                || v.generation != request.generation
                || state.reservations.get(&request.window) != Some(&v.view_id)
        }) || state.profiles.get(profile).is_none_or(|p| p.removing)
        {
            return Err(NativeFailure::Stopped);
        }
        drop(state);
        staged.publish()?;
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let p = state
            .profiles
            .get_mut(profile)
            .ok_or(NativeFailure::Stopped)?;
        if p.removing {
            return Err(NativeFailure::Stopped);
        }
        p.tabs = tabs;
        // Navigation callbacks persist observed URLs; they do not invalidate an
        // already prepared native control generation.
        Ok(())
    }

    // No CEF API may run here. The caller has separately observed runtime
    // return.
    pub fn finish_removals(&self) -> Result<()> {
        if !self.stopping.load(Ordering::Acquire)
            || self.join.lock().map_err(|_| NativeFailure::Busy)?.is_some()
            || self.state.lock().map_err(|_| NativeFailure::Busy)?.live != 0
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        // UI-loop shutdown is already independently complete. Join any final
        // coalesced disk work before deleting its profile directory.
        if let Some(join) = self
            .address_join
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .take()
        {
            join.join().map_err(|_| NativeFailure::SidecarFailed)?;
        }
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        // Queue-local failures retain their original evidence, but cannot
        // prevent the independent account queue from using its exit budget.
        let forgotten = self.finish_forgotten(Instant::now());
        let accounts = self.finish_account_removals();
        for (queue, result) in [("forgotten", &forgotten), ("accounts", &accounts)] {
            if let Err(code) = result {
                tracing::warn!(operation = "browser_shutdown_cleanup", queue, ?code);
            }
        }
        forgotten.and(accounts)
    }

    // Called only after independent native shutdown and the address-worker
    // join, with storage serialized. Offline forgotten scopes cannot spend
    // this budget.
    fn finish_account_removals(&self) -> Result<()> {
        let started = Instant::now();
        let mut first_failure = None;
        let cursor_path = self.root.join("removal-cursor.json");
        let mut paths = fs::read_dir(self.root.join("removals"))
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .map(|entry| entry.map(|entry| entry.path()))
            .collect::<std::io::Result<Vec<_>>>()
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        paths.retain(|path| path.extension().and_then(|v| v.to_str()) == Some("json"));
        paths.sort();
        if cursor_path.exists() {
            let cursor: String = read_json(&cursor_path)?;
            canonical_id(&cursor)?;
            let after = self.root.join("removals").join(format!("{cursor}.json"));
            let next = paths.partition_point(|path| path <= &after);
            paths.rotate_left(next);
        }
        for (processed, path) in paths.into_iter().enumerate() {
            if processed >= 64 || started.elapsed() >= Duration::from_secs(45) {
                tracing::warn!(
                    operation = "browser_removal",
                    state = "deferred",
                    code = "cleanup-budget"
                );
                break;
            }
            let mut removal: Removal = read_json(&path)?;
            removal.record.validate()?;
            canonical_id(&removal.request_id)?;
            if path.file_stem().and_then(|s| s.to_str()) != Some(removal.record.id.as_str()) {
                return Err(NativeFailure::InvalidEvidence);
            }
            // Persist progress before any offline acknowledgment can exhaust
            // the exit budget. Retained receipts must not
            // monopolize every later exit.
            browser::write_private(&cursor_path, &removal.record.id)?;
            let result = (|| -> Result<()> {
                // The durable original intent already denies reopen. Remove
                // locally after native shutdown even if the
                // owning server is temporarily offline.
                // Its receipt remains pending until a fresh exact status and
                // acknowledgment.
                removal.shutdown_confirmed = true;
                browser::write_private(&path, &removal)?;
                self.profile_storage.remove_profile(&removal.record)?;
                Ok(())
            })();
            if let Err(code) = result {
                tracing::warn!(
                    operation = "browser_removal",
                    state = "local-failure",
                    ?code
                );
                first_failure.get_or_insert(code);
                continue;
            }
            // A completed local purge and a deferred server acknowledgment are
            // separate outcomes. Offline/revoked authority retains the original
            // intent without turning an otherwise normal quit into host
            // failure.
            let acknowledgment = (|| -> Result<()> {
                let current = self
                    .connector
                    .browser_profile(removal.scope.as_ref(), &removal.record.id)?;
                if current.data.state == ProfileState::Removed {
                    return Ok(());
                }
                if current.id != removal.record.id
                    || current.revision != removal.record.revision
                    || current.data.deletion_request_id != removal.record.data.deletion_request_id
                {
                    return Err(NativeFailure::InvalidEvidence);
                }
                self.connector.browser_confirm(
                    removal.scope.as_ref(),
                    &removal.record,
                    &removal.request_id,
                )?;
                Ok(())
            })();
            match acknowledgment {
                Ok(()) => {
                    if fs::remove_file(path).is_err() {
                        first_failure.get_or_insert(NativeFailure::StorageUnavailable);
                    } else {
                        tracing::info!(operation = "browser_removal", state = "confirmed");
                    }
                }
                Err(code) => tracing::warn!(
                    operation = "browser_removal",
                    state = "acknowledgment-deferred",
                    ?code
                ),
            }
        }
        first_failure.map_or(Ok(()), Err)
    }
}
#[derive(Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct ForgottenScope {
    server_id: String,
    device_id: String,
    connection_id: String,
    pairing_id: String,
    request_id: String,
    expected_revision: u64,
}
impl ForgottenScope {
    fn from_connection(scope: &SavedConnection) -> Result<Self> {
        scope.validate()?;
        let removal = scope
            .removal
            .as_ref()
            .ok_or(NativeFailure::InvalidEvidence)?;
        Ok(Self {
            server_id: scope.server_id.clone(),
            device_id: scope.device_id.clone(),
            connection_id: scope.id.clone(),
            pairing_id: scope.pairing_id.clone(),
            request_id: removal.request_id.clone(),
            expected_revision: removal.expected_revision,
        })
    }

    fn owns(&self, current: &SavedConnection) -> bool {
        current.id == self.connection_id
            && current.server_id == self.server_id
            && current.device_id == self.device_id
            && current.pairing_id == self.pairing_id
    }

    fn accepted_by(&self, current: &SavedConnection) -> bool {
        self.owns(current)
            && matches!(
                current.state,
                delidev_desktop::SavedConnectionState::Removing
                    | delidev_desktop::SavedConnectionState::Removed
            )
            && current.removal.as_ref().is_some_and(|r| {
                r.request_id == self.request_id && r.expected_revision == self.expected_revision
            })
    }

    fn unaccepted_by(&self, current: &SavedConnection) -> bool {
        self.owns(current)
            && current.state == delidev_desktop::SavedConnectionState::Paired
            && current.revision == self.expected_revision
            && current.removal.is_none()
    }
}
#[derive(Deserialize, Serialize)]
struct Removal {
    record: ProfileRecord,
    scope: Option<SavedConnection>,
    request_id: String,
    shutdown_confirmed: bool,
}
fn read_json<T: serde::de::DeserializeOwned>(path: &std::path::Path) -> Result<T> {
    let m = fs::symlink_metadata(path).map_err(|_| NativeFailure::StorageUnavailable)?;
    if !m.is_file() || m.file_type().is_symlink() || m.len() > 256 << 10 {
        return Err(NativeFailure::InvalidEvidence);
    };
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if m.file_attributes() & 0x400 != 0 {
            return Err(NativeFailure::InvalidEvidence);
        }
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if m.mode() & 0o077 != 0 {
            return Err(NativeFailure::PermissionDenied);
        }
    }
    serde_json::from_slice(&fs::read(path).map_err(|_| NativeFailure::StorageUnavailable)?)
        .map_err(|_| NativeFailure::InvalidEvidence)
}
cef::wrap_request_context_handler! {struct ContextReady{host:Arc<BrowserHost>,app:AppHandle<CefRuntime>,profile:String,}impl RequestContextHandler{
 fn on_request_context_initialized(&self,context:Option<&mut RequestContext>){let Some(context)=context else{return};let pending={let Ok(mut state)=self.host.state.lock()else{return};let Some(p)=state.profiles.get_mut(&self.profile)else{return};if p.removing||self.host.stopping.load(Ordering::Acquire){return};p.ready=true;std::mem::take(&mut p.pending)};for request in pending{let _=create(&self.host,&self.app,self.profile.clone(),request,context.clone());}}
}}
fn create(
    host: &Arc<BrowserHost>,
    app: &AppHandle<CefRuntime>,
    profile: String,
    request: ViewRequest,
    context: RequestContext,
) -> Result<()> {
    let result = create_child(host, app, profile.clone(), request.clone(), context);
    host.creation_completed(&profile, &request, result)
}
fn create_child(
    host: &Arc<BrowserHost>,
    app: &AppHandle<CefRuntime>,
    profile: String,
    request: ViewRequest,
    mut context: RequestContext,
) -> Result<()> {
    let state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
    let p = state
        .profiles
        .get(&profile)
        .ok_or(NativeFailure::InvalidEvidence)?;
    if p.removing
        || host.stopping.load(Ordering::Acquire)
        || app.get_webview_window(&request.window).is_none()
        || state
            .views
            .get(&request.window)
            .is_none_or(|v| v.generation != request.generation)
    {
        return Err(NativeFailure::Stopped);
    }
    let actual = CefString::from(&context.cache_path()).to_string();
    if std::path::Path::new(&actual) != p.cache_path {
        return Err(NativeFailure::InvalidEvidence);
    }
    let url = p
        .tabs
        .tabs
        .iter()
        .find(|t| t.id == request.tab)
        .ok_or(NativeFailure::InvalidEvidence)?
        .url
        .clone();
    let policy = p.policy.clone();
    drop(state);
    {
        let mut state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
        let view = state
            .views
            .get_mut(&request.window)
            .filter(|v| v.profile == profile && v.generation == request.generation)
            .ok_or(NativeFailure::Stopped)?;
        view.creation_pending = true;
        state.live += 1;
    }
    let info = window_info(&request);
    let mut client = ExternalClient::new(
        Arc::clone(host),
        app.clone(),
        profile,
        request.clone(),
        policy,
    );
    if browser_host_create_browser(
        Some(&info),
        Some(&mut client),
        Some(&url.as_str().into()),
        Some(&BrowserSettings {
            javascript_access_clipboard: cef::State::DISABLED,
            javascript_dom_paste: cef::State::DISABLED,
            ..Default::default()
        }),
        None,
        Some(&mut context),
    ) != 1
    {
        let mut state = host.state.lock().map_err(|_| NativeFailure::Busy)?;
        if let Some(view) = state
            .views
            .get_mut(&request.window)
            .filter(|v| v.generation == request.generation)
        {
            view.creation_pending = false;
        }
        state.live -= 1;
        return Err(NativeFailure::SidecarFailed);
    };
    Ok(())
}
cef::wrap_client! {struct ExternalClient{host:Arc<BrowserHost>,app:AppHandle<CefRuntime>,profile:String,request:ViewRequest,policy:Arc<Mutex<Policy>>,}impl Client{
 // The pinned runtime installs a renderer-wide JavaScript message stub. Raw
 // external children have no Tauri browser-side handler: reject every process
 // message here so that stub cannot acquire product/native authority.
 fn on_process_message_received(&self,_browser:Option<&mut Browser>,_frame:Option<&mut Frame>,_source_process:ProcessId,_message:Option<&mut ProcessMessage>)->i32{0}
 fn life_span_handler(&self)->Option<LifeSpanHandler>{Some(ExternalLife::new(Arc::clone(&self.host),self.app.clone(),self.profile.clone(),self.request.clone(),Arc::new(CloseGuard::default())))}
 fn request_handler(&self)->Option<RequestHandler>{Some(ExternalRequests::new(self.policy.clone()))}
 fn display_handler(&self)->Option<DisplayHandler>{Some(ExternalDisplay::new(Arc::clone(&self.host),self.profile.clone(),self.request.clone()))}
 fn permission_handler(&self)->Option<PermissionHandler>{Some(DenyPermissions::new())}
 fn dialog_handler(&self)->Option<DialogHandler>{Some(DenyFiles::new())}
 fn keyboard_handler(&self)->Option<KeyboardHandler>{Some(ExternalKeyboard::new(Arc::clone(&self.host),self.app.clone(),self.profile.clone(),self.request.clone(),Arc::new(Mutex::new(None))))}
 fn download_handler(&self)->Option<DownloadHandler>{Some(DenyDownloads::new())}
}}
#[cfg(target_os = "linux")]
type ExternalOsEvent<'a> = Option<&'a mut cef::sys::XEvent>;
#[cfg(target_os = "macos")]
type ExternalOsEvent<'a> = *mut u8;
#[cfg(windows)]
type ExternalOsEvent<'a> = Option<&'a mut cef::sys::MSG>;
#[derive(Clone, Serialize)]
struct NumericSelection {
    profile_id: String,
    view_id: String,
    position: u8,
    token: String,
}
cef::wrap_keyboard_handler! {struct ExternalKeyboard{host:Arc<BrowserHost>,app:AppHandle<CefRuntime>,profile:String,request:ViewRequest,pressed:Arc<Mutex<Option<i32>>>,}impl KeyboardHandler{
 fn on_pre_key_event(&self,browser:Option<&mut Browser>,event:Option<&KeyEvent>,_os_event:ExternalOsEvent<'_>,_shortcut:Option<&mut i32>)->i32{
  let Some(event)=event else{return 0;};
  if event.type_==cef::sys::cef_key_event_type_t::KEYEVENT_KEYUP.into() {
    if let Ok(mut pressed)=self.pressed.lock() && *pressed==Some(event.windows_key_code) { *pressed=None;return 1; }
    return 0;
  }
  if self.pressed.lock().is_ok_and(|pressed|*pressed==Some(event.windows_key_code)) {return 1;}
  if self.app.state::<Arc<crate::shortcut_capture_host::CaptureHost>>().fenced(){return 0;}
  let raw=event.type_==cef::sys::cef_key_event_type_t::KEYEVENT_RAWKEYDOWN.into();
  let Some(position)=delidev_desktop::session_tab_shortcuts::numeric_intent(event.windows_key_code,event.modifiers,cfg!(target_os="macos"),raw,event.windows_key_code==229)else{return 0;};
  if !browser.as_deref().is_some_and(native_composition_clear) {return 0;}
  let Some((view_id,token))=self.host.numeric_selection(&self.profile,&self.request,position)else{return 0;};
  let Some(window)=self.app.get_webview_window(&self.request.window)else{return 0;};
  if window.emit("session-tab-selection",NumericSelection{profile_id:self.profile.clone(),view_id,position,token}).is_err(){tracing::warn!(operation="browser_tab_shortcut",stage="delivery",classification="unavailable");}
  if let Ok(mut pressed)=self.pressed.lock(){*pressed=Some(event.windows_key_code);}
  1
 }
}}
// CEF 151.8.1 / Chromium 151.0.7922.174
// (39c51c70dd5feca6b6aba5bb7997b595011c553d): windowed native_mac.mm binds the
// original WebContents NSView; its RenderWidgetHostViewCocoa NSTextInputClient
// owns hasMarkedText. The OSR-only OnImeCompositionRangeChanged callback cannot
// establish windowed composition. Windowed CEF has no RenderHandler IME
// callback. Only the original native first responder may prove composition
// clear; missing evidence denies forwarding.
fn native_composition_clear(browser: &Browser) -> bool {
    #[cfg(target_os = "macos")]
    {
        use objc2::{ClassType, msg_send, runtime::AnyObject, sel};
        use objc2_app_kit::NSView;
        let Some(host) = browser.host() else {
            return false;
        };
        let Some(view) = (unsafe { (host.window_handle() as *mut NSView).as_ref() }) else {
            return false;
        };
        unsafe {
            let window: *mut AnyObject = msg_send![view, window];
            if window.is_null() {
                return false;
            }
            let responder: *mut AnyObject = msg_send![window, firstResponder];
            if responder.is_null() {
                return false;
            }
            let is_view: bool = msg_send![responder,isKindOfClass:NSView::class()];
            if !is_view {
                return false;
            }
            let original: bool = msg_send![responder,isDescendantOf:view];
            if !original {
                return false;
            }
            let supported: bool = msg_send![responder,respondsToSelector:sel!(hasMarkedText)];
            if !supported {
                return false;
            }
            let marked: bool = msg_send![responder, hasMarkedText];
            !marked
        }
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = browser;
        false
    }
}
/// Native platforms with distinct CEF child close semantics.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum ClosePlatform {
    MacOs,
    Windows,
    Linux,
}

impl ClosePlatform {
    const CURRENT: Self = if cfg!(target_os = "macos") {
        Self::MacOs
    } else if cfg!(windows) {
        Self::Windows
    } else {
        Self::Linux
    };

    fn as_str(self) -> &'static str {
        match self {
            Self::MacOs => "macos",
            Self::Windows => "windows",
            Self::Linux => "linux",
        }
    }
}

/// How the raw child's `do_close` callback must answer CEF.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum CloseDisposition {
    /// Keep CEF's default behavior (return 0).
    DefaultClose,
    /// The handler owns destruction of the child's own native host (return 1).
    /// CEF's default would send performClose:/WM_CLOSE to the parent product
    /// window on macOS and Windows.
    ChildOwned,
}

fn close_disposition(platform: ClosePlatform, popup: bool) -> CloseDisposition {
    match (platform, popup) {
        (ClosePlatform::MacOs | ClosePlatform::Windows, false) => CloseDisposition::ChildOwned,
        _ => CloseDisposition::DefaultClose,
    }
}

/// Outcome of one `do_close` callback for a child-owned close.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum CloseClaim {
    /// First close of the exact bound browser: destroy its native host.
    Destroy,
    /// A repeated close of the same browser: destruction is already owned.
    Duplicate,
    /// The callback's browser is not the one this handler was created for.
    ForeignBrowser,
}

/// Binds one handler to the exact browser it was created for and makes native
/// destruction run at most once. Browser identifiers are positive, so zero
/// means unbound.
#[derive(Default)]
struct CloseGuard {
    identity: std::sync::atomic::AtomicI32,
    claimed: AtomicBool,
}

impl CloseGuard {
    fn bind(&self, browser_id: i32) -> bool {
        browser_id > 0
            && self
                .identity
                .compare_exchange(0, browser_id, Ordering::AcqRel, Ordering::Acquire)
                .is_ok()
    }

    fn claim(&self, browser_id: i32) -> CloseClaim {
        let bound = self.identity.load(Ordering::Acquire);
        if bound <= 0 || bound != browser_id {
            return CloseClaim::ForeignBrowser;
        }
        if self.claimed.swap(true, Ordering::AcqRel) {
            CloseClaim::Duplicate
        } else {
            CloseClaim::Destroy
        }
    }
}

/// The child's own native host, captured from the exact Browser that CEF passed
/// to `do_close`. It is only used on the UI thread.
struct ChildHost {
    #[cfg(target_os = "macos")]
    view: objc2::rc::Retained<objc2_app_kit::NSView>,
    #[cfg(windows)]
    hwnd: usize,
}

// SAFETY: The captured host is created from a UI-thread callback and only
// dereferenced by the UI-thread closure scheduled through the app handle.
unsafe impl Send for ChildHost {}

impl ChildHost {
    fn capture(handle: cef::sys::cef_window_handle_t) -> Result<Self> {
        #[cfg(target_os = "macos")]
        {
            // Retain so a queued destruction cannot outlive the view's owner.
            let view = unsafe { objc2::rc::Retained::retain(handle as *mut objc2_app_kit::NSView) }
                .ok_or(NativeFailure::InvalidEvidence)?;
            Ok(Self { view })
        }
        #[cfg(windows)]
        {
            if handle.0.is_null() {
                return Err(NativeFailure::InvalidEvidence);
            }
            Ok(Self {
                hwnd: handle.0 as usize,
            })
        }
        #[cfg(not(any(target_os = "macos", windows)))]
        {
            let _ = handle;
            Err(NativeFailure::Incompatible)
        }
    }

    /// Destroys only this child's native host, never its parent.
    fn destroy(self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            // The superview holds the child's strong reference; removing it
            // deallocates the view, which reports WindowDestroyed to CEF and
            // leads to on_before_close.
            self.view.removeFromSuperview();
            Ok(())
        }
        #[cfg(windows)]
        {
            use windows::Win32::{
                Foundation::HWND,
                UI::WindowsAndMessaging::{DestroyWindow, IsWindow},
            };
            let hwnd = HWND(self.hwnd as *mut std::ffi::c_void);
            if !unsafe { IsWindow(Some(hwnd)) }.as_bool() {
                return Err(NativeFailure::InvalidEvidence);
            }
            unsafe { DestroyWindow(hwnd) }.map_err(|_| NativeFailure::SidecarFailed)
        }
        #[cfg(not(any(target_os = "macos", windows)))]
        Err(NativeFailure::Incompatible)
    }
}

/// Decides one `do_close` answer for `browser_id` without touching native
/// state, so the identity and idempotency rules stay unit-testable.
fn resolve_close(
    guard: &CloseGuard,
    platform: ClosePlatform,
    popup: bool,
    browser_id: i32,
) -> Option<CloseClaim> {
    match close_disposition(platform, popup) {
        CloseDisposition::DefaultClose => None,
        CloseDisposition::ChildOwned => Some(guard.claim(browser_id)),
    }
}

cef::wrap_life_span_handler! {struct ExternalLife{host:Arc<BrowserHost>,app:AppHandle<CefRuntime>,profile:String,request:ViewRequest,close_guard:Arc<CloseGuard>,}impl LifeSpanHandler{
 fn on_after_created(&self,browser:Option<&mut Browser>){
   let Some(b)=browser else{return};
   // Capture the exact Browser identity that later close callbacks must match.
   let bound=self.close_guard.bind(b.identifier());
   tracing::info!(operation="browser_view",phase="close_identity_bound",bound,browser_id=b.identifier());
   self.host.finish_child_created(&self.profile,&self.request,
     self.host.child_created(&self.profile,&self.request,b),
     ||{let _=hide_browser(b,&self.request);});
 }
 fn on_before_popup(&self,_browser:Option<&mut Browser>,_frame:Option<&mut Frame>,_popup_id:i32,_target_url:Option<&CefString>,_target_frame_name:Option<&CefString>,_target_disposition:WindowOpenDisposition,_user_gesture:i32,_popup_features:Option<&PopupFeatures>,_window_info:Option<&mut WindowInfo>,_client:Option<&mut Option<Client>>,_settings:Option<&mut BrowserSettings>,_extra_info:Option<&mut Option<DictionaryValue>>,_no_javascript_access:Option<&mut i32>)->i32{1}
 // CEF's default close sends performClose:/WM_CLOSE to the parent window of a
 // parented child on macOS and Windows, which would close the product window.
 // Own the child-only destruction there and keep CEF's default on Linux.
 fn do_close(&self,browser:Option<&mut Browser>)->i32{
   let platform=ClosePlatform::CURRENT;
   let Some(b)=browser else{
     // No browser to destroy: never let CEF fall through to the parent.
     let owned=close_disposition(platform,false)==CloseDisposition::ChildOwned;
     tracing::warn!(operation="browser_view",phase="close_requested",outcome="missing_browser",platform=platform.as_str());
     return i32::from(owned);
   };
   let id=b.identifier();
   let popup=b.is_popup()==1;
   let Some(claim)=resolve_close(&self.close_guard,platform,popup,id) else{
     tracing::info!(operation="browser_view",phase="close_requested",outcome="default",platform=platform.as_str(),popup,browser_id=id);
     return 0;
   };
   match claim{
     CloseClaim::Duplicate=>{
       tracing::info!(operation="browser_view",phase="close_requested",outcome="duplicate_ignored",platform=platform.as_str(),browser_id=id);
     }
     CloseClaim::ForeignBrowser=>{
       tracing::warn!(operation="browser_view",phase="close_requested",outcome="identity_mismatch",platform=platform.as_str(),browser_id=id);
     }
     CloseClaim::Destroy=>{
       tracing::info!(operation="browser_view",phase="close_requested",outcome="child_owned",platform=platform.as_str(),browser_id=id);
       let child=b.host().ok_or(NativeFailure::InvalidEvidence).and_then(|h|ChildHost::capture(h.window_handle()));
       match child{
         Ok(child)=>{
           let queued=self.app.run_on_main_thread(move||{
             match child.destroy(){
               Ok(())=>tracing::info!(operation="browser_view",phase="destroyed",browser_id=id),
               Err(code)=>tracing::warn!(operation="browser_view",phase="destroy_failed",browser_id=id,?code),
             }
           });
           if queued.is_err(){
             tracing::warn!(operation="browser_view",phase="destroy_failed",outcome="not_queued",browser_id=id);
           }
         }
         Err(code)=>tracing::warn!(operation="browser_view",phase="destroy_failed",outcome="capture",browser_id=id,?code),
       }
     }
   }
   // Never fall back to the parent window's close on a child-owned platform.
   1
 }
 fn on_before_close(&self,_browser:Option<&mut Browser>){
   let exit = self.host.child_closed(&self.request);
   tracing::info!(operation="browser_view",state="closed");
   if let Some(code)=exit{self.app.exit(code);}
 }
}}
cef::wrap_request_handler! {struct ExternalRequests{policy:Arc<Mutex<Policy>>,}impl RequestHandler{
 fn on_before_browse(&self,_browser:Option<&mut Browser>,_frame:Option<&mut Frame>,request:Option<&mut cef::Request>,_user_gesture:i32,_is_redirect:i32)->i32{if request.is_some_and(|r|self.policy.lock().is_ok_and(|p|p.navigation(&CefString::from(&r.url()).to_string()))){0}else{1}}
 fn resource_request_handler(&self,_browser:Option<&mut Browser>,_frame:Option<&mut Frame>,_request:Option<&mut cef::Request>,_is_navigation:i32,_is_download:i32,_request_initiator:Option<&CefString>,_disable_default_handling:Option<&mut i32>)->Option<ResourceRequestHandler>{Some(ExternalResources::new(self.policy.clone()))}
}}
cef::wrap_resource_request_handler! {struct ExternalResources{policy:Arc<Mutex<Policy>>,}impl ResourceRequestHandler{
 fn on_before_resource_load(&self,_browser:Option<&mut Browser>,_frame:Option<&mut Frame>,request:Option<&mut cef::Request>,_callback:Option<&mut Callback>)->ReturnValue{if request.is_some_and(|r|self.policy.lock().is_ok_and(|p|p.resource(&CefString::from(&r.url()).to_string()))){ReturnValue::CONTINUE}else{ReturnValue::CANCEL}}
}}
cef::wrap_display_handler! {struct ExternalDisplay{host:Arc<BrowserHost>,profile:String,request:ViewRequest,}impl DisplayHandler{
 // External console content and source URLs must never enter Chromium's default log.
 fn on_console_message(&self,_browser:Option<&mut Browser>,_level:LogSeverity,_message:Option<&CefString>,_source:Option<&CefString>,_line:i32)->i32{1}
 fn on_address_change(&self,_browser:Option<&mut Browser>,frame:Option<&mut Frame>,url:Option<&CefString>){
   if frame.is_none_or(|f|f.is_main()!=1){return};
   let Some(url)=url else{return};
   self.host.queue_address(self.profile.clone(),self.request.clone(),url.to_string());
 }
}}
cef::wrap_permission_handler! {struct DenyPermissions;impl PermissionHandler{
 fn on_request_media_access_permission(&self,_browser:Option<&mut Browser>,_frame:Option<&mut Frame>,_origin:Option<&CefString>,_permissions:u32,callback:Option<&mut MediaAccessCallback>)->i32{if let Some(c)=callback{c.cancel()};1}
 fn on_show_permission_prompt(&self,_browser:Option<&mut Browser>,_id:u64,_origin:Option<&CefString>,_permissions:u32,callback:Option<&mut PermissionPromptCallback>)->i32{if let Some(c)=callback{c.cont(PermissionRequestResult::DENY)};1}
}}
cef::wrap_dialog_handler! {struct DenyFiles;impl DialogHandler{
 fn on_file_dialog(&self,_browser:Option<&mut Browser>,_mode:FileDialogMode,_title:Option<&CefString>,_path:Option<&CefString>,_filters:Option<&mut CefStringList>,_extensions:Option<&mut CefStringList>,_descriptions:Option<&mut CefStringList>,callback:Option<&mut FileDialogCallback>)->i32{if let Some(c)=callback{c.cancel()};1}
}}
cef::wrap_download_handler! {struct DenyDownloads;impl DownloadHandler{fn can_download(&self,_browser:Option<&mut Browser>,_url:Option<&CefString>,_method:Option<&CefString>)->i32{0}}}
fn parent(window: &WebviewWindow<CefRuntime>) -> Result<usize> {
    #[cfg(target_os = "macos")]
    return window
        .ns_view()
        .map(|p| p as usize)
        .map_err(|_| NativeFailure::SidecarFailed);
    #[cfg(windows)]
    return window
        .hwnd()
        .map(|p| p.0 as usize)
        .map_err(|_| NativeFailure::SidecarFailed);
    #[cfg(target_os = "linux")]
    {
        use raw_window_handle::{HasWindowHandle, RawWindowHandle};
        // The raw native handle belongs to WebviewWindow, not its Webview.
        // Keep this bound to the pinned runtime's native X11 parent.
        match window
            .window_handle()
            .map_err(|_| NativeFailure::SidecarFailed)?
            .as_raw()
        {
            RawWindowHandle::Xlib(h) => Ok(h.window as usize),
            _ => Err(NativeFailure::Incompatible),
        }
    }
}
fn window_info(r: &ViewRequest) -> WindowInfo {
    let mut info = WindowInfo {
        bounds: Rect {
            x: (r.bounds.x * r.scale).round() as i32,
            y: (r.bounds.y * r.scale).round() as i32,
            width: (r.bounds.width * r.scale).round() as i32,
            height: (r.bounds.height * r.scale).round() as i32,
        },
        ..Default::default()
    };
    #[cfg(target_os = "macos")]
    {
        info.parent_view = r.parent as *mut std::ffi::c_void;
    }
    #[cfg(windows)]
    {
        info.parent_window = cef::sys::HWND(r.parent as *mut cef::sys::HWND__);
        info.style = 0x40000000 | 0x10000000 | 0x04000000 | 0x02000000;
    }
    #[cfg(target_os = "linux")]
    {
        info.parent_window = r.parent as _;
    }
    info
}
fn unmap_view(view: &View) -> Result<()> {
    if let Some(browser) = &view.browser {
        let hidden = browser
            .host()
            .ok_or(NativeFailure::InvalidEvidence)
            .and_then(|host| {
                position(
                    host.window_handle(),
                    view.request.bounds,
                    view.request.scale,
                    false,
                )
            });
        if let Err(code) = hidden {
            tracing::warn!(operation = "browser_unmap", ?code);
        }
        hidden?;
    }
    Ok(())
}

fn hide_browser(browser: &Browser, request: &ViewRequest) -> Result<()> {
    let host = browser.host().ok_or(NativeFailure::InvalidEvidence)?;
    // Native invisibility is synchronous; CloseBrowser only requests teardown.
    // Still request closure after an unmap failure, retaining the view until
    // its exact close callback or a later successful native hide proves
    // absence.
    let hidden = position(host.window_handle(), request.bounds, request.scale, false);
    host.close_browser(1);
    hidden
}

fn position(
    handle: cef::sys::cef_window_handle_t,
    b: Bounds,
    scale: f64,
    visible: bool,
) -> Result<()> {
    #[cfg(target_os = "macos")]
    {
        use objc2_app_kit::NSView;
        use objc2_foundation::{NSPoint, NSRect, NSSize};
        let view =
            unsafe { (handle as *mut NSView).as_ref() }.ok_or(NativeFailure::InvalidEvidence)?;
        let parent = unsafe { view.superview() }.ok_or(NativeFailure::InvalidEvidence)?;
        let y = if parent.isFlipped() {
            b.y
        } else {
            parent.frame().size.height - b.y - b.height
        };
        view.setFrame(NSRect::new(
            NSPoint::new(b.x, y),
            NSSize::new(b.width, b.height),
        ));
        view.setHidden(!visible);
        let _ = scale;
        Ok(())
    }
    #[cfg(windows)]
    {
        use windows::Win32::{
            Foundation::HWND,
            UI::WindowsAndMessaging::{
                SWP_HIDEWINDOW, SWP_NOACTIVATE, SWP_NOZORDER, SWP_SHOWWINDOW, SetWindowPos,
            },
        };
        unsafe {
            SetWindowPos(
                HWND(handle.0.cast()),
                None,
                (b.x * scale).round() as i32,
                (b.y * scale).round() as i32,
                (b.width * scale).round() as i32,
                (b.height * scale).round() as i32,
                SWP_NOZORDER
                    | SWP_NOACTIVATE
                    | if visible {
                        SWP_SHOWWINDOW
                    } else {
                        SWP_HIDEWINDOW
                    },
            )
        }
        .map_err(|_| NativeFailure::SidecarFailed)?;
        Ok(())
    }
    #[cfg(target_os = "linux")]
    {
        #[link(name = "X11")]
        unsafe extern "C" {
            fn XOpenDisplay(name: *const std::ffi::c_char) -> *mut std::ffi::c_void;
            fn XMoveResizeWindow(
                display: *mut std::ffi::c_void,
                window: std::ffi::c_ulong,
                x: i32,
                y: i32,
                width: u32,
                height: u32,
            ) -> i32;
            fn XMapWindow(display: *mut std::ffi::c_void, window: std::ffi::c_ulong) -> i32;
            fn XUnmapWindow(display: *mut std::ffi::c_void, window: std::ffi::c_ulong) -> i32;
            fn XSync(display: *mut std::ffi::c_void, discard: i32) -> i32;
            fn XCloseDisplay(display: *mut std::ffi::c_void) -> i32;
        }
        // Only the already selected X11 session can move this owned CEF child.
        let display = unsafe { XOpenDisplay(std::ptr::null()) };
        if display.is_null() {
            return Err(NativeFailure::SidecarFailed);
        }
        unsafe {
            XMoveResizeWindow(
                display,
                handle,
                (b.x * scale).round() as i32,
                (b.y * scale).round() as i32,
                (b.width * scale).round() as u32,
                (b.height * scale).round() as u32,
            );
            if visible {
                XMapWindow(display, handle);
            } else {
                XUnmapWindow(display, handle);
            }
            XSync(display, 0);
            XCloseDisplay(display);
        };
        Ok(())
    }
}

#[cfg(all(test, unix))]
mod tests {
    use std::os::unix::fs::PermissionsExt;

    use cef::rc::ConvertReturnValue;
    use delidev_desktop::browser::Profile;

    use super::*;

    struct CloseProbe {
        host: std::sync::Weak<BrowserHost>,
        window: String,
        events: Arc<Mutex<Vec<String>>>,
    }

    fn browser_fixture(
        host: &Arc<BrowserHost>,
        window: &str,
        events: &Arc<Mutex<Vec<String>>>,
    ) -> Browser {
        unsafe extern "C" fn get_host(
            raw: *mut cef::sys::cef_browser_t,
        ) -> *mut cef::sys::cef_browser_host_t {
            cef::rc::RcImpl::<_, cef::BrowserHost>::get(raw)
                .interface
                .clone()
                .into()
        }
        unsafe extern "C" fn close_browser(raw: *mut cef::sys::cef_browser_host_t, force: i32) {
            let probe = &cef::rc::RcImpl::<_, CloseProbe>::get(raw).interface;
            let unlocked = probe
                .host
                .upgrade()
                .is_some_and(|host| host.state.try_lock().is_ok());
            probe.events.lock().unwrap().push(format!(
                "close:{}:unlocked={unlocked}:force={force}",
                probe.window
            ));
        }
        // Use disposable reference-counted CEF interfaces with no native
        // window. Only the injected unmap and close probes run;
        // callbacks stay held.
        // SAFETY: These CEF interfaces contain only integer fields and optional
        // function pointers. RcImpl fills their reference-counted base below.
        let mut raw_host: cef::sys::cef_browser_host_t = unsafe { std::mem::zeroed() };
        raw_host.close_browser = Some(close_browser);
        let probe = CloseProbe {
            host: Arc::downgrade(host),
            window: window.into(),
            events: Arc::clone(events),
        };
        let raw_host = cef::rc::RcImpl::new(raw_host, probe).cast::<cef::sys::cef_browser_host_t>();
        let child_host: cef::BrowserHost = raw_host.wrap_result();
        // SAFETY: The browser interface has the same nullable C field layout.
        let mut raw: cef::sys::cef_browser_t = unsafe { std::mem::zeroed() };
        raw.get_host = Some(get_host);
        cef::rc::RcImpl::new(raw, child_host)
            .cast::<cef::sys::cef_browser_t>()
            .wrap_result()
    }

    #[test]
    fn child_close_is_owned_only_for_non_popup_macos_and_windows() {
        use ClosePlatform::{Linux, MacOs, Windows};
        for platform in [MacOs, Windows] {
            assert_eq!(
                close_disposition(platform, false),
                CloseDisposition::ChildOwned
            );
            assert_eq!(
                close_disposition(platform, true),
                CloseDisposition::DefaultClose
            );
        }
        for popup in [false, true] {
            assert_eq!(
                close_disposition(Linux, popup),
                CloseDisposition::DefaultClose
            );
        }
    }

    #[test]
    fn child_close_destroys_only_the_bound_browser_once() {
        let guard = CloseGuard::default();
        // Unbound handlers never destroy anything.
        assert_eq!(guard.claim(7), CloseClaim::ForeignBrowser);
        assert!(!guard.bind(0));
        assert!(guard.bind(7));
        // The binding cannot be replaced by a later browser.
        assert!(!guard.bind(8));
        // A sibling or replacement browser cannot consume or trigger the claim.
        assert_eq!(guard.claim(8), CloseClaim::ForeignBrowser);
        assert_eq!(guard.claim(7), CloseClaim::Destroy);
        assert_eq!(guard.claim(7), CloseClaim::Duplicate);
        assert_eq!(guard.claim(8), CloseClaim::ForeignBrowser);
    }

    #[test]
    fn resolve_close_keeps_default_close_without_claiming() {
        let guard = CloseGuard::default();
        assert!(guard.bind(3));
        assert_eq!(resolve_close(&guard, ClosePlatform::Linux, false, 3), None);
        assert_eq!(resolve_close(&guard, ClosePlatform::MacOs, true, 3), None);
        // Defaulted closes leave the child-owned claim available.
        assert_eq!(
            resolve_close(&guard, ClosePlatform::Windows, false, 3),
            Some(CloseClaim::Destroy)
        );
        assert_eq!(
            resolve_close(&guard, ClosePlatform::Windows, false, 3),
            Some(CloseClaim::Duplicate)
        );
    }

    #[test]
    fn generation_filter_ignores_late_close_for_replacement_view() {
        let (_temp, host, _record) = storage_fixture();
        let events = Arc::new(Mutex::new(Vec::new()));
        let request = |generation| ViewRequest {
            window: "main".into(),
            parent: 0,
            bounds: Bounds {
                x: 0.0,
                y: 0.0,
                width: 1.0,
                height: 1.0,
            },
            scale: 1.0,
            tab: "tab".into(),
            generation,
        };
        let replacement = View {
            profile: "profile".into(),
            generation: 2,
            request: request(2),
            browser: Some(browser_fixture(&host, "main", &events)),
            failure: None,
            creation_pending: false,
            closing: true,
            view_id: "view".into(),
        };
        {
            let mut state = host.state.lock().unwrap();
            state.views.insert("main".into(), replacement);
            state.live = 2;
        }
        // The old generation's close accounts for its own child only.
        host.child_closed(&request(1));
        {
            let state = host.state.lock().unwrap();
            let view = state.views.get("main").expect("replacement survives");
            assert!(view.browser.is_some());
            assert_eq!(state.live, 1);
        }
        host.child_closed(&request(2));
        let state = host.state.lock().unwrap();
        assert!(state.views.get("main").is_none());
        assert_eq!(state.live, 0);
    }

    fn forgotten_scope(record: &ProfileRecord) -> SavedConnection {
        SavedConnection {
            version: 1,
            revision: 2,
            id: uuid::Uuid::now_v7().to_string(),
            name: "fixture".into(),
            endpoint: "https://server.test".into(),
            server_id: record.data.server_id.clone(),
            pairing_id: uuid::Uuid::now_v7().to_string(),
            device_id: record.data.device_id.clone(),
            state: delidev_desktop::SavedConnectionState::Removing,
            created_at: "2026-09-30T00:00:00Z".into(),
            removal: Some(delidev_desktop::RemovalMetadata {
                request_id: uuid::Uuid::now_v7().to_string(),
                expected_revision: 1,
            }),
        }
    }
    fn storage_fixture() -> (tempfile::TempDir, Arc<BrowserHost>, ProfileRecord) {
        storage_fixture_with_mode(BrowserStorageMode::System)
    }

    fn storage_fixture_with_mode(
        mode: BrowserStorageMode,
    ) -> (tempfile::TempDir, Arc<BrowserHost>, ProfileRecord) {
        let temp = tempfile::tempdir().unwrap();
        let sidecar = temp.path().join("sidecar");
        fs::write(&sidecar, "#!/bin/sh\nexit 1\n").unwrap();
        fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
        let connector =
            Arc::new(native_fixture_connector(sidecar, temp.path().to_path_buf()).unwrap());
        let host = Arc::new(BrowserHost::new(temp.path().join("cef"), connector, mode).unwrap());
        let record = ProfileRecord {
            id: uuid::Uuid::now_v7().to_string(),
            revision: 1,
            data: Profile {
                server_id: uuid::Uuid::now_v7().to_string(),
                device_id: uuid::Uuid::now_v7().to_string(),
                account_id: uuid::Uuid::now_v7().to_string(),
                state: ProfileState::Active,
                deletion_request_id: String::new(),
            },
        };
        (temp, host, record)
    }

    #[test]
    fn forgotten_failure_preserves_evidence_without_starving_account_cleanup() {
        for unsafe_storage in [false, true] {
            let (temp, host, mut account) = storage_fixture();
            let account_cache =
                browser::profile_path(&host.root.join("profiles"), &account).unwrap();
            fs::write(account_cache.join("Cookies"), b"account fixture").unwrap();
            account.data.state = ProfileState::RemovalPending;
            account.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
            host.prepare_removal(account.clone(), None).unwrap();
            let account_intent = host
                .root
                .join("removals")
                .join(format!("{}.json", account.id));
            let original: Removal = read_json(&account_intent).unwrap();

            let mut forgotten_cookies = None;
            let marker = if unsafe_storage {
                let mut forgotten_account = account.clone();
                forgotten_account.data.device_id = uuid::Uuid::now_v7().to_string();
                let scope = forgotten_scope(&forgotten_account);
                host.prepare_forget(&scope).unwrap();
                fs::write(
                    temp.path().join("sidecar.operation"),
                    "#!/bin/sh\nexec /bin/cat \"$2/reply.json\"\n",
                )
                .unwrap();
                browser::write_private(
                    &temp.path().join("reply.json"),
                    &serde_json::json!({"version": 1, "result": scope}),
                )
                .unwrap();
                let cache =
                    browser::profile_path(&host.root.join("profiles"), &forgotten_account).unwrap();
                fs::write(cache.join("Cookies"), b"forgotten fixture").unwrap();
                forgotten_cookies = Some(cache.join("Cookies"));
                // Unsafe ownership must retain the original scope and cookies.
                let device = cache.parent().unwrap();
                fs::set_permissions(device, fs::Permissions::from_mode(0o755)).unwrap();
                host.forgotten_path(&scope.server_id, &scope.device_id)
                    .unwrap()
            } else {
                let marker = host.root.join("forgotten").join("malformed.json");
                browser::write_private(&marker, &"malformed fixture").unwrap();
                marker
            };
            let evidence = fs::read(&marker).unwrap();
            assert_eq!(host.finish_removals(), Err(NativeFailure::InvalidEvidence));
            assert!(account_cache.exists(), "native shutdown remains mandatory");
            host.stop();
            let expected = if unsafe_storage {
                NativeFailure::PermissionDenied
            } else {
                NativeFailure::InvalidEvidence
            };
            assert_eq!(host.finish_removals(), Err(expected));
            assert_eq!(fs::read(&marker).unwrap(), evidence);
            if let Some(cookies) = forgotten_cookies {
                assert_eq!(fs::read(cookies).unwrap(), b"forgotten fixture");
            }
            assert!(!account_cache.exists());
            assert_eq!(
                read_json::<String>(&host.root.join("removal-cursor.json")).unwrap(),
                account.id
            );
            let retained: Removal = read_json(&account_intent).unwrap();
            assert_eq!(
                serde_json::to_value(&retained.record).unwrap(),
                serde_json::to_value(&original.record).unwrap()
            );
            assert_eq!(retained.request_id, original.request_id);
            assert!(retained.shutdown_confirmed);
        }
    }

    #[test]
    fn simultaneous_cleanup_failures_return_first_error_after_account_checkpoint() {
        let (_temp, host, mut account) = storage_fixture();
        let cache = browser::profile_path(&host.root.join("profiles"), &account).unwrap();
        account.data.state = ProfileState::RemovalPending;
        account.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
        host.prepare_removal(account.clone(), None).unwrap();
        fs::set_permissions(&cache, fs::Permissions::from_mode(0o755)).unwrap();
        browser::write_private(
            &host.root.join("forgotten/malformed.json"),
            &"malformed fixture",
        )
        .unwrap();
        host.stop();
        assert_eq!(host.finish_removals(), Err(NativeFailure::InvalidEvidence));
        assert!(cache.exists());
        assert_eq!(
            read_json::<String>(&host.root.join("removal-cursor.json")).unwrap(),
            account.id
        );
        let retained: Removal = read_json(
            &host
                .root
                .join("removals")
                .join(format!("{}.json", account.id)),
        )
        .unwrap();
        assert_eq!(
            serde_json::to_value(&retained.record).unwrap(),
            serde_json::to_value(&account).unwrap()
        );
        assert!(retained.shutdown_confirmed);
    }

    #[test]
    fn development_context_uses_isolated_cookies_and_shared_tabs() {
        let (_temp, host, record) = storage_fixture_with_mode(BrowserStorageMode::DevelopmentMock);
        let original = browser::profile_path(&host.root.join("profiles"), &record).unwrap();
        fs::write(original.join("Cookies"), b"original cookie fixture").unwrap();
        let view = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &view).unwrap();
        host.prepare_fixture_open(
            "fixture",
            &view,
            None,
            record.clone(),
            "https://fixture.test/",
        )
        .unwrap();
        let state = host.state.lock().unwrap();
        let profile = &state.profiles[&record.id];
        assert_eq!(profile.path, original);
        assert!(
            profile
                .cache_path
                .starts_with(host.root.join("development/profiles"))
        );
        assert!(!profile.cache_path.join("Cookies").exists());
        let tabs: Tabs = read_json(&original.join("tabs.json")).unwrap();
        assert_eq!(tabs.tabs[0].url, "https://fixture.test/");
        assert_eq!(
            fs::read(original.join("Cookies")).unwrap(),
            b"original cookie fixture"
        );
    }

    #[test]
    fn failed_cookie_purge_retains_receipt_and_sends_no_acknowledgment() {
        for failing_mode in [
            BrowserStorageMode::DevelopmentMock,
            BrowserStorageMode::System,
        ] {
            let (temp, host, mut record) =
                storage_fixture_with_mode(BrowserStorageMode::DevelopmentMock);
            record.revision = 2;
            record.data.state = ProfileState::RemovalPending;
            record.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
            let development = host.profile_storage.prepare_profile(&record).unwrap();
            let original = browser::profile_path(&host.root.join("profiles"), &record).unwrap();
            fs::write(original.join("Cookies"), b"original fixture").unwrap();
            let observed = temp.path().join("desktop-client");
            browser::private_dir(&observed).unwrap();
            let mut removed = record.clone();
            removed.revision += 1;
            removed.data.state = ProfileState::Removed;
            browser::write_private(
                &observed.join("pending.json"),
                &serde_json::json!({"version":1,"result":{"profile":record}}),
            )
            .unwrap();
            browser::write_private(
                &observed.join("removed.json"),
                &serde_json::json!({"version":1,"result":{"profile":removed}}),
            )
            .unwrap();
            fs::write(
                temp.path().join("sidecar.operation"),
                r#"#!/bin/sh
for arg do
  if [ "$arg" = "confirm-removal" ]; then
    : > "$2/desktop-client/acknowledged"
    exec /bin/cat "$2/desktop-client/removed.json"
  fi
done
exec /bin/cat "$2/desktop-client/pending.json"
"#,
            )
            .unwrap();
            host.prepare_removal(record.clone(), None).unwrap();
            let intent = host
                .root
                .join("removals")
                .join(format!("{}.json", record.id));
            let request = read_json::<Removal>(&intent).unwrap().request_id;
            let blocked = if failing_mode == BrowserStorageMode::System {
                &original
            } else {
                &development
            };
            fs::set_permissions(blocked, fs::Permissions::from_mode(0o755)).unwrap();
            host.stopping.store(true, Ordering::Release);
            assert_eq!(host.finish_removals(), Err(NativeFailure::PermissionDenied));
            assert_eq!(read_json::<Removal>(&intent).unwrap().request_id, request);
            assert!(!observed.join("acknowledged").exists());
            assert_eq!(
                fs::read(original.join("Cookies")).unwrap(),
                b"original fixture"
            );
            assert_eq!(
                development.exists(),
                failing_mode == BrowserStorageMode::DevelopmentMock
            );
            fs::set_permissions(blocked, fs::Permissions::from_mode(0o700)).unwrap();
            host.finish_removals().unwrap();
            assert!(observed.join("acknowledged").exists());
            assert!(!intent.exists());
            assert!(!original.exists());
            assert!(!development.exists());
        }
    }

    #[test]
    fn session_tab_shortcut_admission_fences_original_view_and_generation() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        let token = uuid::Uuid::now_v7().to_string();
        assert!(host.numeric_selection(&record.id, &request, 1).is_none());
        host.tab_shortcuts("fixture", &record.id, &view_id, 9, &token)
            .unwrap();
        assert_eq!(
            host.numeric_selection(&record.id, &request, 9),
            Some((view_id.clone(), token.clone()))
        );
        assert!(host.numeric_selection("foreign", &request, 1).is_none());
        let mut stale = request.clone();
        stale.generation += 1;
        assert!(host.numeric_selection(&record.id, &stale, 1).is_none());
        host.tab_shortcuts("fixture", &record.id, &view_id, 0, &token)
            .unwrap();
        assert!(host.numeric_selection(&record.id, &request, 1).is_none());
        assert!(
            host.tab_shortcuts("fixture", &record.id, &view_id, 10, &token)
                .is_err()
        );
        host.tab_shortcuts("fixture", &record.id, &view_id, 9, &token)
            .unwrap();
        host.state
            .lock()
            .unwrap()
            .tab_shortcuts
            .get_mut("fixture")
            .unwrap()
            .4 = Instant::now() - Duration::from_secs(1);
        assert!(host.numeric_selection(&record.id, &request, 1).is_none());
        host.tab_shortcuts("fixture", &record.id, &view_id, 9, &token)
            .unwrap();
        assert!(
            host.hide_with("fixture", &record.id, &view_id, |_| Err(
                NativeFailure::Busy
            ))
            .is_err()
        );
        assert!(host.numeric_selection(&record.id, &request, 1).is_none());
    }
    const OWNER: &str = "fixture-owner";

    fn page_request(action: PageAction, url: Option<&str>, page: Option<&str>) -> PageRequest {
        PageRequest {
            action,
            operation_id: Some(uuid::Uuid::now_v7().to_string()),
            url: url.map(Into::into),
            page_id: page.map(Into::into),
        }
    }

    fn commit_fixture(staged: browser::PrivateWrite, committed: &mut bool) -> Result<()> {
        staged.publish_with_commit(committed)
    }

    // Runs one admitted page operation and settles its native cleanup the way
    // the UI loop would, without a CEF application handle.
    fn settle_page(
        host: &BrowserHost,
        record: &ProfileRecord,
        request: &PageRequest,
    ) -> PageReceipt {
        host.prepare_pages(OWNER, None, record, request, commit_fixture)
            .unwrap();
        let key = operation_key(OWNER, &record.id, request.operation_id.as_deref().unwrap());
        let committed = host.state.lock().unwrap().page_operations[&key]
            .receipt
            .metadata_committed;
        host.complete_page_cleanup(&key, &record.id, committed, Ok(()))
            .unwrap()
            .receipt
            .unwrap()
    }

    fn create_page(host: &BrowserHost, record: &ProfileRecord, url: &str) -> String {
        let receipt = settle_page(
            host,
            record,
            &page_request(PageAction::Create, Some(url), None),
        );
        assert_eq!(receipt.phase, PagePhase::Completed);
        receipt.page_id
    }

    fn close_page(host: &BrowserHost, record: &ProfileRecord, page: &str) -> PageReceipt {
        settle_page(
            host,
            record,
            &page_request(PageAction::Close, None, Some(page)),
        )
    }

    fn active_storage_fixture() -> (
        tempfile::TempDir,
        Arc<BrowserHost>,
        ProfileRecord,
        String,
        ViewRequest,
    ) {
        let (temp, host, record) = storage_fixture();
        let view_id = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &view_id).unwrap();
        host.prepare_fixture_open(
            "fixture",
            &view_id,
            None,
            record.clone(),
            "https://fixture.test/original",
        )
        .unwrap();
        let mut state = host.state.lock().unwrap();
        let request = ViewRequest {
            window: "fixture".into(),
            parent: 0,
            bounds: Bounds {
                x: 0.0,
                y: 0.0,
                width: 100.0,
                height: 100.0,
            },
            scale: 1.0,
            tab: state.profiles[&record.id].tabs.selected.clone(),
            generation: 1,
        };
        state.views.insert(
            "fixture".into(),
            View {
                profile: record.id.clone(),
                generation: 1,
                request: request.clone(),
                browser: None,
                failure: None,
                creation_pending: false,
                closing: false,
                view_id: view_id.clone(),
            },
        );
        drop(state);
        (temp, host, record, view_id, request)
    }

    fn wait_for_staged_tabs(path: &std::path::Path) {
        let deadline = Instant::now() + Duration::from_secs(2);
        while !fs::read_dir(path).unwrap().any(|entry| {
            entry
                .unwrap()
                .file_name()
                .to_string_lossy()
                .ends_with(".pending")
        }) {
            assert!(Instant::now() < deadline, "worker did not stage tabs");
            thread::sleep(Duration::from_millis(1));
        }
    }

    #[test]
    fn superseded_tab_controls_discard_staged_files_before_publication() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        create_page(&host, &record, "https://fixture.test/second");
        let path = host.state.lock().unwrap().profiles[&record.id].path.clone();
        let before = fs::read(path.join("tabs.json")).unwrap();
        // The reservation callback owns the publication fence while a
        // worker completes slow staging. UI state remains available.
        let publication = host.publication.lock().unwrap();
        let copy = Arc::clone(&host);
        let profile = record.id.clone();
        let worker = thread::spawn(move || {
            copy.prepare_control(
                "fixture",
                &profile,
                &view_id,
                Action::SelectTab,
                Some("https://fixture.test/stale"),
                Some(&request.tab),
            )
        });
        wait_for_staged_tabs(&path);
        assert!(host.state.try_lock().is_ok());
        host.reserve("fixture", &uuid::Uuid::now_v7().to_string())
            .unwrap();
        drop(publication);
        assert_eq!(worker.join().unwrap(), Err(NativeFailure::Stopped));
        assert_eq!(fs::read(path.join("tabs.json")).unwrap(), before);
        assert_eq!(
            serde_json::to_vec(&host.state.lock().unwrap().profiles[&record.id].tabs).unwrap(),
            before
        );
        assert!(!fs::read_dir(path).unwrap().any(|entry| {
            entry
                .unwrap()
                .file_name()
                .to_string_lossy()
                .ends_with(".pending")
        }));
    }

    #[test]
    fn superseded_presentation_selection_discards_its_staged_write() {
        let (_temp, host, record) = storage_fixture();
        let first = create_page(&host, &record, "https://fixture.test/first");
        create_page(&host, &record, "https://fixture.test/second");
        let path = browser::profile_path(&host.root.join("profiles"), &record).unwrap();
        let before = fs::read(path.join("tabs.json")).unwrap();
        let original = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &original).unwrap();
        let publication = host.publication.lock().unwrap();
        let copy = Arc::clone(&host);
        let prepared = record.clone();
        let page = first.clone();
        let worker =
            thread::spawn(move || copy.prepare_open("fixture", &original, None, prepared, &page));
        wait_for_staged_tabs(&path);
        assert!(host.state.try_lock().is_ok());
        let replacement = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &replacement).unwrap();
        drop(publication);
        assert_eq!(worker.join().unwrap(), Err(NativeFailure::Stopped));
        assert_eq!(fs::read(path.join("tabs.json")).unwrap(), before);
        assert!(host.state.lock().unwrap().profiles.is_empty());
        host.prepare_open("fixture", &replacement, None, record.clone(), &first)
            .unwrap();
        let restored: Tabs = read_json(&path.join("tabs.json")).unwrap();
        assert_eq!(restored.tabs.len(), 2);
        assert_eq!(restored.selected, first);
    }

    #[test]
    fn abandoned_ui_reservation_releases_fence_and_cannot_publish_late() {
        let (_temp, host, record, view_id, _request) = active_storage_fixture();
        let attempt = Arc::new(ReservationAttempt::default());
        let (send, receive) = std::sync::mpsc::sync_channel(1);
        let copy = Arc::clone(&host);
        let waiting = Arc::clone(&attempt);
        let worker = thread::spawn(move || {
            let _publication = copy.publication.lock().unwrap();
            waiting.wait(receive, Duration::from_millis(5))
        });
        assert_eq!(worker.join().unwrap(), Err(NativeFailure::Stopped));
        assert!(host.publication.try_lock().is_ok());
        let late_id = uuid::Uuid::now_v7().to_string();
        let late = attempt.run(|| host.reserve("fixture", &late_id));
        send.send(late).unwrap_err();
        assert_eq!(late, Err(NativeFailure::Stopped));
        assert_eq!(
            host.state.lock().unwrap().reservations.get("fixture"),
            Some(&view_id)
        );
        host.prepare_control("fixture", &record.id, &view_id, Action::Reload, None, None)
            .unwrap();
    }

    #[test]
    fn superseded_address_callback_cannot_publish_staged_url() {
        let (_temp, host, record, _view_id, request) = active_storage_fixture();
        let path = host.state.lock().unwrap().profiles[&record.id].path.clone();
        let before = fs::read(path.join("tabs.json")).unwrap();
        let page = request.tab.clone();
        let publication = host.publication.lock().unwrap();
        let copy = Arc::clone(&host);
        let profile = record.id.clone();
        let worker = thread::spawn(move || {
            copy.persist_address(&profile, &request, "https://fixture.test/stale")
        });
        wait_for_staged_tabs(&path);
        assert!(host.state.try_lock().is_ok());
        let replacement = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &replacement).unwrap();
        drop(publication);
        assert_eq!(worker.join().unwrap(), Err(NativeFailure::Stopped));
        assert_eq!(fs::read(path.join("tabs.json")).unwrap(), before);
        host.prepare_open("fixture", &replacement, None, record.clone(), &page)
            .unwrap();
        assert_eq!(
            serde_json::to_vec(&host.state.lock().unwrap().profiles[&record.id].tabs).unwrap(),
            before
        );
    }

    #[test]
    fn background_tab_close_preserves_selected_child_generation() {
        let (_temp, host, record, _view_id, original) = active_storage_fixture();
        create_page(&host, &record, "https://fixture.test/selected");
        let selected = host.state.lock().unwrap().profiles[&record.id]
            .tabs
            .selected
            .clone();
        let requests = host
            .state
            .lock()
            .unwrap()
            .select_tab(&record.id, &selected, unmap_view)
            .unwrap()
            .requests;
        assert_eq!(requests.len(), 1);
        let generation = requests[0].generation;
        close_page(&host, &record, &original.tab);
        let path = host.state.lock().unwrap().profiles[&record.id].path.clone();
        let tabs: Tabs = read_json(&path.join("tabs.json")).unwrap();
        assert_eq!(tabs.selected, selected);
        assert_eq!(tabs.tabs.len(), 1);
        let mut state = host.state.lock().unwrap();
        assert!(
            state
                .select_tab(&record.id, &selected, unmap_view)
                .unwrap()
                .requests
                .is_empty()
        );
        assert_eq!(state.views["fixture"].generation, generation);
        assert_eq!(state.views["fixture"].request.tab, selected);
        assert_eq!(state.views["fixture"].failure, None);
        drop(state);
        close_page(&host, &record, &selected);
        let mut state = host.state.lock().unwrap();
        assert!(
            state
                .select_tab(&record.id, "", unmap_view)
                .unwrap()
                .requests
                .is_empty()
        );
        assert!(state.views["fixture"].generation > generation);
        assert!(state.views["fixture"].request.tab.is_empty());
    }

    #[test]
    fn tab_replacement_unmaps_the_old_child_before_generation_advance_and_hide() {
        for selected in [uuid::Uuid::now_v7().to_string(), String::new()] {
            let (_temp, host, record, view_id, original) = active_storage_fixture();
            let visible = std::cell::Cell::new(true);
            let mut state = host.state.lock().unwrap();
            state.generation = original.generation;
            state.live = 1; // The superseded CEF close callback is still pending.
            let replacement = state
                .select_tab(&record.id, &selected, |view| {
                    assert_eq!(view.generation, original.generation);
                    assert_eq!(view.request.tab, original.tab);
                    visible.set(false);
                    Ok(())
                })
                .unwrap();
            assert!(!visible.get());
            assert_eq!(replacement.requests.is_empty(), selected.is_empty());
            let generation = state.views["fixture"].generation;
            assert!(generation > original.generation);
            drop(state);
            host.hide_with("fixture", &record.id, &view_id, |_| {
                // Hide has no old handle after replacement. Its earlier unmap
                // must already have proved that the delayed old child is
                // hidden.
                assert!(!visible.get());
                Ok(())
            })
            .unwrap();
            assert_eq!(host.state.lock().unwrap().live, 1);
            host.child_closed(&original);
            assert_eq!(host.state.lock().unwrap().live, 0);
        }
    }

    #[test]
    fn shared_tab_unmap_failure_retains_all_original_views_for_hide() {
        let (_temp, host, record, view_id, original) = active_storage_fixture();
        let mut sibling = original.clone();
        sibling.window = "sibling".into();
        let mut state = host.state.lock().unwrap();
        state.generation = original.generation;
        state.views.insert(
            sibling.window.clone(),
            View {
                profile: record.id.clone(),
                generation: sibling.generation,
                request: sibling.clone(),
                browser: None,
                failure: None,
                creation_pending: false,
                closing: false,
                view_id: view_id.clone(),
            },
        );
        let mut attempts = 0;
        let result = state.select_tab(&record.id, &uuid::Uuid::now_v7().to_string(), |_| {
            attempts += 1;
            if attempts == 2 {
                Err(NativeFailure::SidecarFailed)
            } else {
                Ok(())
            }
        });
        assert!(matches!(result, Err(NativeFailure::SidecarFailed)));
        assert_eq!(attempts, 2);
        for view in state.views.values() {
            assert_eq!(view.generation, original.generation);
            assert_eq!(view.request.tab, original.tab);
            assert_eq!(view.view_id, view_id);
        }
        assert_eq!(state.generation, original.generation);
        drop(state);
        for window in ["fixture", "sibling"] {
            host.hide_with(window, &record.id, &view_id, |view| {
                assert_eq!(view.request.tab, original.tab);
                Ok(())
            })
            .unwrap();
        }
        assert!(host.state.lock().unwrap().views.is_empty());
    }

    #[test]
    fn failed_reservation_unmap_preserves_the_original_cleanup_identity() {
        let (_temp, host, record, view_id, original) = active_storage_fixture();
        let replacement = uuid::Uuid::now_v7().to_string();
        assert_eq!(
            host.reserve_with("fixture", &replacement, |_| Err(
                NativeFailure::SidecarFailed
            )),
            Err(NativeFailure::SidecarFailed)
        );
        let state = host.state.lock().unwrap();
        assert_eq!(state.reservations["fixture"], view_id);
        assert_eq!(state.views["fixture"].generation, original.generation);
        drop(state);
        host.hide_with("fixture", &record.id, &view_id, |view| {
            assert_eq!(view.request.tab, original.tab);
            Ok(())
        })
        .unwrap();
        host.reserve("fixture", &replacement).unwrap();
        assert_eq!(
            host.state.lock().unwrap().reservations["fixture"],
            replacement
        );
    }

    #[test]
    fn profile_removal_unmaps_all_users_and_retains_exact_close_ownership() {
        for failed_window in [None, Some("fixture")] {
            let (_temp, host, mut record, view_id, original) = active_storage_fixture();
            let mut sibling = original.clone();
            sibling.window = "sibling".into();
            let mut unrelated = original.clone();
            unrelated.window = "unrelated".into();
            {
                let mut state = host.state.lock().unwrap();
                for (request, profile) in [
                    (sibling.clone(), record.id.clone()),
                    (unrelated, uuid::Uuid::now_v7().to_string()),
                ] {
                    state.views.insert(
                        request.window.clone(),
                        View {
                            profile,
                            generation: request.generation,
                            request,
                            browser: None,
                            failure: None,
                            creation_pending: false,
                            closing: false,
                            view_id: view_id.clone(),
                        },
                    );
                }
                state.live = 2;
            }
            record.revision += 1;
            record.data.state = ProfileState::RemovalPending;
            record.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
            host.prepare_removal(record.clone(), None).unwrap();
            let mut unmapped = Vec::new();
            let result = host.close_profile_with(&record.id, |view| {
                assert!(host.state.try_lock().is_ok());
                assert!(view.closing);
                assert_eq!(view.view_id, view_id);
                assert_eq!(view.generation, original.generation);
                unmapped.push(view.request.window.clone());
                if failed_window == Some(view.request.window.as_str()) {
                    Err(NativeFailure::SidecarFailed)
                } else {
                    Ok(())
                }
            });
            assert_eq!(
                result,
                failed_window.map_or(Ok(()), |_| Err(NativeFailure::SidecarFailed))
            );
            assert_eq!(unmapped, ["fixture", "sibling"]);
            {
                let state = host.state.lock().unwrap();
                assert_eq!(state.live, 2);
                assert_eq!(state.views.len(), 3);
                assert!(state.views["fixture"].closing);
                assert!(state.views["sibling"].closing);
                assert!(!state.views["unrelated"].closing);
                assert!(state.profiles[&record.id].path.join("tabs.json").exists());
            }
            if failed_window.is_some() {
                host.hide_with("fixture", &record.id, &view_id, |view| {
                    assert!(view.closing);
                    assert_eq!(view.generation, original.generation);
                    Ok(())
                })
                .unwrap();
            }
            host.child_closed(&original);
            host.child_closed(&sibling);
            let state = host.state.lock().unwrap();
            assert_eq!(state.live, 0);
            assert_eq!(state.views.len(), 1);
            assert!(state.views.contains_key("unrelated"));
            assert!(state.profiles[&record.id].path.join("tabs.json").exists());
        }
    }

    #[test]
    fn forgotten_scope_unmaps_every_child_before_close_and_retains_callback_ownership() {
        for failed_window in [None, Some("fixture")] {
            let (_temp, host, record, view_id, original) = active_storage_fixture();
            let scope = forgotten_scope(&record);
            let events = Arc::new(Mutex::new(Vec::new()));
            let mut records = Vec::new();
            let mut requests = vec![original.clone()];
            for window in ["second", "other-device", "other-server"] {
                let mut other = record.clone();
                other.id = uuid::Uuid::now_v7().to_string();
                other.data.account_id = uuid::Uuid::now_v7().to_string();
                match window {
                    "other-device" => other.data.device_id = uuid::Uuid::now_v7().to_string(),
                    "other-server" => other.data.server_id = uuid::Uuid::now_v7().to_string(),
                    _ => {}
                }
                host.reserve(window, &view_id).unwrap();
                host.prepare_fixture_open(
                    window,
                    &view_id,
                    None,
                    other.clone(),
                    "https://fixture.test",
                )
                .unwrap();
                let mut request = original.clone();
                request.window = window.into();
                let mut state = host.state.lock().unwrap();
                let mut view = state.views["fixture"].clone();
                view.profile = other.id.clone();
                view.request = request.clone();
                state.views.insert(window.into(), view);
                state
                    .profiles
                    .get_mut(&other.id)
                    .unwrap()
                    .pending
                    .push(request.clone());
                records.push(other);
                requests.push(request);
            }
            {
                let mut state = host.state.lock().unwrap();
                let mut sibling = state.views["fixture"].clone();
                sibling.request.window = "sibling".into();
                requests.push(sibling.request.clone());
                state.views.insert("sibling".into(), sibling);
                for (window, view) in &mut state.views {
                    view.browser = Some(browser_fixture(&host, window, &events));
                }
                state.live = state.views.len();
                state
                    .profiles
                    .get_mut(&record.id)
                    .unwrap()
                    .pending
                    .push(original.clone());
            }
            let handles: BTreeMap<_, _> = host
                .state
                .lock()
                .unwrap()
                .views
                .iter()
                .map(|(window, view)| (window.clone(), view.browser.as_ref().unwrap().get_raw()))
                .collect();
            host.prepare_forget(&scope).unwrap();
            // Discovery must also discard context-ready work queued before
            // close.
            host.state
                .lock()
                .unwrap()
                .profiles
                .get_mut(&record.id)
                .unwrap()
                .pending
                .push(original.clone());
            let result = host.close_scope_with(&scope, |view| {
                let state = host.state.try_lock().expect("unmap must run outside state");
                for window in ["fixture", "second", "sibling"] {
                    assert!(state.views[window].closing);
                }
                assert!(view.browser.is_some());
                assert_eq!(view.view_id, view_id);
                assert_eq!(view.generation, original.generation);
                events
                    .lock()
                    .unwrap()
                    .push(format!("unmap:{}", view.request.window));
                if failed_window == Some(view.request.window.as_str()) {
                    Err(NativeFailure::SidecarFailed)
                } else {
                    Ok(())
                }
            });
            assert_eq!(
                result,
                failed_window.map_or(Ok(()), |_| Err(NativeFailure::SidecarFailed))
            );
            assert_eq!(
                *events.lock().unwrap(),
                [
                    "unmap:fixture",
                    "unmap:second",
                    "unmap:sibling",
                    "close:fixture:unlocked=true:force=1",
                    "close:second:unlocked=true:force=1",
                    "close:sibling:unlocked=true:force=1",
                ]
            );
            {
                let state = host.state.lock().unwrap();
                assert_eq!(state.live, 5);
                assert_eq!(state.views.len(), 5);
                for window in ["fixture", "second", "sibling"] {
                    assert!(state.views[window].closing);
                    assert!(state.views[window].browser.is_some());
                    assert_eq!(
                        state.views[window].browser.as_ref().unwrap().get_raw(),
                        handles[window]
                    );
                }
                for window in ["other-device", "other-server"] {
                    assert!(!state.views[window].closing);
                    let profile = &state.profiles[&state.views[window].profile];
                    assert!(!profile.removing);
                    assert_eq!(profile.pending.len(), 1);
                }
                assert!(state.profiles[&record.id].pending.is_empty());
                assert!(state.profiles[&records[0].id].pending.is_empty());
                for profile in state.profiles.values() {
                    assert!(profile.path.join("tabs.json").exists());
                }
            }
            host.stopping.store(true, Ordering::Release);
            assert_eq!(host.finish_removals(), Err(NativeFailure::InvalidEvidence));
            let intent = host
                .forgotten_path(&scope.server_id, &scope.device_id)
                .unwrap();
            assert!(
                read_json::<ForgottenScope>(&intent).unwrap()
                    == ForgottenScope::from_connection(&scope).unwrap()
            );
            // Repeated discovery retains the same handles and durable intent.
            host.prepare_forget(&scope).unwrap();
            host.close_scope_with(&scope, |_| Ok(())).unwrap();
            {
                let state = host.state.lock().unwrap();
                assert_eq!(state.live, 5);
                for (window, handle) in &handles {
                    assert_eq!(
                        state.views[window].browser.as_ref().unwrap().get_raw(),
                        *handle
                    );
                }
            }
            for request in requests
                .iter()
                .filter(|r| matches!(r.window.as_str(), "fixture" | "second" | "sibling"))
            {
                host.child_closed(request);
            }
            let state = host.state.lock().unwrap();
            assert_eq!(state.live, 2);
            assert_eq!(state.views.len(), 2);
            assert!(state.views.contains_key("other-device"));
            assert!(state.views.contains_key("other-server"));
            assert!(intent.exists());
            for profile in state.profiles.values() {
                assert!(profile.path.join("tabs.json").exists());
            }
        }
    }

    #[test]
    fn forgotten_scope_keeps_pending_creation_denied_until_its_exact_callback() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        let scope = forgotten_scope(&record);
        {
            let mut state = host.state.lock().unwrap();
            state.views.get_mut("fixture").unwrap().creation_pending = true;
            state.live = 1;
        }
        host.prepare_forget(&scope).unwrap();
        for _ in 0..2 {
            host.close_scope_with(&scope, |view| {
                assert!(view.closing);
                assert!(view.creation_pending);
                assert!(view.browser.is_none());
                Ok(())
            })
            .unwrap();
            assert_eq!(
                host.hide_with("fixture", &record.id, &view_id, |_| panic!(
                    "creation remains pending"
                ))
                .err(),
                Some(NativeFailure::Busy)
            );
        }
        assert_eq!(
            host.prepare_control("fixture", &record.id, &view_id, Action::Reload, None, None),
            Err(NativeFailure::Stopped)
        );
        let events = Arc::new(Mutex::new(Vec::new()));
        let child = browser_fixture(&host, "fixture", &events);
        assert_eq!(
            host.child_created(&record.id, &request, &child),
            Err(NativeFailure::InvalidEvidence)
        );
        assert_eq!(
            *events.lock().unwrap(),
            ["close:fixture:unlocked=true:force=1"]
        );
        let state = host.state.lock().unwrap();
        assert_eq!(state.live, 1);
        assert!(state.views["fixture"].closing);
        assert!(state.views["fixture"].creation_pending);
        drop(state);
        host.child_closed(&request);
        assert_eq!(host.state.lock().unwrap().live, 0);
        assert!(host.state.lock().unwrap().views.is_empty());
        assert!(host.hide("fixture", &record.id, &view_id).is_ok());
    }

    #[test]
    fn hide_retains_ownership_until_native_invisibility() {
        let (_temp, host, record, view_id, _) = active_storage_fixture();
        assert!(matches!(
            host.hide_with("fixture", &record.id, &view_id, |view| {
                assert!(view.closing);
                assert_eq!(view.view_id, view_id);
                Err(NativeFailure::SidecarFailed)
            }),
            Err(NativeFailure::SidecarFailed)
        ));
        assert_eq!(host.state.lock().unwrap().views["fixture"].view_id, view_id);
        assert!(matches!(
            host.prepare_control(
                "fixture",
                &record.id,
                &view_id,
                Action::Navigate,
                Some("https://fixture.test/late"),
                None
            ),
            Err(NativeFailure::Stopped)
        ));
        let mut hidden = false;
        let result = host
            .hide_with("fixture", &record.id, &view_id, |_| {
                hidden = true;
                Ok(())
            })
            .unwrap();
        assert!(hidden);
        assert!(result.tabs.tabs.is_empty());
        assert!(host.state.lock().unwrap().views.is_empty());
    }

    #[test]
    fn hide_waits_for_pending_creation_and_exact_close_callback() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        {
            let mut state = host.state.lock().unwrap();
            state.views.get_mut("fixture").unwrap().creation_pending = true;
            state.live = 1;
        }
        assert!(matches!(
            host.hide_with("fixture", &record.id, &view_id, |_| panic!(
                "pending child cannot prove invisibility"
            )),
            Err(NativeFailure::Busy)
        ));
        assert!(host.state.lock().unwrap().views["fixture"].closing);
        host.child_closed(&request);
        assert_eq!(host.state.lock().unwrap().live, 0);
        assert!(
            host.hide("fixture", &record.id, &view_id)
                .unwrap()
                .tabs
                .tabs
                .is_empty()
        );
        assert!(host.state.lock().unwrap().views.is_empty());
    }

    #[test]
    fn old_close_callback_preserves_replacement_view() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        {
            let mut state = host.state.lock().unwrap();
            let view = state.views.get_mut("fixture").unwrap();
            view.generation += 1;
            view.request.generation = view.generation;
            state.live = 1;
        }
        host.child_closed(&request);
        assert!(host.status("fixture", &record.id, &view_id).is_ok());
        assert_eq!(
            host.state.lock().unwrap().views["fixture"].generation,
            request.generation + 1
        );
    }

    #[test]
    fn repeated_hide_preserves_a_replacement_view() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        assert!(
            host.hide("fixture", &record.id, &view_id)
                .unwrap()
                .tabs
                .tabs
                .is_empty()
        );
        assert!(host.state.lock().unwrap().views.is_empty());
        assert!(
            host.hide("fixture", &record.id, &view_id)
                .unwrap()
                .tabs
                .tabs
                .is_empty()
        );
        let replacement = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &replacement).unwrap();
        host.state.lock().unwrap().views.insert(
            "fixture".into(),
            View {
                profile: record.id.clone(),
                generation: request.generation,
                request,
                browser: None,
                failure: None,
                creation_pending: false,
                closing: false,
                view_id: replacement.clone(),
            },
        );
        assert!(
            host.hide("fixture", &record.id, &view_id)
                .unwrap()
                .tabs
                .tabs
                .is_empty()
        );
        assert!(
            host.hide("unrelated-window", &record.id, &replacement)
                .unwrap()
                .tabs
                .tabs
                .is_empty()
        );
        assert!(host.status("fixture", &record.id, &replacement).is_ok());
    }

    #[test]
    fn shared_child_recreation_attempts_every_view_after_failures() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        let mut requests = vec![request];
        let mut identities = vec![view_id];
        for window in ["second", "third"] {
            let id = uuid::Uuid::now_v7().to_string();
            let mut request = requests[0].clone();
            request.window = window.into();
            let mut state = host.state.lock().unwrap();
            state.reservations.insert(window.into(), id.clone());
            state.views.insert(
                window.into(),
                View {
                    profile: record.id.clone(),
                    generation: request.generation,
                    request: request.clone(),
                    browser: None,
                    failure: None,
                    creation_pending: false,
                    closing: false,
                    view_id: id.clone(),
                },
            );
            requests.push(request);
            identities.push(id);
        }
        let mut attempted = Vec::new();
        assert_eq!(
            host.recreate_children(&record.id, &requests, |request| {
                assert!(host.state.try_lock().is_ok());
                attempted.push(request.window.clone());
                match request.window.as_str() {
                    "fixture" => Err(NativeFailure::SidecarFailed),
                    "second" => Err(NativeFailure::Busy),
                    _ => Ok(()),
                }
            }),
            Err(NativeFailure::SidecarFailed)
        );
        assert_eq!(attempted, ["fixture", "second", "third"]);
        assert!(matches!(
            host.status("fixture", &record.id, &identities[0]),
            Err(NativeFailure::SidecarFailed)
        ));
        assert!(matches!(
            host.status("second", &record.id, &identities[1]),
            Err(NativeFailure::Busy)
        ));
        assert!(host.status("third", &record.id, &identities[2]).is_ok());
    }

    #[test]
    fn asynchronous_creation_failure_is_visible_only_to_its_exact_view() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        assert!(host.status("fixture", &record.id, &view_id).is_ok());
        assert_eq!(
            host.creation_completed(&record.id, &request, Err(NativeFailure::SidecarFailed)),
            Err(NativeFailure::SidecarFailed)
        );
        assert!(matches!(
            host.status("fixture", &record.id, &view_id),
            Err(NativeFailure::SidecarFailed)
        ));
        assert_eq!(
            host.prepare_control("fixture", &record.id, &view_id, Action::Reload, None, None),
            Err(NativeFailure::SidecarFailed)
        );
        let replacement = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &replacement).unwrap();
        let mut newer = request.clone();
        newer.generation += 1;
        host.state.lock().unwrap().views.insert(
            "fixture".into(),
            View {
                profile: record.id.clone(),
                generation: newer.generation,
                request: newer.clone(),
                browser: None,
                failure: None,
                creation_pending: false,
                closing: false,
                view_id: replacement.clone(),
            },
        );
        let _ = host.creation_completed(&record.id, &request, Err(NativeFailure::SidecarFailed));
        assert!(host.status("fixture", &record.id, &replacement).is_ok());
        let _ = host.creation_completed(&record.id, &newer, Err(NativeFailure::SidecarFailed));
        host.state
            .lock()
            .unwrap()
            .profiles
            .get_mut(&record.id)
            .unwrap()
            .removing = true;
        assert!(
            host.status("fixture", &record.id, &replacement)
                .unwrap()
                .removal_pending
        );
    }

    #[test]
    fn after_created_geometry_failure_survives_the_close_callback_and_retry() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        host.state.lock().unwrap().live = 1;
        // The controlled geometry result models an already accepted native
        // child. Its close adapter observes the retained failure before
        // delivering CEF's separate close callback; neither callback
        // uses a real renderer here.
        host.finish_child_created(
            &record.id,
            &request,
            Err(NativeFailure::SidecarFailed),
            || {
                assert!(matches!(
                    host.status("fixture", &record.id, &view_id),
                    Err(NativeFailure::SidecarFailed)
                ));
                host.child_closed(&request);
            },
        );
        assert_eq!(host.state.lock().unwrap().live, 0);
        assert!(matches!(
            host.status("fixture", &record.id, &view_id),
            Err(NativeFailure::SidecarFailed)
        ));
        assert_eq!(
            host.prepare_control("fixture", &record.id, &view_id, Action::Reload, None, None),
            Err(NativeFailure::SidecarFailed)
        );

        let replacement = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &replacement).unwrap();
        let mut newer = request.clone();
        newer.generation += 1;
        host.state.lock().unwrap().views.insert(
            "fixture".into(),
            View {
                profile: record.id.clone(),
                generation: newer.generation,
                request: newer,
                browser: None,
                failure: None,
                creation_pending: false,
                closing: false,
                view_id: replacement.clone(),
            },
        );
        host.finish_child_created(
            &record.id,
            &request,
            Err(NativeFailure::SidecarFailed),
            || {},
        );
        assert!(host.status("fixture", &record.id, &replacement).is_ok());
    }

    #[test]
    fn blocked_storage_does_not_block_native_state_or_publish_superseded_open() {
        let (_temp, host, record) = storage_fixture();
        let original = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &original).unwrap();
        // Model a slow filesystem with storage-worker backpressure. Native
        // state and later reservations must remain usable while
        // preparation is blocked.
        let storage = host.storage.lock().unwrap();
        let (started, wait) = std::sync::mpsc::channel();
        let copy = Arc::clone(&host);
        let worker = thread::spawn(move || {
            started.send(()).unwrap();
            copy.prepare_fixture_open("fixture", &original, None, record, "https://fixture.test/")
        });
        wait.recv_timeout(Duration::from_secs(2)).unwrap();
        assert!(host.state.try_lock().is_ok());
        host.reserve("fixture", &uuid::Uuid::now_v7().to_string())
            .unwrap();
        assert!(!host.begin_exit(0));
        drop(storage);
        assert_eq!(worker.join().unwrap(), Err(NativeFailure::Stopped));
        assert!(host.state.lock().unwrap().profiles.is_empty());
    }

    #[test]
    fn failed_retry_releases_old_view_before_authority_or_storage_work() {
        let (_temp, host, record) = storage_fixture();
        let original = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &original).unwrap();
        host.state.lock().unwrap().views.insert(
            "fixture".into(),
            View {
                profile: record.id.clone(),
                generation: 1,
                request: ViewRequest {
                    window: "fixture".into(),
                    parent: 0,
                    bounds: Bounds {
                        x: 0.0,
                        y: 0.0,
                        width: 100.0,
                        height: 100.0,
                    },
                    scale: 1.0,
                    tab: uuid::Uuid::now_v7().to_string(),
                    generation: 1,
                },
                browser: None,
                failure: None,
                creation_pending: false,
                closing: false,
                view_id: original,
            },
        );
        let replacement = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &replacement).unwrap();
        assert!(host.state.lock().unwrap().views.is_empty());
        assert!(
            host.prepare_fixture_open("fixture", &replacement, None, record, "javascript:alert(1)")
                .is_err()
        );
        let state = host.state.lock().unwrap();
        assert!(state.views.is_empty());
        assert_eq!(state.reservations.get("fixture"), Some(&replacement));
    }

    #[test]
    fn exit_discovers_deletion_since_last_poll_before_releasing_native_shutdown() {
        let (temp, host, mut record) = storage_fixture();
        fs::write(temp.path().join("sidecar.operation"), r#"#!/bin/sh
case "$*" in
  *"connection removed"*) printf '%s\n' '{"version":1,"result":{"connections":[],"next_after":""}}' ;;
  *"connection list"*) printf '%s\n' '{"version":1,"result":{"connections":[]}}' ;;
  *"browser-profile list"*) /bin/cat "$(dirname "$0")/reply.json" ;;
  *) exit 1 ;;
esac
"#).unwrap();
        let reply = temp.path().join("reply.json");
        fs::write(
            &reply,
            serde_json::json!({"version":1,"result":{"profiles":[record],"next_page_token":""}})
                .to_string(),
        )
        .unwrap();
        let cache = browser::profile_path(&host.root.join("profiles"), &record).unwrap();
        host.discover_removals(
            DiscoveryPass::Poll,
            |_| panic!("unexpected scope removal"),
            |_| panic!("unexpected account removal"),
        );
        assert_eq!(fs::read_dir(host.root.join("removals")).unwrap().count(), 0);

        // The owner deletes the account after the last successful poll, then
        // quits during its sleep. Native children may close before discovery.
        record.revision = 2;
        record.data.state = ProfileState::RemovalPending;
        record.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
        fs::write(
            &reply,
            serde_json::json!({"version":1,"result":{"profiles":[record],"next_page_token":""}})
                .to_string(),
        )
        .unwrap();
        host.state.lock().unwrap().discovery_pending = true;
        assert!(host.begin_exit(7));
        assert_eq!(host.state.lock().unwrap().exit_when_ready(), None);
        let mut closed = Vec::new();
        host.discover_removals(
            DiscoveryPass::Exit {
                deadline: Instant::now() + Duration::from_secs(8),
            },
            |_| panic!("unexpected scope removal"),
            |id| closed.push(id),
        );
        assert_eq!(closed, vec![record.id.clone()]);
        assert_eq!(host.finish_discovery(), Some(7));
        assert!(!host.begin_exit(7));
        assert!(cache.exists());
        // Discovery grants no purge before independent native shutdown proof.
        let intent = host
            .root
            .join("removals")
            .join(format!("{}.json", record.id));
        assert!(!read_json::<Removal>(&intent).unwrap().shutdown_confirmed);
        host.finish_removals().unwrap();
        assert!(!cache.exists());
        assert!(intent.exists());
    }

    #[test]
    fn final_discovery_budget_prevents_new_sidecar_reads_and_preserves_close_gate() {
        let (temp, host, _record) = storage_fixture();
        fs::write(
            temp.path().join("sidecar.operation"),
            "#!/bin/sh\n: > \"$(dirname \"$0\")/unexpected-read\"\nexit 1\n",
        )
        .unwrap();
        host.state.lock().unwrap().discovery_pending = true;
        host.state.lock().unwrap().live = 1;
        assert!(host.begin_exit(0));
        host.discover_removals(
            DiscoveryPass::Exit {
                deadline: Instant::now(),
            },
            |_| panic!("unexpected scope removal"),
            |_| panic!("unexpected account removal"),
        );
        assert!(!temp.path().join("unexpected-read").exists());
        assert_eq!(host.finish_discovery(), None);
        assert!(host.begin_exit(0));
        host.state.lock().unwrap().live = 0;
        assert_eq!(host.state.lock().unwrap().exit_when_ready(), Some(0));
        assert!(!host.begin_exit(0));
    }

    #[test]
    fn destroyed_window_cancels_its_view_and_pending_creation_without_purging_profile() {
        let (_temp, host, record, view_id, request) = active_storage_fixture();
        let mut sibling = request.clone();
        sibling.window = "sibling".into();
        {
            let mut state = host.state.lock().unwrap();
            state.profiles.get_mut(&record.id).unwrap().pending =
                vec![request.clone(), sibling.clone()];
            state.reservations.insert("sibling".into(), view_id.clone());
            state.views.insert(
                "sibling".into(),
                View {
                    profile: record.id.clone(),
                    generation: sibling.generation,
                    request: sibling,
                    browser: None,
                    failure: None,
                    creation_pending: false,
                    closing: false,
                    view_id: view_id.clone(),
                },
            );
            state.views.get_mut("fixture").unwrap().creation_pending = true;
            state.live = 1;
        }
        let storage = host.storage.lock().unwrap();
        host.close_window("fixture").unwrap();
        host.close_window("fixture").unwrap();
        {
            let state = host.state.lock().unwrap();
            assert!(!state.views.contains_key("fixture"));
            assert!(!state.reservations.contains_key("fixture"));
            assert!(state.views.contains_key("sibling"));
            assert_eq!(state.profiles[&record.id].pending.len(), 1);
            assert_eq!(state.profiles[&record.id].pending[0].window, "sibling");
            assert!(state.profiles[&record.id].path.join("tabs.json").exists());
            assert!(!state.profiles[&record.id].removing);
            assert_eq!(state.live, 1);
        }
        drop(storage);
        assert_eq!(
            host.prepare_fixture_open(
                "fixture",
                &view_id,
                None,
                record.clone(),
                "https://fixture.test/late"
            ),
            Err(NativeFailure::Stopped)
        );
        assert!(host.begin_exit(0));
        assert_eq!(host.child_closed(&request), Some(0));
        assert_eq!(host.state.lock().unwrap().live, 0);
    }

    #[test]
    fn graceful_exit_drains_accepted_addresses_and_rejects_new_callbacks() {
        let (_temp, host, record, _, request) = active_storage_fixture();
        let path = host.state.lock().unwrap().profiles[&record.id]
            .path
            .join("tabs.json");
        let storage = host.storage.lock().unwrap();
        host.queue_address(
            record.id.clone(),
            request.clone(),
            "https://fixture.test/accepted".into(),
        );
        host.queue_address(
            record.id.clone(),
            request.clone(),
            "https://fixture.test/latest".into(),
        );
        assert!(host.address_running.load(Ordering::Acquire));
        assert!(!host.begin_exit(0));
        host.queue_address(
            record.id.clone(),
            request,
            "https://fixture.test/after-stop".into(),
        );
        drop(storage);
        host.finish_removals().unwrap();
        let persisted: Tabs = read_json(&path).unwrap();
        assert_eq!(persisted.tabs[0].url, "https://fixture.test/latest");
        assert_eq!(
            host.state.lock().unwrap().profiles[&record.id].tabs.tabs[0].url,
            "https://fixture.test/latest"
        );
        assert!(host.addresses.lock().unwrap().is_empty());
        assert!(!host.address_running.load(Ordering::Acquire));
        assert!(host.address_join.lock().unwrap().is_none());
    }

    #[test]
    fn worker_tab_updates_reject_stale_callbacks_and_durable_removal() {
        let (_temp, host, mut record) = storage_fixture();
        let view = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &view).unwrap();
        host.prepare_fixture_open(
            "fixture",
            &view,
            None,
            record.clone(),
            "https://fixture.test/original",
        )
        .unwrap();
        let request = {
            let mut state = host.state.lock().unwrap();
            let request = ViewRequest {
                window: "fixture".into(),
                parent: 0,
                bounds: Bounds {
                    x: 0.0,
                    y: 0.0,
                    width: 100.0,
                    height: 100.0,
                },
                scale: 1.0,
                tab: state.profiles[&record.id].tabs.selected.clone(),
                generation: 1,
            };
            state.views.insert(
                "fixture".into(),
                View {
                    profile: record.id.clone(),
                    generation: 1,
                    request: request.clone(),
                    browser: None,
                    failure: None,
                    creation_pending: false,
                    closing: false,
                    view_id: view.clone(),
                },
            );
            request
        };
        create_page(&host, &record, "https://fixture.test/second");
        let path = host.state.lock().unwrap().profiles[&record.id]
            .path
            .join("tabs.json");
        let persisted: Tabs = read_json(&path).unwrap();
        assert_eq!(persisted.tabs.len(), 2);
        host.persist_address(&record.id, &request, "https://fixture.test/visited")
            .unwrap();
        host.reserve("fixture", &uuid::Uuid::now_v7().to_string())
            .unwrap();
        assert_eq!(
            host.persist_address(&record.id, &request, "https://fixture.test/stale"),
            Err(NativeFailure::Stopped)
        );
        record.revision = 2;
        record.data.state = ProfileState::RemovalPending;
        record.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
        host.prepare_removal(record.clone(), None).unwrap();
        assert_eq!(
            host.prepare_fixture_open(
                "other",
                &view,
                None,
                record.clone(),
                "https://fixture.test/"
            ),
            Err(NativeFailure::Stopped)
        );
        let retained: Tabs = read_json(&path).unwrap();
        assert_eq!(retained.tabs[0].url, "https://fixture.test/visited");
    }

    #[test]
    fn forgotten_connection_purges_whole_original_scope_only_after_native_shutdown() {
        let temp = tempfile::tempdir().unwrap();
        let sidecar = temp.path().join("sidecar");
        fs::write(&sidecar, "#!/bin/sh\nexit 1\n").unwrap();
        fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
        let connector =
            Arc::new(native_fixture_connector(sidecar, temp.path().to_path_buf()).unwrap());
        let host = BrowserHost::new(
            temp.path().join("cef"),
            connector,
            BrowserStorageMode::System,
        )
        .unwrap();
        let scope = SavedConnection {
            version: 1,
            revision: 2,
            id: uuid::Uuid::now_v7().to_string(),
            name: "fixture".into(),
            endpoint: "https://server.test".into(),
            server_id: uuid::Uuid::now_v7().to_string(),
            pairing_id: uuid::Uuid::now_v7().to_string(),
            device_id: uuid::Uuid::now_v7().to_string(),
            state: delidev_desktop::SavedConnectionState::Removed,
            created_at: "2026-09-30T00:00:00Z".into(),
            removal: Some(delidev_desktop::RemovalMetadata {
                request_id: uuid::Uuid::now_v7().to_string(),
                expected_revision: 1,
            }),
        };
        fs::write(
            temp.path().join("sidecar.operation"),
            "#!/bin/sh\nexec /bin/cat \"$2/reply.json\"\n",
        )
        .unwrap();
        browser::write_private(
            &temp.path().join("reply.json"),
            &serde_json::json!({"version": 1, "result": scope}),
        )
        .unwrap();
        let record = ProfileRecord {
            id: uuid::Uuid::now_v7().to_string(),
            revision: 1,
            data: Profile {
                server_id: scope.server_id.clone(),
                device_id: scope.device_id.clone(),
                account_id: uuid::Uuid::now_v7().to_string(),
                state: ProfileState::Active,
                deletion_request_id: String::new(),
            },
        };
        let cache = browser::profile_path(&host.root.join("profiles"), &record).unwrap();
        fs::write(cache.join("cookies"), b"fixture web credentials").unwrap();
        let mut other = record.clone();
        other.data.device_id = uuid::Uuid::now_v7().to_string();
        let preserved = browser::profile_path(&host.root.join("profiles"), &other).unwrap();
        let development_root = host.root.join("development");
        browser::private_dir(&development_root).unwrap();
        let development_profiles = development_root.join("profiles");
        browser::private_dir(&development_profiles).unwrap();
        let development = browser::profile_path(&development_profiles, &record).unwrap();
        let development_preserved = browser::profile_path(&development_profiles, &other).unwrap();
        // A native staging marker without an accepted Go receipt must preserve
        // the entire profile on exit and release denial after fresh evidence.
        let mut paired = scope.clone();
        paired.state = delidev_desktop::SavedConnectionState::Paired;
        paired.revision = 1;
        paired.removal = None;
        browser::write_private(
            &temp.path().join("reply.json"),
            &serde_json::json!({"version": 1, "result": paired}),
        )
        .unwrap();
        assert_eq!(
            host.observer.inspect_saved(&scope.id).unwrap().state,
            delidev_desktop::SavedConnectionState::Paired
        );
        host.prepare_forget(&scope).unwrap();
        host.stopping.store(true, Ordering::Release);
        host.finish_removals().unwrap();
        assert!(cache.exists());
        assert!(preserved.exists());
        assert!(development.exists());
        assert!(
            !host
                .forgotten_path(&scope.server_id, &scope.device_id)
                .unwrap()
                .exists()
        );
        host.stopping.store(false, Ordering::Release);
        browser::write_private(
            &temp.path().join("reply.json"),
            &serde_json::json!({"version": 1, "result": scope}),
        )
        .unwrap();
        host.prepare_forget(&scope).unwrap();
        assert!(
            host.forgotten_path(&scope.server_id, &scope.device_id)
                .unwrap()
                .exists()
        );
        assert!(host.finish_removals().is_err());
        host.stopping.store(true, Ordering::Release);
        host.state.lock().unwrap().live = 1;
        assert!(host.finish_removals().is_err());
        assert!(cache.exists());
        host.state.lock().unwrap().live = 0;
        fs::set_permissions(
            development.parent().unwrap(),
            fs::Permissions::from_mode(0o755),
        )
        .unwrap();
        assert_eq!(host.finish_removals(), Err(NativeFailure::PermissionDenied));
        assert!(
            host.forgotten_path(&scope.server_id, &scope.device_id)
                .unwrap()
                .exists()
        );
        assert!(cache.exists());
        fs::set_permissions(
            development.parent().unwrap(),
            fs::Permissions::from_mode(0o700),
        )
        .unwrap();
        host.finish_removals().unwrap();
        assert!(!cache.parent().unwrap().exists());
        assert!(!development.parent().unwrap().exists());
        assert!(preserved.exists());
        assert!(development_preserved.exists());
        assert_eq!(
            fs::read_dir(host.root.join("forgotten")).unwrap().count(),
            0
        );
    }
    #[test]
    fn depleted_forgotten_budget_preserves_account_purge_progress() {
        let (_temp, host, mut record) = storage_fixture();
        record.data.state = ProfileState::RemovalPending;
        record.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
        let cache = browser::profile_path(&host.root.join("profiles"), &record).unwrap();
        fs::write(cache.join("cookies"), b"fixture web credentials").unwrap();
        host.prepare_removal(record.clone(), None).unwrap();
        let forgotten = ForgottenScope {
            server_id: uuid::Uuid::now_v7().to_string(),
            device_id: uuid::Uuid::now_v7().to_string(),
            connection_id: uuid::Uuid::now_v7().to_string(),
            pairing_id: uuid::Uuid::now_v7().to_string(),
            request_id: uuid::Uuid::now_v7().to_string(),
            expected_revision: 1,
        };
        let deferred = host
            .forgotten_path(&forgotten.server_id, &forgotten.device_id)
            .unwrap();
        browser::write_private(&deferred, &forgotten).unwrap();
        host.stopping.store(true, Ordering::Release);
        let _storage = host.storage.lock().unwrap();
        // Model the exhausted first queue directly, without a 45-second sleep.
        // The production second phase creates its own fresh budget internally.
        host.finish_forgotten(Instant::now() - Duration::from_secs(46))
            .unwrap();
        assert!(deferred.exists());
        host.finish_account_removals().unwrap();
        assert!(!cache.exists());
        assert_eq!(
            read_json::<String>(&host.root.join("removal-cursor.json")).unwrap(),
            record.id
        );
        let intent = host
            .root
            .join("removals")
            .join(format!("{}.json", record.id));
        assert!(read_json::<Removal>(&intent).unwrap().shutdown_confirmed);
        assert!(deferred.exists());
    }

    #[test]
    fn offline_acknowledgment_retains_intent_without_failing_normal_quit() {
        let temp = tempfile::tempdir().unwrap();
        let sidecar = temp.path().join("sidecar");
        fs::write(&sidecar, "#!/bin/sh\nexit 1\n").unwrap();
        fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
        let connector =
            Arc::new(native_fixture_connector(sidecar, temp.path().to_path_buf()).unwrap());
        let host = BrowserHost::new(
            temp.path().join("cef"),
            connector,
            BrowserStorageMode::System,
        )
        .unwrap();
        let record = ProfileRecord {
            id: uuid::Uuid::now_v7().to_string(),
            revision: 2,
            data: Profile {
                server_id: uuid::Uuid::now_v7().to_string(),
                device_id: uuid::Uuid::now_v7().to_string(),
                account_id: uuid::Uuid::now_v7().to_string(),
                state: ProfileState::RemovalPending,
                deletion_request_id: uuid::Uuid::now_v7().to_string(),
            },
        };
        let cache = browser::profile_path(&host.root.join("profiles"), &record).unwrap();
        host.prepare_removal(record.clone(), None).unwrap();
        host.stopping.store(true, Ordering::Release);
        host.finish_removals().unwrap();
        assert!(!cache.exists());
        let intent = host
            .root
            .join("removals")
            .join(format!("{}.json", record.id));
        let retained: Removal = read_json(&intent).unwrap();
        assert!(retained.shutdown_confirmed);
        let original_request = retained.request_id;
        host.finish_removals().unwrap();
        let retry: Removal = read_json(&intent).unwrap();
        assert_eq!(retry.request_id, original_request);
        assert!(!cache.exists());
    }

    #[test]
    fn offline_receipts_rotate_across_exits_without_starving_later_profiles() {
        let (_temp, host, mut record) = storage_fixture();
        record.revision = 2;
        record.data.state = ProfileState::RemovalPending;
        record.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
        let mut caches = Vec::new();
        for _ in 0..65 {
            record.id = uuid::Uuid::now_v7().to_string();
            record.data.account_id = uuid::Uuid::now_v7().to_string();
            caches.push(browser::profile_path(&host.root.join("profiles"), &record).unwrap());
            host.prepare_removal(record.clone(), None).unwrap();
        }
        host.stopping.store(true, Ordering::Release);
        host.finish_removals().unwrap();
        assert_eq!(caches.iter().filter(|cache| cache.exists()).count(), 1);
        assert_eq!(
            fs::read_dir(host.root.join("removals")).unwrap().count(),
            65
        );

        // Restart, rather than reusing in-memory progress. Every receipt
        // remains offline, so only the durable cursor can reach the
        // remaining directory.
        let root = host.root.clone();
        let connector = Arc::clone(&host.connector);
        drop(host);
        let restarted =
            BrowserHost::new(root.clone(), connector, BrowserStorageMode::System).unwrap();
        restarted.stopping.store(true, Ordering::Release);
        restarted.finish_removals().unwrap();
        assert!(caches.iter().all(|cache| !cache.exists()));
        assert_eq!(fs::read_dir(root.join("removals")).unwrap().count(), 65);
    }
    #[test]
    fn pending_cleanup_waits_for_owned_late_flush_and_native_close_proof() {
        let temp = tempfile::tempdir().unwrap();
        let record = ProfileRecord {
            id: uuid::Uuid::now_v7().to_string(),
            revision: 2,
            data: Profile {
                server_id: uuid::Uuid::now_v7().to_string(),
                device_id: uuid::Uuid::now_v7().to_string(),
                account_id: uuid::Uuid::now_v7().to_string(),
                state: ProfileState::RemovalPending,
                deletion_request_id: uuid::Uuid::now_v7().to_string(),
            },
        };
        let mut confirmed = record.clone();
        confirmed.revision += 1;
        confirmed.data.state = ProfileState::Removed;
        let pending = serde_json::json!({"version":1,"result":{"profile":record}}).to_string();
        let completed = serde_json::json!({"version":1,"result":{"profile":confirmed}}).to_string();
        let sidecar = temp.path().join("sidecar");
        fs::write(
            &sidecar,
            format!(
                "#!/bin/sh\ncase \"$*\" in *confirm-removal*) printf '%s\\n' '{completed}' ;; *) \
                 printf '%s\\n' '{pending}' ;; esac\n"
            ),
        )
        .unwrap();
        fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
        let connector =
            Arc::new(native_fixture_connector(sidecar, temp.path().to_path_buf()).unwrap());
        let host = BrowserHost::new(
            temp.path().join("cef"),
            connector,
            BrowserStorageMode::System,
        )
        .unwrap();
        let cache = browser::profile_path(&host.root.join("profiles"), &record).unwrap();
        host.prepare_removal(record.clone(), None).unwrap();
        host.state.lock().unwrap().live = 1;
        let (release, wait) = std::sync::mpsc::channel();
        let path = cache.clone();
        let writer = thread::spawn(move || {
            wait.recv().unwrap();
            fs::write(path.join("late-storage"), b"controlled late flush").unwrap();
        });
        *host.join.lock().unwrap() = Some(writer);
        host.stopping.store(true, Ordering::Release);
        assert!(host.finish_removals().is_err());
        assert!(cache.exists());
        release.send(()).unwrap();
        host.stop();
        assert!(cache.join("late-storage").exists());
        assert!(host.finish_removals().is_err());
        assert!(cache.exists());
        // A controlled adapter supplies the native close proof only after its
        // writer has joined. The real host receives this through CEF callbacks
        // and calls final cleanup only after app.run returns through
        // CefShutdown.
        host.state.lock().unwrap().live = 0;
        host.finish_removals().unwrap();
        assert!(!cache.exists());
        assert_eq!(fs::read_dir(host.root.join("removals")).unwrap().count(), 0);
    }

    fn inventory(host: &BrowserHost, record: &ProfileRecord) -> BrowserState {
        host.read_pages(
            OWNER,
            None,
            record,
            &page_request(PageAction::Inventory, None, None),
        )
        .unwrap()
        .state
    }

    #[test]
    fn inventory_and_presentation_never_create_or_initialize_a_page() {
        let (_temp, host, record) = storage_fixture();
        let profiles = host.root.join("profiles");
        let state = inventory(&host, &record);
        assert!(state.tabs.tabs.is_empty());
        assert!(state.tabs.selected.is_empty());
        assert!(!state.removal_pending);
        // Read-only restoration creates neither storage nor runtime state.
        assert_eq!(fs::read_dir(&profiles).unwrap().count(), 0);
        assert!(host.state.lock().unwrap().profiles.is_empty());
        let view = uuid::Uuid::now_v7().to_string();
        host.reserve("fixture", &view).unwrap();
        assert_eq!(
            host.prepare_open(
                "fixture",
                &view,
                None,
                record.clone(),
                &uuid::Uuid::now_v7().to_string()
            ),
            Err(NativeFailure::InvalidInput)
        );
        assert_eq!(fs::read_dir(&profiles).unwrap().count(), 0);
        assert!(host.state.lock().unwrap().profiles.is_empty());
        assert!(inventory(&host, &record).tabs.tabs.is_empty());
        // Observation of an unknown operation never admits it either.
        let observed = host
            .read_pages(
                OWNER,
                None,
                &record,
                &page_request(PageAction::Observe, None, None),
            )
            .unwrap();
        assert!(observed.receipt.is_none());
    }

    #[test]
    fn explicit_create_adds_exactly_one_page_and_replays_its_original_receipt() {
        let (_temp, host, record) = storage_fixture();
        let request = page_request(PageAction::Create, Some("https://fixture.test/a"), None);
        assert_eq!(
            host.prepare_pages(OWNER, None, &record, &request, commit_fixture),
            Ok(PageAdmission::Admitted)
        );
        let key = operation_key(OWNER, &record.id, request.operation_id.as_deref().unwrap());
        // A second profile mutation is serialized until the first settles.
        let other = page_request(PageAction::Create, Some("https://fixture.test/b"), None);
        assert_eq!(
            host.prepare_pages(OWNER, None, &record, &other, commit_fixture),
            Err(NativeFailure::Busy)
        );
        // A lost reply is observed, then replayed without a second page.
        let pending = host
            .read_pages(
                OWNER,
                None,
                &record,
                &PageRequest {
                    action: PageAction::Observe,
                    operation_id: request.operation_id.clone(),
                    url: None,
                    page_id: None,
                },
            )
            .unwrap();
        let receipt = pending.receipt.unwrap();
        assert_eq!(receipt.phase, PagePhase::CleanupPending);
        assert!(receipt.metadata_committed);
        assert!(!receipt.native_cleanup_complete);
        assert_eq!(pending.state.tabs.tabs.len(), 1);
        assert_eq!(
            host.prepare_pages(OWNER, None, &record, &request, commit_fixture),
            Ok(PageAdmission::Replayed)
        );
        let settled = host
            .complete_page_cleanup(&key, &record.id, true, Ok(()))
            .unwrap();
        let receipt = settled.receipt.unwrap();
        assert_eq!(receipt.phase, PagePhase::Completed);
        assert_eq!(settled.state.tabs.tabs.len(), 1);
        assert_eq!(settled.state.tabs.selected, receipt.page_id);
        assert_eq!(
            host.prepare_pages(OWNER, None, &record, &request, commit_fixture),
            Ok(PageAdmission::Replayed)
        );
        assert_eq!(inventory(&host, &record).tabs.tabs.len(), 1);
        // The same operation identity cannot carry a different request.
        let mut changed = request.clone();
        changed.url = Some("https://fixture.test/changed".into());
        assert_eq!(
            host.prepare_pages(OWNER, None, &record, &changed, commit_fixture),
            Err(NativeFailure::InvalidInput)
        );
        // A different window instance owns no part of the operation.
        assert!(
            host.read_pages(
                "other-owner",
                None,
                &record,
                &PageRequest {
                    action: PageAction::Observe,
                    operation_id: request.operation_id.clone(),
                    url: None,
                    page_id: None,
                },
            )
            .unwrap()
            .receipt
            .is_none()
        );
    }

    #[test]
    fn close_removes_only_the_requested_page_and_last_close_keeps_the_profile() {
        let (_temp, host, record) = storage_fixture();
        let first = create_page(&host, &record, "https://fixture.test/first");
        let second = create_page(&host, &record, "https://fixture.test/second");
        let third = create_page(&host, &record, "https://fixture.test/third");
        // Closing an inactive page preserves the others, in order.
        let receipt = close_page(&host, &record, &second);
        assert_eq!(receipt.phase, PagePhase::Completed);
        assert!(receipt.metadata_committed && receipt.native_cleanup_complete);
        let state = inventory(&host, &record);
        assert_eq!(
            state
                .tabs
                .tabs
                .iter()
                .map(|tab| tab.id.as_str())
                .collect::<Vec<_>>(),
            [first.as_str(), third.as_str()]
        );
        assert_eq!(state.tabs.selected, third);
        // Closing the selected page falls back to its left neighbor.
        close_page(&host, &record, &third);
        assert_eq!(inventory(&host, &record).tabs.selected, first);
        // Closing the final page leaves the profile with zero pages.
        close_page(&host, &record, &first);
        let path = browser::inspect_profile_path(&host.root.join("profiles"), &record).unwrap();
        assert!(path.is_dir());
        let stored: Tabs = read_json(&path.join("tabs.json")).unwrap();
        assert!(stored.tabs.is_empty() && stored.selected.is_empty());
        // Closing an absent page is an idempotent completed operation.
        let absent = close_page(&host, &record, &first);
        assert_eq!(absent.phase, PagePhase::Completed);
        assert!(inventory(&host, &record).tabs.tabs.is_empty());
        // Restoration reads the same durable empty inventory and creates
        // nothing.
        let stored: Tabs = read_json(&path.join("tabs.json")).unwrap();
        assert!(stored.tabs.is_empty() && stored.selected.is_empty());
    }

    #[test]
    fn page_limit_rejects_creation_without_changing_the_inventory() {
        let (_temp, host, record) = storage_fixture();
        for index in 0..PAGE_LIMIT {
            create_page(&host, &record, &format!("https://fixture.test/{index}"));
        }
        let receipt = settle_page(
            &host,
            &record,
            &page_request(PageAction::Create, Some("https://fixture.test/extra"), None),
        );
        assert_eq!(receipt.phase, PagePhase::Failed);
        assert_eq!(receipt.failure, Some(NativeFailure::InvalidInput));
        assert!(!receipt.metadata_committed);
        assert_eq!(inventory(&host, &record).tabs.tabs.len(), PAGE_LIMIT);
    }

    #[test]
    fn invalid_addresses_and_missing_identity_are_not_admitted() {
        let (_temp, host, record) = storage_fixture();
        for request in [
            PageRequest {
                operation_id: None,
                ..page_request(PageAction::Create, Some("https://fixture.test/"), None)
            },
            page_request(PageAction::Create, None, None),
            page_request(PageAction::Create, Some(""), None),
            page_request(PageAction::Close, None, None),
            page_request(PageAction::Close, None, Some("not-a-page")),
            page_request(PageAction::Inventory, None, None),
        ] {
            assert!(
                host.prepare_pages(OWNER, None, &record, &request, commit_fixture)
                    .is_err()
            );
        }
        // An unsafe address is admitted as a failed operation, never stored.
        for url in [
            "javascript:alert(1)",
            "https://user:secret@fixture.test/",
            "file:///etc/passwd",
        ] {
            let receipt = settle_page(
                &host,
                &record,
                &page_request(PageAction::Create, Some(url), None),
            );
            assert_eq!(receipt.phase, PagePhase::Failed);
            assert!(!receipt.metadata_committed);
        }
        assert!(inventory(&host, &record).tabs.tabs.is_empty());
        assert!(host.state.lock().unwrap().page_busy.is_empty());
    }

    #[test]
    fn publication_failure_before_replacement_keeps_the_inventory_and_releases_the_profile() {
        let (_temp, host, record) = storage_fixture();
        let existing = create_page(&host, &record, "https://fixture.test/existing");
        let request = page_request(PageAction::Close, None, Some(&existing));
        host.prepare_pages(OWNER, None, &record, &request, |_staged, _committed| {
            Err(NativeFailure::InvalidEvidence)
        })
        .unwrap();
        let key = operation_key(OWNER, &record.id, request.operation_id.as_deref().unwrap());
        let failed = host.page_receipt(&key).unwrap();
        assert_eq!(failed.phase, PagePhase::Failed);
        assert_eq!(failed.failure, Some(NativeFailure::InvalidEvidence));
        assert!(!failed.metadata_committed);
        // The profile stays serialized until the original operation settles.
        assert_eq!(
            host.prepare_pages(
                OWNER,
                None,
                &record,
                &page_request(PageAction::Close, None, Some(&existing)),
                commit_fixture
            ),
            Err(NativeFailure::Busy)
        );
        let settled = host
            .complete_page_cleanup(&key, &record.id, false, Ok(()))
            .unwrap();
        assert_eq!(settled.receipt.unwrap().phase, PagePhase::Failed);
        assert_eq!(settled.state.tabs.tabs.len(), 1);
        assert_eq!(inventory(&host, &record).tabs.tabs.len(), 1);
        // A fresh operation may now close the page.
        assert_eq!(
            close_page(&host, &record, &existing).phase,
            PagePhase::Completed
        );
        assert!(inventory(&host, &record).tabs.tabs.is_empty());
    }

    #[test]
    fn durability_failure_after_replacement_is_settled_by_observation_only() {
        let (_temp, host, record) = storage_fixture();
        let existing = create_page(&host, &record, "https://fixture.test/existing");
        let request = page_request(PageAction::Close, None, Some(&existing));
        host.prepare_pages(OWNER, None, &record, &request, |staged, committed| {
            staged.replace_with_commit(committed)?;
            Err(NativeFailure::StorageUnavailable)
        })
        .unwrap();
        let key = operation_key(OWNER, &record.id, request.operation_id.as_deref().unwrap());
        let receipt = host.page_receipt(&key).unwrap();
        // Metadata is committed, but the original proof is incomplete.
        assert_eq!(receipt.phase, PagePhase::CleanupPending);
        assert!(receipt.metadata_committed);
        assert!(!receipt.native_cleanup_complete);
        assert_eq!(receipt.failure, Some(NativeFailure::StorageUnavailable));
        assert!(inventory(&host, &record).tabs.tabs.is_empty());
        // Native child cleanup completes, but durability keeps it pending.
        host.complete_page_cleanup(&key, &record.id, true, Ok(()))
            .unwrap();
        assert_eq!(
            host.page_receipt(&key).unwrap().phase,
            PagePhase::CleanupPending
        );
        // Observation retries only the directory sync; the page is not closed
        // or recreated again.
        let observed = host
            .read_pages(
                OWNER,
                None,
                &record,
                &PageRequest {
                    action: PageAction::Observe,
                    operation_id: request.operation_id.clone(),
                    url: None,
                    page_id: None,
                },
            )
            .unwrap();
        let receipt = observed.receipt.unwrap();
        assert_eq!(receipt.phase, PagePhase::Completed);
        assert_eq!(receipt.failure, None);
        assert!(observed.state.tabs.tabs.is_empty());
    }

    #[test]
    fn incomplete_native_cleanup_keeps_the_committed_receipt_pending() {
        let (_temp, host, record) = storage_fixture();
        let existing = create_page(&host, &record, "https://fixture.test/existing");
        let request = page_request(PageAction::Close, None, Some(&existing));
        host.prepare_pages(OWNER, None, &record, &request, commit_fixture)
            .unwrap();
        let key = operation_key(OWNER, &record.id, request.operation_id.as_deref().unwrap());
        let pending = host
            .complete_page_cleanup(&key, &record.id, true, Err(NativeFailure::SidecarFailed))
            .unwrap();
        let receipt = pending.receipt.unwrap();
        assert_eq!(receipt.phase, PagePhase::CleanupPending);
        assert!(receipt.metadata_committed);
        assert!(!receipt.native_cleanup_complete);
        assert_eq!(receipt.failure, Some(NativeFailure::SidecarFailed));
        assert!(pending.state.tabs.tabs.is_empty());
        // The profile is no longer in flight, so a retried cleanup completes
        // the same receipt without another mutation.
        assert!(host.state.lock().unwrap().page_busy.is_empty());
        let settled = host
            .complete_page_cleanup(&key, &record.id, true, Ok(()))
            .unwrap();
        let receipt = settled.receipt.unwrap();
        assert_eq!(receipt.phase, PagePhase::Completed);
        assert_eq!(receipt.failure, None);
    }

    #[test]
    fn released_operation_keeps_its_receipt_and_frees_the_profile() {
        let (_temp, host, record) = storage_fixture();
        let request = page_request(PageAction::Create, Some("https://fixture.test/a"), None);
        host.prepare_pages(OWNER, None, &record, &request, commit_fixture)
            .unwrap();
        host.release_pages(OWNER, &record.id, request.operation_id.as_deref().unwrap());
        assert!(host.state.lock().unwrap().page_busy.is_empty());
        let key = operation_key(OWNER, &record.id, request.operation_id.as_deref().unwrap());
        assert_eq!(
            host.page_receipt(&key).unwrap().phase,
            PagePhase::CleanupPending
        );
        create_page(&host, &record, "https://fixture.test/b");
        assert_eq!(inventory(&host, &record).tabs.tabs.len(), 2);
    }

    #[test]
    fn page_operations_deny_removal_pending_and_stopped_profiles() {
        let (_temp, host, mut record) = storage_fixture();
        let page = create_page(&host, &record, "https://fixture.test/a");
        record.revision = 2;
        record.data.state = ProfileState::RemovalPending;
        record.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
        let pending = host
            .read_pages(
                OWNER,
                None,
                &record,
                &page_request(PageAction::Inventory, None, None),
            )
            .unwrap();
        assert!(pending.state.removal_pending);
        assert!(pending.state.tabs.tabs.is_empty());
        for request in [
            page_request(PageAction::Create, Some("https://fixture.test/b"), None),
            page_request(PageAction::Close, None, Some(&page)),
        ] {
            assert_eq!(
                host.prepare_pages(OWNER, None, &record, &request, commit_fixture),
                Err(NativeFailure::Stopped)
            );
        }
        record.data.state = ProfileState::Active;
        record.data.deletion_request_id.clear();
        host.begin_exit(0);
        assert_eq!(
            host.prepare_pages(
                OWNER,
                None,
                &record,
                &page_request(PageAction::Close, None, Some(&page)),
                commit_fixture
            ),
            Err(NativeFailure::Stopped)
        );
        assert!(
            host.read_pages(
                OWNER,
                None,
                &record,
                &page_request(PageAction::Inventory, None, None),
            )
            .is_err()
        );
    }

    #[test]
    fn loaded_profile_pages_follow_create_and_close_without_a_presentation() {
        let (_temp, host, record, _view_id, original) = active_storage_fixture();
        let second = create_page(&host, &record, "https://fixture.test/second");
        {
            let state = host.state.lock().unwrap();
            let profile = &state.profiles[&record.id];
            assert_eq!(profile.tabs.tabs.len(), 2);
            assert_eq!(profile.tabs.selected, second);
            assert_eq!(profile.storage_revision, 2);
        }
        // The in-memory inventory is authoritative for a loaded profile and
        // includes observed addresses.
        host.persist_address(&record.id, &original, "https://fixture.test/visited")
            .unwrap();
        let state = inventory(&host, &record);
        assert_eq!(state.tabs.tabs[0].url, "https://fixture.test/visited");
        close_page(&host, &record, &second);
        let state = inventory(&host, &record);
        assert_eq!(state.tabs.tabs.len(), 1);
        assert_eq!(state.tabs.selected, original.tab);
    }
}

#[cfg(all(test, unix))]
fn native_fixture_connector(executable: PathBuf, root: PathBuf) -> Result<Connector> {
    use std::os::unix::fs::PermissionsExt;
    let operation = executable.with_extension("operation");
    fs::rename(&executable, &operation).unwrap();
    let adapter = include_str!("resident_fixture.py").replace(
        "OPERATION_PATH",
        &serde_json::to_string(&operation.to_string_lossy()).unwrap(),
    );
    fs::write(&executable, adapter).unwrap();
    fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
    let connector = Connector::new(executable, root)?;
    connector.runtime_endpoint()?;
    Ok(connector)
}
