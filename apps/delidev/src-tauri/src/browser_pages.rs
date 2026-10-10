// SPDX-License-Identifier: Apache-2.0
//! Closed, admitted profile-page operations independent of visible children.
use super::*;

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(tag = "action", rename_all = "kebab-case", deny_unknown_fields)]
pub enum PageAction {
    Inventory,
    Create {
        operation_id: String,
        url: String,
    },
    Close {
        operation_id: String,
        page_id: String,
    },
    Observe {
        operation_id: String,
    },
}
#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum PagePhase {
    Uncommitted,
    NativePending,
    Complete,
}
#[derive(Serialize)]
pub struct PageReceipt {
    operation_id: String,
    page_id: String,
    phase: PagePhase,
}
#[derive(Serialize)]
pub struct PageReply {
    pub state: BrowserState,
    operation: Option<PageReceipt>,
    #[serde(skip)]
    pub finish: bool,
    pub pending_pages: Vec<String>,
}
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct ChildClaim {
    window: String,
    generation: u64,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Operation {
    version: u8,
    owner: String,
    profile: String,
    server: String,
    device: String,
    account: String,
    original_revision: u64,
    action: PageAction,
    page: String,
    children: Vec<ChildClaim>,
    #[serde(default)]
    native_closed: bool,
}
impl Operation {
    fn id(&self) -> Result<&str> {
        match &self.action {
            PageAction::Create { operation_id, .. } | PageAction::Close { operation_id, .. } => {
                Ok(operation_id)
            }
            _ => Err(NativeFailure::InvalidEvidence),
        }
    }

    fn validate(&self, owner: &str, record: &ProfileRecord) -> Result<()> {
        canonical_id(self.id()?)?;
        canonical_id(&self.page)?;
        canonical_id(&self.owner)?;
        if self.version != 1
            || self.owner != owner
            || self.profile != record.id
            || self.server != record.data.server_id
            || self.device != record.data.device_id
            || self.account != record.data.account_id
            || self.original_revision == 0
            || self.original_revision > record.revision
            || self.children.len() > 128
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        let mut claims = BTreeSet::new();
        for child in &self.children {
            if child.window.is_empty()
                || child.window.len() > 256
                || child.generation == 0
                || !claims.insert((&child.window, child.generation))
            {
                return Err(NativeFailure::InvalidEvidence);
            }
        }
        match &self.action {
            PageAction::Create { url, .. } if url.len() <= 8192 => {
                let address = url::Url::parse(url).map_err(|_| NativeFailure::InvalidEvidence)?;
                if !matches!(address.scheme(), "http" | "https")
                    || !address.username().is_empty()
                    || address.password().is_some()
                {
                    return Err(NativeFailure::InvalidEvidence);
                }
            }
            PageAction::Close { page_id, .. } if page_id == &self.page => {
                canonical_id(page_id)?;
            }
            _ => return Err(NativeFailure::InvalidEvidence),
        }
        Ok(())
    }

    fn committed(&self, tabs: &Tabs) -> bool {
        self.id()
            .is_ok_and(|id| tabs.operations.iter().any(|operation| operation == id))
    }
}
struct PageCloseFence<'a> {
    host: &'a BrowserHost,
    profile: String,
    page: String,
}
impl Drop for PageCloseFence<'_> {
    fn drop(&mut self) {
        if let Ok(mut state) = self.host.state.lock() {
            state
                .page_close_fences
                .remove(&(self.profile.clone(), self.page.clone()));
        }
    }
}
impl BrowserHost {
    /// Storage worker only. Admission is checked again under final publication;
    /// inventory never initializes tabs and observation never replays a
    /// mutation.
    pub fn prepare_pages(
        &self,
        scope: Option<SavedConnection>,
        record: ProfileRecord,
        owner: &str,
        action: &PageAction,
        admitted: impl Fn() -> Result<()>,
    ) -> Result<PageReply> {
        canonical_id(owner)?;
        self.prepare_profile(None, scope, record.clone(), None)?;
        let _storage = self.storage.lock().map_err(|_| NativeFailure::Busy)?;
        admitted()?;
        let (path, mut tabs, mut policy) = {
            let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            let p = state
                .profiles
                .get(&record.id)
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
        let directory = path.join("page-operations");
        if directory.exists() {
            browser::private_dir(&directory)?;
        }
        if matches!(action, PageAction::Inventory) {
            let pending_pages = self.pending_page_closures(&directory, &record, &tabs)?;
            return Ok(PageReply {
                state: BrowserState {
                    tabs,
                    removal_pending: false,
                },
                operation: None,
                finish: false,
                pending_pages,
            });
        }
        let id = match action {
            PageAction::Create { operation_id, .. }
            | PageAction::Close { operation_id, .. }
            | PageAction::Observe { operation_id } => operation_id,
            PageAction::Inventory => unreachable!(),
        };
        canonical_id(id)?;
        let receipt = directory.join(format!("{id}.json"));
        if receipt.exists() {
            let original: Operation = read_json(&receipt)?;
            original.validate(owner, &record)?;
            if original.id()? != id {
                return Err(NativeFailure::InvalidEvidence);
            }
            if !matches!(action, PageAction::Observe { .. }) && original.action != *action {
                return Err(NativeFailure::InvalidEvidence);
            }
            let mut original = original;
            if original.committed(&tabs)
                && self.page_children_closed(&original)?
                && !original.native_closed
            {
                original.native_closed = true;
                browser::write_private(&receipt, &original)?;
            }
            let pending_pages = self.pending_page_closures(&directory, &record, &tabs)?;
            let mut reply = self.observe_page_operation(&original, tabs)?;
            reply.pending_pages = pending_pages;
            return Ok(reply);
        }
        if matches!(action, PageAction::Observe { .. }) {
            return Err(NativeFailure::InvalidEvidence);
        }
        browser::private_dir(&directory)?;
        let entries: Vec<_> = fs::read_dir(&directory)
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .take(257)
            .collect();
        if entries.len() >= 256 {
            return Err(NativeFailure::Busy);
        }
        // A committed mutation with unsettled original children holds the
        // shared profile gate. Do not replace another window's original
        // operation.
        for entry in entries {
            let previous: Operation =
                read_json(&entry.map_err(|_| NativeFailure::StorageUnavailable)?.path())?;
            previous.validate(&previous.owner, &record)?;
            if previous.committed(&tabs) && !self.page_children_closed(&previous)? {
                return Err(NativeFailure::Busy);
            }
        }
        let page = match action {
            PageAction::Create { url, .. } => {
                if tabs.tabs.len() >= 16 || url.len() > 8192 {
                    return Err(NativeFailure::InvalidInput);
                }
                policy = policy.with_explicit(url)?;
                let page = uuid::Uuid::now_v7().to_string();
                tabs.tabs.push(Tab {
                    id: page.clone(),
                    url: url.clone(),
                });
                tabs.selected = page.clone();
                page
            }
            PageAction::Close { page_id, .. } => {
                canonical_id(page_id)?;
                let index = tabs
                    .tabs
                    .iter()
                    .position(|tab| tab.id == *page_id)
                    .ok_or(NativeFailure::InvalidInput)?;
                tabs.tabs.remove(index);
                if tabs.selected == *page_id {
                    tabs.selected = index
                        .checked_sub(1)
                        .and_then(|index| tabs.tabs.get(index))
                        .or_else(|| tabs.tabs.first())
                        .map(|tab| tab.id.clone())
                        .unwrap_or_default();
                }
                page_id.clone()
            }
            _ => unreachable!(),
        };
        tabs.operations.push(id.clone());
        tabs.validate(&policy)?;
        // Fence new children before capturing all accepted originals, including
        // retiring presentations already replaced by another page.
        let _close_fence = match action {
            PageAction::Close { .. } => {
                let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
                state
                    .page_close_fences
                    .insert((record.id.clone(), page.clone()));
                Some(PageCloseFence {
                    host: self,
                    profile: record.id.clone(),
                    page: page.clone(),
                })
            }
            _ => None,
        };
        let children = {
            let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            let children: Vec<ChildClaim> = state
                .page_children
                .iter()
                .filter(|(_, (profile, tab))| {
                    matches!(action, PageAction::Close { .. })
                        && profile == &record.id
                        && tab == &page
                })
                .map(|((window, generation), _)| ChildClaim {
                    window: window.clone(),
                    generation: *generation,
                })
                .collect();
            if children.len() > 128 {
                return Err(NativeFailure::Busy);
            }
            state.page_close_waiters.extend(
                children
                    .iter()
                    .map(|child| (child.window.clone(), child.generation)),
            );
            children
        };
        let operation = Operation {
            version: 1,
            owner: owner.into(),
            profile: record.id.clone(),
            server: record.data.server_id.clone(),
            device: record.data.device_id.clone(),
            account: record.data.account_id.clone(),
            original_revision: record.revision,
            action: action.clone(),
            page,
            children,
            native_closed: false,
        };
        let staged_tabs = browser::stage_private(&path.join("tabs.json"), &tabs)?;
        let _publication = self.publication.lock().map_err(|_| NativeFailure::Busy)?;
        admitted()?;
        if self.stopping.load(Ordering::Acquire)
            || self
                .state
                .lock()
                .map_err(|_| NativeFailure::Busy)?
                .profiles
                .get(&record.id)
                .is_none_or(|p| p.removing)
        {
            return Err(NativeFailure::Stopped);
        }
        // Write the original target identity before tabs publication. A lost
        // reply or failed publication can only be observed under this receipt;
        // reusing the operation ID never creates a second page or resends
        // Close.
        browser::write_private(&receipt, &operation)?;
        admitted()?;
        staged_tabs.publish()?;
        let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        let p = state
            .profiles
            .get_mut(&record.id)
            .ok_or(NativeFailure::Stopped)?;
        p.tabs = tabs.clone();
        *p.policy.lock().map_err(|_| NativeFailure::Busy)? = policy;
        p.storage_revision = p
            .storage_revision
            .checked_add(1)
            .ok_or(NativeFailure::Stopped)?;
        drop(state);
        tracing::info!(operation = "browser_page", phase = "metadata_committed");
        let mut operation = operation;
        if self.page_children_closed(&operation)? {
            operation.native_closed = true;
            browser::write_private(&receipt, &operation)?;
        }
        let mut reply = self.observe_page_operation(&operation, tabs)?;
        reply.finish = true;
        Ok(reply)
    }

    pub(super) fn finish_page_receipts(&self) -> Result<()> {
        let profiles: Vec<_> = {
            let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
            state
                .profiles
                .values()
                .map(|p| (p.path.clone(), p.record.clone(), p.tabs.clone()))
                .collect()
        };
        let mut failure = None;
        for (path, record, tabs) in profiles {
            if let Err(code) =
                self.pending_page_closures(&path.join("page-operations"), &record, &tabs)
            {
                failure.get_or_insert(code);
            }
        }
        failure.map_or(Ok(()), Err)
    }

    fn pending_page_closures(
        &self,
        directory: &std::path::Path,
        record: &ProfileRecord,
        tabs: &Tabs,
    ) -> Result<Vec<String>> {
        if !directory.exists() {
            return Ok(vec![]);
        }
        browser::private_dir(directory)?;
        let mut pending = Vec::new();
        for (index, entry) in fs::read_dir(directory)
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .take(257)
            .enumerate()
        {
            if index >= 256 {
                return Err(NativeFailure::InvalidEvidence);
            }
            let path = entry.map_err(|_| NativeFailure::StorageUnavailable)?.path();
            let mut operation: Operation = read_json(&path)?;
            operation.validate(&operation.owner, record)?;
            if path.file_name().and_then(|name| name.to_str())
                != Some(format!("{}.json", operation.id()?).as_str())
            {
                return Err(NativeFailure::InvalidEvidence);
            }
            if operation.committed(tabs) {
                if self.page_children_closed(&operation)? {
                    if !operation.native_closed {
                        operation.native_closed = true;
                        browser::write_private(&path, &operation)?;
                    }
                } else if matches!(operation.action, PageAction::Close { .. }) {
                    pending.push(operation.page);
                }
            }
        }
        if pending.len() > 256 {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(pending)
    }

    fn page_children_closed(&self, operation: &Operation) -> Result<bool> {
        if operation.native_closed {
            return Ok(true);
        }
        let state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
        Ok(operation.children.iter().all(|claim| {
            state
                .closed_children
                .contains(&(claim.window.clone(), claim.generation))
        }))
    }

    fn observe_page_operation(&self, operation: &Operation, tabs: Tabs) -> Result<PageReply> {
        let phase = if !operation.committed(&tabs) {
            PagePhase::Uncommitted
        } else if self.page_children_closed(operation)? {
            PagePhase::Complete
        } else {
            PagePhase::NativePending
        };
        Ok(PageReply {
            state: BrowserState {
                tabs,
                removal_pending: false,
            },
            operation: Some(PageReceipt {
                operation_id: operation.id()?.into(),
                page_id: operation.page.clone(),
                phase,
            }),
            finish: false,
            pending_pages: if phase == PagePhase::NativePending
                && matches!(operation.action, PageAction::Close { .. })
            {
                vec![operation.page.clone()]
            } else {
                vec![]
            },
        })
    }

    /// UI loop only. This never commits/replays metadata or exposes a hidden
    /// page. Close owns only children currently displaying its exact page ID.
    pub fn finish_pages(
        self: &Arc<Self>,
        _app: &AppHandle<CefRuntime>,
        profile: &str,
        action: &PageAction,
    ) -> Result<()> {
        match action {
            PageAction::Create { .. } => {}
            PageAction::Close { page_id, .. } => {
                let mut state = self.state.lock().map_err(|_| NativeFailure::Busy)?;
                let mut children = Vec::new();
                let mut failure = None;
                for view in state
                    .views
                    .values_mut()
                    .filter(|view| view.profile == profile && view.request.tab == *page_id)
                {
                    view.closing = true;
                    if let Err(code) = unmap_view(view) {
                        failure.get_or_insert(code);
                    }
                    if let Some(browser) = view.browser.clone() {
                        children.push(browser);
                    }
                }
                drop(state);
                for browser in children {
                    if let Some(host) = browser.host() {
                        host.close_browser(1);
                    }
                }
                if let Some(code) = failure {
                    return Err(code);
                }
            }
            PageAction::Inventory | PageAction::Observe { .. } => {}
        }
        Ok(())
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::{
        super::tests::{active_storage_fixture, storage_fixture},
        *,
    };

    #[test]
    fn inventory_and_restoration_create_no_pages_and_original_create_is_once_only() {
        let (_temp, host, record) = storage_fixture();
        let owner = uuid::Uuid::now_v7().to_string();
        let inventory = host
            .prepare_pages(None, record.clone(), &owner, &PageAction::Inventory, || {
                Ok(())
            })
            .unwrap();
        assert!(inventory.state.tabs.tabs.is_empty());
        let action = PageAction::Create {
            operation_id: uuid::Uuid::now_v7().to_string(),
            url: "https://fixture.test/page".into(),
        };
        let created = host
            .prepare_pages(None, record.clone(), &owner, &action, || Ok(()))
            .unwrap();
        assert!(created.finish);
        assert_eq!(created.state.tabs.tabs.len(), 1);
        let page = created.operation.as_ref().unwrap().page_id.clone();
        let replay = host
            .prepare_pages(None, record.clone(), &owner, &action, || Ok(()))
            .unwrap();
        assert!(!replay.finish);
        assert_eq!(replay.state.tabs.tabs.len(), 1);
        assert_eq!(replay.operation.unwrap().page_id, page);
        assert_eq!(
            host.prepare_pages(
                None,
                record.clone(),
                &uuid::Uuid::now_v7().to_string(),
                &action,
                || Ok(())
            )
            .err(),
            Some(NativeFailure::InvalidEvidence)
        );
        let close = PageAction::Close {
            operation_id: uuid::Uuid::now_v7().to_string(),
            page_id: page,
        };
        host.prepare_pages(None, record.clone(), &owner, &close, || Ok(()))
            .unwrap();
        let restored = host
            .prepare_pages(None, record.clone(), &owner, &PageAction::Inventory, || {
                Ok(())
            })
            .unwrap();
        assert!(restored.state.tabs.tabs.is_empty());
        assert_eq!(restored.state.tabs.selected, "");
        assert!(
            host.prepare_pages(None, record, &owner, &action, || Ok(()))
                .unwrap()
                .state
                .tabs
                .tabs
                .is_empty(),
            "old creation receipt never resurrects a closed page"
        );
    }

    #[test]
    fn interrupted_publication_observes_uncommitted_original_without_retrying_create() {
        let (_temp, host, record) = storage_fixture();
        let owner = uuid::Uuid::now_v7().to_string();
        let action = PageAction::Create {
            operation_id: uuid::Uuid::now_v7().to_string(),
            url: "https://fixture.test/page".into(),
        };
        let count = std::cell::Cell::new(0);
        assert_eq!(
            host.prepare_pages(None, record.clone(), &owner, &action, || {
                count.set(count.get() + 1);
                if count.get() == 3 {
                    Err(NativeFailure::Stopped)
                } else {
                    Ok(())
                }
            })
            .err(),
            Some(NativeFailure::Stopped)
        );
        let observed = host
            .prepare_pages(None, record, &owner, &action, || Ok(()))
            .unwrap();
        assert!(!observed.finish);
        assert!(observed.state.tabs.tabs.is_empty());
        assert_eq!(observed.operation.unwrap().phase, PagePhase::Uncommitted);
    }

    #[test]
    fn committed_close_waits_original_native_cleanup_then_retains_zero_inventory() {
        let (_temp, host, record, _, request) = active_storage_fixture();
        let owner = uuid::Uuid::now_v7().to_string();
        {
            let mut state = host.state.lock().unwrap();
            state.views.get_mut("fixture").unwrap().creation_pending = true;
            state.live = 1;
            state.page_children.insert(
                (request.window.clone(), request.generation),
                (record.id.clone(), request.tab.clone()),
            );
        }
        let id = uuid::Uuid::now_v7().to_string();
        let close = PageAction::Close {
            operation_id: id.clone(),
            page_id: request.tab.clone(),
        };
        let pending = host
            .prepare_pages(None, record.clone(), &owner, &close, || Ok(()))
            .unwrap();
        assert!(pending.state.tabs.tabs.is_empty());
        assert_eq!(pending.operation.unwrap().phase, PagePhase::NativePending);
        let inventory = host
            .prepare_pages(None, record.clone(), &owner, &PageAction::Inventory, || {
                Ok(())
            })
            .unwrap();
        assert_eq!(inventory.pending_pages, vec![request.tab.clone()]);
        let next = PageAction::Create {
            operation_id: uuid::Uuid::now_v7().to_string(),
            url: "https://fixture.test/next".into(),
        };
        assert_eq!(
            host.prepare_pages(None, record.clone(), &owner, &next, || Ok(()))
                .err(),
            Some(NativeFailure::Busy)
        );
        host.child_closed(&request);
        let settled = host
            .prepare_pages(
                None,
                record.clone(),
                &owner,
                &PageAction::Observe { operation_id: id },
                || Ok(()),
            )
            .unwrap();
        assert_eq!(settled.operation.unwrap().phase, PagePhase::Complete);
        host.state.lock().unwrap().closed_children.clear();
        let persisted = host
            .prepare_pages(None, record, &owner, &close, || Ok(()))
            .unwrap();
        assert!(!persisted.finish);
        assert_eq!(persisted.operation.unwrap().phase, PagePhase::Complete);
        assert!(persisted.state.tabs.tabs.is_empty());
    }

    #[test]
    fn close_waits_for_replaced_original_children_and_never_accepts_new_arguments() {
        let (_temp, host, record, _, request) = active_storage_fixture();
        let owner = uuid::Uuid::now_v7().to_string();
        let mut retiring = request.clone();
        retiring.generation += 1;
        {
            let mut state = host.state.lock().unwrap();
            state.live = 2;
            for child in [&request, &retiring] {
                state.page_children.insert(
                    (child.window.clone(), child.generation),
                    (record.id.clone(), request.tab.clone()),
                );
            }
        }
        let id = uuid::Uuid::now_v7().to_string();
        let close = PageAction::Close {
            operation_id: id.clone(),
            page_id: request.tab.clone(),
        };
        assert_eq!(
            host.prepare_pages(None, record.clone(), &owner, &close, || Ok(()))
                .unwrap()
                .operation
                .unwrap()
                .phase,
            PagePhase::NativePending
        );
        host.child_closed(&request);
        assert_eq!(
            host.prepare_pages(
                None,
                record.clone(),
                &owner,
                &PageAction::Observe {
                    operation_id: id.clone()
                },
                || Ok(())
            )
            .unwrap()
            .operation
            .unwrap()
            .phase,
            PagePhase::NativePending
        );
        assert_eq!(
            host.prepare_pages(
                None,
                record.clone(),
                &owner,
                &PageAction::Create {
                    operation_id: id.clone(),
                    url: "https://fixture.test/replacement".into()
                },
                || Ok(())
            )
            .err(),
            Some(NativeFailure::InvalidEvidence)
        );
        host.child_closed(&retiring);
        assert_eq!(
            host.prepare_pages(
                None,
                record,
                &owner,
                &PageAction::Observe { operation_id: id },
                || Ok(())
            )
            .unwrap()
            .operation
            .unwrap()
            .phase,
            PagePhase::Complete
        );
    }

    #[test]
    fn linked_operation_directory_cannot_grant_inventory_or_publication() {
        let (temp, host, record) = storage_fixture();
        let owner = uuid::Uuid::now_v7().to_string();
        host.prepare_pages(None, record.clone(), &owner, &PageAction::Inventory, || {
            Ok(())
        })
        .unwrap();
        let path = host.state.lock().unwrap().profiles[&record.id]
            .path
            .join("page-operations");
        let outside = temp.path().join("foreign-operation-directory");
        browser::private_dir(&outside).unwrap();
        std::os::unix::fs::symlink(&outside, &path).unwrap();
        assert_eq!(
            host.prepare_pages(None, record, &owner, &PageAction::Inventory, || Ok(()))
                .err(),
            Some(NativeFailure::InvalidEvidence)
        );
        assert!(fs::read_dir(outside).unwrap().next().is_none());
    }

    #[test]
    fn profile_mutations_reject_invalid_urls_limits_and_removal_without_fallback_pages() {
        let (_temp, host, mut record) = storage_fixture();
        let owner = uuid::Uuid::now_v7().to_string();
        for url in [
            "file:///fixture",
            "https://user:secret@fixture.test/",
            "javascript:alert(1)",
        ] {
            assert_eq!(
                host.prepare_pages(
                    None,
                    record.clone(),
                    &owner,
                    &PageAction::Create {
                        operation_id: uuid::Uuid::now_v7().to_string(),
                        url: url.into()
                    },
                    || Ok(())
                )
                .err(),
                Some(NativeFailure::InvalidInput)
            );
        }
        for _ in 0..16 {
            host.prepare_pages(
                None,
                record.clone(),
                &owner,
                &PageAction::Create {
                    operation_id: uuid::Uuid::now_v7().to_string(),
                    url: "https://fixture.test/page".into(),
                },
                || Ok(()),
            )
            .unwrap();
        }
        assert_eq!(
            host.prepare_pages(
                None,
                record.clone(),
                &owner,
                &PageAction::Create {
                    operation_id: uuid::Uuid::now_v7().to_string(),
                    url: "https://fixture.test/extra".into()
                },
                || Ok(())
            )
            .err(),
            Some(NativeFailure::InvalidInput)
        );
        record.data.state = ProfileState::RemovalPending;
        record.data.deletion_request_id = uuid::Uuid::now_v7().to_string();
        assert_eq!(
            host.prepare_pages(None, record, &owner, &PageAction::Inventory, || Ok(()))
                .err(),
            Some(NativeFailure::Stopped)
        );
    }
}
