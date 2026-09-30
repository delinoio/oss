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
    canonical_id,
};
use serde::{Deserialize, Serialize};
use tauri::{AppHandle, Cef, Manager, WebviewWindow};
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
    NewTab,
    SelectTab,
    CloseTab,
    Resize,
    Hide,
}
#[derive(Clone, Serialize)]
pub struct BrowserState {
    pub tabs: Tabs,
    pub removal_pending: bool,
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
    path: PathBuf,
    policy: Arc<Mutex<Policy>>,
    scope: Option<SavedConnection>,
    tabs: Tabs,
    context: Option<RequestContext>,
    ready: bool,
    pending: Vec<ViewRequest>,
    removing: bool,
}
struct View {
    profile: String,
    generation: u64,
    request: ViewRequest,
    browser: Option<Browser>,
    view_id: String,
}
#[derive(Default)]
struct State {
    profiles: BTreeMap<String, RuntimeProfile>,
    views: BTreeMap<String, View>,
    generation: u64,
    reservations: BTreeMap<String, String>,
    live: usize,
    exit_code: Option<i32>,
}
pub struct BrowserHost {
    root: PathBuf,
    connector: Arc<Connector>,
    state: Mutex<State>,
    stopping: AtomicBool,
    join: Mutex<Option<thread::JoinHandle<()>>>,
}
impl BrowserHost {
    pub fn new(root: PathBuf, connector: Arc<Connector>) -> Result<Self> {
        browser::private_dir(&root)?;
        browser::private_dir(&root.join("profiles"))?;
        browser::private_dir(&root.join("removals"))?;
        browser::private_dir(&root.join("forgotten"))?;
        Ok(Self {
            root,
            connector,
            state: Mutex::new(State::default()),
            stopping: AtomicBool::new(false),
            join: Mutex::new(None),
        })
    }

    // Reserve before the asynchronous authority read. Older completions and
    // cleanup calls cannot replace a later presentation instance.
    pub fn reserve(&self, window: &str, view_id: &str) -> Result<()> {
        canonical_id(view_id)?;
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        if self.stopping.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        state.reservations.insert(window.into(), view_id.into());
        Ok(())
    }

    pub fn begin_exit(&self, code: i32) -> bool {
        self.stopping.store(true, Ordering::Release);
        if let Ok(mut state) = self.state.lock() {
            state.exit_code = Some(code);
        }
        self.close_all();
        self.state.lock().map(|s| s.live > 0).unwrap_or(true)
    }

    pub fn start(self: &Arc<Self>, app: AppHandle<Cef>) {
        let host = Arc::clone(self);
        let thread = thread::spawn(move || {
            while !host.stopping.load(Ordering::Acquire) {
                // Completed offline connection tombstones retain the exact non-secret
                // scope even after its client credential has been destroyed. This also
                // discovers removals initiated by the CLI or while the app was stopped.
                let mut after = String::new();
                for _ in 0..16 {
                    if host.stopping.load(Ordering::Acquire) {
                        break;
                    }
                    let Ok(page) = host.connector.removed_connections(&after) else {
                        break;
                    };
                    for scope in page.connections {
                        if let Err(code) = host.prepare_forget(&scope) {
                            tracing::warn!(operation = "browser_scope_removal", ?code);
                            continue;
                        }
                        let copy = Arc::clone(&host);
                        let _ = app.run_on_main_thread(move || copy.close_scope(&scope));
                    }
                    after = page.next_after;
                    if after.is_empty() {
                        break;
                    }
                }
                // Read every saved client, including windows which have not been opened.
                let mut scopes = vec![None];
                if let Ok(saved) = host.connector.saved_connections() {
                    scopes.extend(saved.into_iter().map(Some));
                }
                for scope in scopes {
                    let mut page = String::new();
                    for _ in 0..100 {
                        if host.stopping.load(Ordering::Acquire) {
                            break;
                        }
                        let result = host.connector.browser_query(
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
                        let Some(profiles) = result.get("profiles").and_then(|v| v.as_array())
                        else {
                            break;
                        };
                        for value in profiles {
                            let Ok(record) = serde_json::from_value::<ProfileRecord>(value.clone())
                            else {
                                continue;
                            };
                            if record.validate().is_err()
                                || record.data.state != ProfileState::RemovalPending
                            {
                                continue;
                            }
                            let copy = Arc::clone(&host);
                            let scope = scope.clone();
                            let _ = app.run_on_main_thread(move || {
                                if !copy.stopping.load(Ordering::Acquire) {
                                    if let Err(code) = copy.require_removal(record, scope) {
                                        tracing::warn!(operation = "browser_removal", ?code)
                                    }
                                }
                            });
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
                for _ in 0..50 {
                    if host.stopping.load(Ordering::Acquire) {
                        break;
                    }
                    thread::sleep(Duration::from_millis(100));
                }
            }
        });
        *self.join.lock().unwrap() = Some(thread);
    }

    pub fn stop(&self) {
        self.stopping.store(true, Ordering::Release);
        if let Some(join) = self.join.lock().unwrap().take() {
            let _ = join.join();
        }
    }

    // Called on the UI thread after the trusted command independently reads Go
    // authority.
    pub fn open(
        self: &Arc<Self>,
        app: &AppHandle<Cef>,
        window: &WebviewWindow<Cef>,
        scope: Option<SavedConnection>,
        record: ProfileRecord,
        url: String,
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
        let endpoint = scope
            .as_ref()
            .map(|s| s.endpoint.as_str())
            .unwrap_or("http://127.0.0.1:46310");
        let mut policy = Policy::new(endpoint, &url)?;
        if self
            .forgotten_path(&record.data.server_id, &record.data.device_id)?
            .exists()
        {
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
        if let Some(view) = state.views.remove(window.label()) {
            if let Some(b) = view.browser {
                if let Some(h) = b.host() {
                    h.close_browser(1);
                }
            }
        }
        if !state.profiles.contains_key(&record.id) {
            if state.profiles.len() >= 64 {
                return Err(NativeFailure::Busy);
            }
            let path = browser::profile_path(&self.root.join("profiles"), &record)?;
            if self
                .root
                .join("removals")
                .join(format!("{}.json", record.id))
                .exists()
            {
                return Err(NativeFailure::Stopped);
            }
            let mut tabs: Tabs = if path.join("tabs.json").exists() {
                read_json(&path.join("tabs.json"))?
            } else {
                Tabs::default()
            };
            // Existing local URLs remain private, but a changed product authority cannot
            // reopen a previously allowed loopback tab. Keep the file for explicit
            // recovery.
            for tab in &tabs.tabs {
                policy = policy.with_explicit(&tab.url)?;
            }
            tabs.validate(&policy)?;
            if tabs.tabs.is_empty() {
                let id = uuid::Uuid::now_v7().to_string();
                tabs.tabs.push(Tab {
                    id: id.clone(),
                    url: url.clone(),
                });
                tabs.selected = id;
                browser::write_private(&path.join("tabs.json"), &tabs)?;
            }
            state.profiles.insert(
                record.id.clone(),
                RuntimeProfile {
                    record: record.clone(),
                    path,
                    policy: Arc::new(Mutex::new(policy)),
                    scope: scope.clone(),
                    tabs,
                    context: None,
                    ready: false,
                    pending: vec![],
                    removing: false,
                },
            );
        }
        let p = state.profiles.get_mut(&record.id).unwrap();
        if p.tabs.tabs.is_empty() {
            let next = p
                .policy
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .with_explicit(&url)?;
            let tab = Tab {
                id: uuid::Uuid::now_v7().to_string(),
                url: url.clone(),
            };
            let tabs = Tabs {
                selected: tab.id.clone(),
                tabs: vec![tab],
            };
            browser::write_private(&p.path.join("tabs.json"), &tabs)?;
            *p.policy.lock().map_err(|_| NativeFailure::Busy)? = next;
            p.tabs = tabs;
        }
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
                    cache_path: p.path.to_string_lossy().as_ref().into(),
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

    pub fn control(
        self: &Arc<Self>,
        app: &AppHandle<Cef>,
        window: &WebviewWindow<Cef>,
        profile: &str,
        view_id: &str,
        action: Action,
        url: Option<String>,
        tab: Option<String>,
        bounds: Option<Bounds>,
    ) -> Result<BrowserState> {
        tracing::debug!(operation = "browser_control", ?action);
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
                let next = p
                    .policy
                    .lock()
                    .map_err(|_| NativeFailure::Busy)?
                    .with_explicit(&url)?;
                *p.policy.lock().map_err(|_| NativeFailure::Busy)? = next;
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
                if let Some(b) = &view.browser {
                    if let Some(h) = b.host() {
                        position(h.window_handle(), bounds, view.request.scale, true)?;
                    }
                }
            }
            Action::Hide => {
                if let Some(v) = state.views.remove(window.label()) {
                    if let Some(b) = v.browser {
                        if let Some(h) = b.host() {
                            h.close_browser(1);
                        }
                    }
                }
            }
            Action::NewTab | Action::SelectTab | Action::CloseTab => {
                let p = state.profiles.get_mut(profile).unwrap();
                let mut tabs = p.tabs.clone();
                match action {
                    Action::NewTab => {
                        let url = url.ok_or(NativeFailure::InvalidInput)?;
                        if tabs.tabs.len() >= 16 {
                            return Err(NativeFailure::InvalidInput);
                        };
                        let next = p
                            .policy
                            .lock()
                            .map_err(|_| NativeFailure::Busy)?
                            .with_explicit(&url)?;
                        *p.policy.lock().map_err(|_| NativeFailure::Busy)? = next;
                        let id = uuid::Uuid::now_v7().to_string();
                        tabs.tabs.push(Tab {
                            id: id.clone(),
                            url,
                        });
                        tabs.selected = id;
                    }
                    Action::SelectTab => {
                        let id = tab.ok_or(NativeFailure::InvalidInput)?;
                        if !tabs.tabs.iter().any(|t| t.id == id) {
                            return Err(NativeFailure::InvalidInput);
                        };
                        tabs.selected = id;
                    }
                    Action::CloseTab => {
                        let id = tab.ok_or(NativeFailure::InvalidInput)?;
                        if !tabs.tabs.iter().any(|t| t.id == id) {
                            return Err(NativeFailure::InvalidInput);
                        };
                        tabs.tabs.retain(|t| t.id != id);
                        if tabs.selected == id {
                            tabs.selected =
                                tabs.tabs.first().map(|t| t.id.clone()).unwrap_or_default();
                        }
                    }
                    _ => unreachable!(),
                }
                browser::write_private(&p.path.join("tabs.json"), &tabs)?;
                p.tabs = tabs;
                let context = p.context.clone().ok_or(NativeFailure::Busy)?;
                let selected = p.tabs.selected.clone();
                let labels: Vec<String> = state
                    .views
                    .iter()
                    .filter(|(_, v)| v.profile == profile)
                    .map(|(label, _)| label.clone())
                    .collect();
                let mut requests = Vec::new();
                // Profile tabs are shared: every live user observes the same selected
                // tab, including closing the last tab without deleting the profile.
                for label in labels {
                    state.generation = state
                        .generation
                        .checked_add(1)
                        .ok_or(NativeFailure::Stopped)?;
                    let generation = state.generation;
                    let view = state.views.get_mut(&label).unwrap();
                    if let Some(b) = view.browser.take() {
                        if let Some(h) = b.host() {
                            h.close_browser(1)
                        }
                    }
                    view.generation = generation;
                    view.request.generation = generation;
                    view.request.tab = selected.clone();
                    if !selected.is_empty() {
                        requests.push(view.request.clone());
                    }
                }
                drop(state);
                for request in requests {
                    create(self, app, profile.into(), request, context.clone())?;
                }
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
        Ok(BrowserState {
            tabs: p.tabs.clone(),
            removal_pending: p.removing,
        })
    }

    fn require_removal(&self, record: ProfileRecord, scope: Option<SavedConnection>) -> Result<()> {
        record.validate()?;
        if record.data.state != ProfileState::RemovalPending
            || scope.as_ref().is_some_and(|saved| {
                saved.server_id != record.data.server_id || saved.device_id != record.data.device_id
            })
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
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
        if let Some(p) = state.profiles.get_mut(&record.id) {
            p.removing = true;
            p.record = record.clone();
            p.pending.clear();
        }
        // The durable denial precedes closing every native user. Directory removal is
        // deferred until app.run has returned through the pinned runtime's CefShutdown.
        for v in state.views.values_mut().filter(|v| v.profile == record.id) {
            if let Some(b) = v.browser.take() {
                if let Some(h) = b.host() {
                    h.close_browser(1);
                }
            }
        }
        let _ = path;
        Ok(())
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
        scope.validate()?;
        if scope.device_id.is_empty() {
            return Ok(());
        }
        let path = self.forgotten_path(&scope.server_id, &scope.device_id)?;
        if !path.exists() {
            browser::write_private(
                &path,
                &ForgottenScope {
                    server_id: scope.server_id.clone(),
                    device_id: scope.device_id.clone(),
                },
            )?;
        }
        Ok(())
    }

    pub fn close_scope(&self, scope: &SavedConnection) {
        let Ok(mut state) = self.state.lock() else {
            return;
        };
        let mut profiles = Vec::new();
        for (id, profile) in &mut state.profiles {
            if profile.record.data.server_id == scope.server_id
                && profile.record.data.device_id == scope.device_id
            {
                profile.removing = true;
                profile.pending.clear();
                profiles.push(id.clone());
            }
        }
        for view in state
            .views
            .values_mut()
            .filter(|view| profiles.contains(&view.profile))
        {
            if let Some(browser) = view.browser.take()
                && let Some(host) = browser.host()
            {
                host.close_browser(1);
            }
        }
    }

    fn finish_forgotten(&self) -> Result<()> {
        for entry in fs::read_dir(self.root.join("forgotten"))
            .map_err(|_| NativeFailure::StorageUnavailable)?
        {
            let path = entry.map_err(|_| NativeFailure::StorageUnavailable)?.path();
            if path.extension().and_then(|v| v.to_str()) != Some("json") {
                continue;
            }
            let scope: ForgottenScope = read_json(&path)?;
            if path != self.forgotten_path(&scope.server_id, &scope.device_id)? {
                return Err(NativeFailure::InvalidEvidence);
            }
            let server = self.root.join("profiles").join(&scope.server_id);
            let device = server.join(&scope.device_id);
            if server.exists() {
                browser::private_dir(&server)?;
                if device.exists() {
                    browser::private_dir(&device)?;
                    fs::remove_dir_all(&device).map_err(|_| NativeFailure::StorageUnavailable)?;
                    #[cfg(unix)]
                    std::fs::File::open(&server)
                        .and_then(|f| f.sync_all())
                        .map_err(|_| NativeFailure::StorageUnavailable)?;
                }
            }
            fs::remove_file(path).map_err(|_| NativeFailure::StorageUnavailable)?;
            tracing::info!(
                operation = "browser_scope_removal",
                state = "locally-purged"
            );
        }
        Ok(())
    }

    pub fn close_all(&self) {
        if let Ok(mut state) = self.state.lock() {
            for v in state.views.values_mut() {
                if let Some(b) = v.browser.take() {
                    if let Some(h) = b.host() {
                        h.close_browser(1);
                    }
                }
            }
            for p in state.profiles.values_mut() {
                p.pending.clear();
                p.context.take();
            }
        }
    }

    // No CEF API may run here. The caller has separately observed runtime return.
    pub fn finish_removals(&self) -> Result<()> {
        if !self.stopping.load(Ordering::Acquire)
            || self.join.lock().map_err(|_| NativeFailure::Busy)?.is_some()
            || self.state.lock().map_err(|_| NativeFailure::Busy)?.live != 0
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        self.finish_forgotten()?;
        let mut first_failure = None;
        let started = Instant::now();
        let mut processed = 0;
        for entry in fs::read_dir(self.root.join("removals"))
            .map_err(|_| NativeFailure::StorageUnavailable)?
        {
            let entry = entry.map_err(|_| NativeFailure::StorageUnavailable)?;
            let path = entry.path();
            if path.extension().and_then(|v| v.to_str()) != Some("json") {
                continue;
            }
            if processed >= 64 || started.elapsed() >= Duration::from_secs(45) {
                tracing::warn!(
                    operation = "browser_removal",
                    state = "deferred",
                    code = "cleanup-budget"
                );
                break;
            }
            processed += 1;
            let mut removal: Removal = read_json(&path)?;
            removal.record.validate()?;
            canonical_id(&removal.request_id)?;
            if path.file_stem().and_then(|s| s.to_str()) != Some(removal.record.id.as_str()) {
                return Err(NativeFailure::InvalidEvidence);
            }
            let result = (|| -> Result<()> {
                // The durable original intent already denies reopen. Remove locally
                // after native shutdown even if the owning server is temporarily offline.
                // Its receipt remains pending until a fresh exact status and acknowledgment.
                let cache = browser::profile_path(&self.root.join("profiles"), &removal.record)?;
                removal.shutdown_confirmed = true;
                browser::write_private(&path, &removal)?;
                fs::remove_dir_all(&cache).map_err(|_| NativeFailure::StorageUnavailable)?;
                #[cfg(unix)]
                std::fs::File::open(cache.parent().unwrap())
                    .and_then(|f| f.sync_all())
                    .map_err(|_| NativeFailure::StorageUnavailable)?;
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
            // intent without turning an otherwise normal quit into host failure.
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
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct ForgottenScope {
    server_id: String,
    device_id: String,
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
cef::wrap_request_context_handler! {struct ContextReady{host:Arc<BrowserHost>,app:AppHandle<Cef>,profile:String,}impl RequestContextHandler{
 fn on_request_context_initialized(&self,context:Option<&mut RequestContext>){let Some(context)=context else{return};let pending={let Ok(mut state)=self.host.state.lock()else{return};let Some(p)=state.profiles.get_mut(&self.profile)else{return};if p.removing||self.host.stopping.load(Ordering::Acquire){return};p.ready=true;std::mem::take(&mut p.pending)};for request in pending{if let Err(code)=create(&self.host,&self.app,self.profile.clone(),request,context.clone()){tracing::warn!(operation="browser_create",?code)}}}
}}
fn create(
    host: &Arc<BrowserHost>,
    app: &AppHandle<Cef>,
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
    if std::path::Path::new(&actual) != p.path {
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
    host.state.lock().map_err(|_| NativeFailure::Busy)?.live += 1;
    let info = window_info(&request);
    let mut client = ExternalClient::new(Arc::clone(host), app.clone(), profile, request, policy);
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
        host.state.lock().map_err(|_| NativeFailure::Busy)?.live -= 1;
        return Err(NativeFailure::SidecarFailed);
    };
    Ok(())
}
cef::wrap_client! {struct ExternalClient{host:Arc<BrowserHost>,app:AppHandle<Cef>,profile:String,request:ViewRequest,policy:Arc<Mutex<Policy>>,}impl Client{
 fn life_span_handler(&self)->Option<LifeSpanHandler>{Some(ExternalLife::new(Arc::clone(&self.host),self.app.clone(),self.profile.clone(),self.request.clone()))}
 fn request_handler(&self)->Option<RequestHandler>{Some(ExternalRequests::new(self.policy.clone()))}
 fn display_handler(&self)->Option<DisplayHandler>{Some(ExternalDisplay::new(Arc::clone(&self.host),self.profile.clone(),self.request.clone()))}
 fn permission_handler(&self)->Option<PermissionHandler>{Some(DenyPermissions::new())}
 fn dialog_handler(&self)->Option<DialogHandler>{Some(DenyFiles::new())}
 fn download_handler(&self)->Option<DownloadHandler>{Some(DenyDownloads::new())}
}}
cef::wrap_life_span_handler! {struct ExternalLife{host:Arc<BrowserHost>,app:AppHandle<Cef>,profile:String,request:ViewRequest,}impl LifeSpanHandler{
 fn on_after_created(&self,browser:Option<&mut Browser>){let Some(b)=browser else{return};let Ok(mut state)=self.host.state.lock()else{if let Some(h)=b.host(){h.close_browser(1)};return};let removing=state.profiles.get(&self.profile).is_none_or(|p|p.removing);if !removing&&!self.host.stopping.load(Ordering::Acquire)&&let Some(view)=state.views.get_mut(&self.request.window)&&view.generation==self.request.generation{view.browser=Some(b.clone());if let Some(h)=b.host(){if let Err(code)=position(h.window_handle(),view.request.bounds,view.request.scale,true){tracing::warn!(operation="browser_geometry",?code)}}}else if let Some(h)=b.host(){h.close_browser(1)}}
 fn on_before_popup(&self,_browser:Option<&mut Browser>,_frame:Option<&mut Frame>,_popup_id:i32,_target_url:Option<&CefString>,_target_frame_name:Option<&CefString>,_target_disposition:WindowOpenDisposition,_user_gesture:i32,_popup_features:Option<&PopupFeatures>,_window_info:Option<&mut WindowInfo>,_client:Option<&mut Option<Client>>,_settings:Option<&mut BrowserSettings>,_extra_info:Option<&mut Option<DictionaryValue>>,_no_javascript_access:Option<&mut i32>)->i32{1}
 fn on_before_close(&self,_browser:Option<&mut Browser>){
   let exit = if let Ok(mut state)=self.host.state.lock(){state.live=state.live.saturating_sub(1);if state.live==0{state.exit_code}else{None}}else{None};
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
 fn on_address_change(&self,_browser:Option<&mut Browser>,frame:Option<&mut Frame>,url:Option<&CefString>){if frame.is_none_or(|f|f.is_main()!=1){return};let Some(url)=url else{return};let url=url.to_string();let Ok(mut state)=self.host.state.lock()else{return};if state.views.get(&self.request.window).is_none_or(|v|v.generation!=self.request.generation){return};let Some(p)=state.profiles.get_mut(&self.profile)else{return};if p.removing||!p.policy.lock().is_ok_and(|policy|policy.navigation(&url)){return};let mut tabs=p.tabs.clone();if let Some(t)=tabs.tabs.iter_mut().find(|t|t.id==self.request.tab){t.url=url;if browser::write_private(&p.path.join("tabs.json"),&tabs).is_ok(){p.tabs=tabs}else{tracing::warn!(operation="browser_tabs",code="storage-unavailable")}}}
}}
cef::wrap_permission_handler! {struct DenyPermissions;impl PermissionHandler{
 fn on_request_media_access_permission(&self,_browser:Option<&mut Browser>,_frame:Option<&mut Frame>,_origin:Option<&CefString>,_permissions:u32,callback:Option<&mut MediaAccessCallback>)->i32{if let Some(c)=callback{c.cancel()};1}
 fn on_show_permission_prompt(&self,_browser:Option<&mut Browser>,_id:u64,_origin:Option<&CefString>,_permissions:u32,callback:Option<&mut PermissionPromptCallback>)->i32{if let Some(c)=callback{c.cont(PermissionRequestResult::DENY)};1}
}}
cef::wrap_dialog_handler! {struct DenyFiles;impl DialogHandler{
 fn on_file_dialog(&self,_browser:Option<&mut Browser>,_mode:FileDialogMode,_title:Option<&CefString>,_path:Option<&CefString>,_filters:Option<&mut CefStringList>,_extensions:Option<&mut CefStringList>,_descriptions:Option<&mut CefStringList>,callback:Option<&mut FileDialogCallback>)->i32{if let Some(c)=callback{c.cancel()};1}
}}
cef::wrap_download_handler! {struct DenyDownloads;impl DownloadHandler{fn can_download(&self,_browser:Option<&mut Browser>,_url:Option<&CefString>,_method:Option<&CefString>)->i32{0}}}
fn parent(window: &WebviewWindow<Cef>) -> Result<usize> {
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
        match window
            .as_ref()
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
        info.parent_window = r.parent as *mut std::ffi::c_void;
        info.style = 0x40000000 | 0x10000000 | 0x04000000 | 0x02000000;
    }
    #[cfg(target_os = "linux")]
    {
        info.parent_window = r.parent as _;
    }
    info
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
        return Ok(());
    }
    #[cfg(windows)]
    {
        use windows::Win32::{
            Foundation::HWND,
            UI::WindowsAndMessaging::{SWP_NOACTIVATE, SWP_NOZORDER, SWP_SHOWWINDOW, SetWindowPos},
        };
        unsafe {
            SetWindowPos(
                HWND(handle),
                None,
                (b.x * scale).round() as i32,
                (b.y * scale).round() as i32,
                (b.width * scale).round() as i32,
                (b.height * scale).round() as i32,
                SWP_NOZORDER | SWP_NOACTIVATE | SWP_SHOWWINDOW,
            )
        }
        .map_err(|_| NativeFailure::SidecarFailed)?;
        let _ = visible;
        return Ok(());
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

    use delidev_desktop::browser::Profile;

    use super::*;
    #[test]
    fn forgotten_connection_purges_whole_original_scope_only_after_native_shutdown() {
        let temp = tempfile::tempdir().unwrap();
        let sidecar = temp.path().join("sidecar");
        fs::write(&sidecar, "#!/bin/sh\nexit 1\n").unwrap();
        fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
        let connector = Arc::new(Connector::new(sidecar, temp.path().to_path_buf()).unwrap());
        let host = BrowserHost::new(temp.path().join("cef"), connector).unwrap();
        let scope = SavedConnection {
            version: 1,
            revision: 1,
            id: uuid::Uuid::now_v7().to_string(),
            name: "fixture".into(),
            endpoint: "https://server.test".into(),
            server_id: uuid::Uuid::now_v7().to_string(),
            pairing_id: uuid::Uuid::now_v7().to_string(),
            device_id: uuid::Uuid::now_v7().to_string(),
            state: delidev_desktop::SavedConnectionState::Paired,
            created_at: "2026-09-30T00:00:00Z".into(),
            removal: None,
        };
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
        host.finish_removals().unwrap();
        assert!(!cache.parent().unwrap().exists());
        assert!(preserved.exists());
        assert_eq!(
            fs::read_dir(host.root.join("forgotten")).unwrap().count(),
            0
        );
    }
    #[test]
    fn offline_acknowledgment_retains_intent_without_failing_normal_quit() {
        let temp = tempfile::tempdir().unwrap();
        let sidecar = temp.path().join("sidecar");
        fs::write(&sidecar, "#!/bin/sh\nexit 1\n").unwrap();
        fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
        let connector = Arc::new(Connector::new(sidecar, temp.path().to_path_buf()).unwrap());
        let host = BrowserHost::new(temp.path().join("cef"), connector).unwrap();
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
        host.require_removal(record.clone(), None).unwrap();
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
        let connector = Arc::new(Connector::new(sidecar, temp.path().to_path_buf()).unwrap());
        let host = BrowserHost::new(temp.path().join("cef"), connector).unwrap();
        let cache = browser::profile_path(&host.root.join("profiles"), &record).unwrap();
        host.require_removal(record.clone(), None).unwrap();
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
        // writer has joined. The real host receives this through CEF callbacks and
        // calls final cleanup only after app.run returns through CefShutdown.
        host.state.lock().unwrap().live = 0;
        host.finish_removals().unwrap();
        assert!(!cache.exists());
        assert_eq!(fs::read_dir(host.root.join("removals")).unwrap().count(), 0);
    }
}
