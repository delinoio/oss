use std::{
    collections::BTreeMap,
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
};

use forge_pptx::{Assets, Binding, TemplateLayout, sha};
use forge_tree_doc::*;
use serde::{Deserialize, Serialize};
use tokio_util::sync::CancellationToken;
use uuid::Uuid;

#[derive(Clone)]
pub struct Store {
    pub root: PathBuf,
    pub cancel: CancellationToken,
}

struct DocumentLock(File);

impl Drop for DocumentLock {
    fn drop(&mut self) {
        // Closing one descriptor does not release flock while a duplicate is
        // alive, including one inherited by another thread's fork before exec.
        // Explicit unlock ties ownership to the operation's guard lifetime.
        if fs2::FileExt::unlock(&self.0).is_err() {
            tracing::warn!(
                operation = "unlock",
                stage = "release",
                code = ?ErrorCode::Io,
                "Document lock release failed"
            );
        }
    }
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Source {
    pub path: PathBuf,
    pub digest: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct State {
    pub document_id: Uuid,
    pub revision: u64,
    pub document: Presentation,
    pub bindings: BTreeMap<Uuid, Binding>,
    pub layouts: Vec<TemplateLayout>,
    pub source: Option<Source>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
struct Pointer {
    generation: Uuid,
}
#[derive(Debug, Clone, Serialize, schemars::JsonSchema)]
pub struct Receipt {
    pub document_id: Uuid,
    pub revision: u64,
    pub slides: usize,
    pub changed: bool,
    pub diagnostics: Vec<Diagnostic>,
    pub change_summary: ChangeSummary,
}
#[derive(Debug, Clone, Serialize, schemars::JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum ChangeSummary {
    Committed,
    Unchanged,
}
fn receipt(s: &State, changed: bool) -> Receipt {
    Receipt {
        document_id: s.document_id,
        revision: s.revision,
        slides: s.document.slides.len(),
        changed,
        diagnostics: Vec::new(),
        change_summary: if changed {
            ChangeSummary::Committed
        } else {
            ChangeSummary::Unchanged
        },
    }
}
pub fn limited_read(path: &Path, limit: usize) -> Result<Vec<u8>> {
    let meta = fs::symlink_metadata(path).map_err(io_error)?;
    if !meta.file_type().is_file() || meta.len() > limit as u64 {
        return error(
            ErrorCode::ResourceLimit,
            "",
            "Expected a bounded regular file",
        );
    }
    let mut bytes = Vec::new();
    File::open(path)
        .map_err(io_error)?
        .take(limit as u64 + 1)
        .read_to_end(&mut bytes)
        .map_err(io_error)?;
    if bytes.len() > limit {
        return error(ErrorCode::ResourceLimit, "", "File exceeds input limit");
    }
    Ok(bytes)
}
fn private_dir(path: &Path) -> Result<()> {
    if let Ok(m) = fs::symlink_metadata(path) {
        if !m.is_dir() || m.file_type().is_symlink() {
            return error(ErrorCode::Io, "", "State directory is not a real directory");
        }
    } else {
        fs::create_dir_all(path).map_err(io_error)?;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o700)).map_err(io_error)?;
    }
    Ok(())
}
fn atomic_file(path: &Path, bytes: &[u8], overwrite: bool) -> Result<()> {
    let parent = path
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    if let Ok(meta) = fs::symlink_metadata(path) {
        if !meta.file_type().is_file() {
            return error(ErrorCode::Io, "", "Output must be a regular file");
        }
        if !overwrite {
            return error(
                ErrorCode::OutputExists,
                "",
                "Output already exists; explicit overwrite is required",
            );
        }
    }
    let mut tmp = tempfile::NamedTempFile::new_in(parent).map_err(io_error)?;
    tmp.write_all(bytes).map_err(io_error)?;
    tmp.as_file().sync_all().map_err(io_error)?;
    if overwrite {
        tmp.persist(path).map_err(|e| io_error(e.error))?;
    } else {
        tmp.persist_noclobber(path).map_err(|e| {
            if e.error.kind() == std::io::ErrorKind::AlreadyExists {
                Diagnostic::new(ErrorCode::OutputExists, "", "Output already exists")
            } else {
                io_error(e.error)
            }
        })?;
    }
    Ok(())
}
impl Store {
    pub fn new(root: Option<PathBuf>, cancel: CancellationToken) -> Result<Self> {
        let root = match root {
            Some(root) => root,
            None => default_root()?,
        };
        private_dir(&root)?;
        for sub in ["documents", "assets", "locks"] {
            private_dir(&root.join(sub))?;
        }
        Ok(Self { root, cancel })
    }

    pub fn check_cancelled(&self) -> Result<()> {
        if self.cancel.is_cancelled() {
            return error(
                ErrorCode::Cancelled,
                "",
                "Operation cancelled before publication",
            );
        }
        Ok(())
    }

    fn directory(&self, id: Uuid) -> Result<PathBuf> {
        if id.get_version_num() != 7 {
            return error(
                ErrorCode::InvalidReference,
                "/document_id",
                "Document ID must be UUID v7",
            );
        }
        Ok(self.root.join("documents").join(id.to_string()))
    }

    fn lock(&self, id: Uuid) -> Result<DocumentLock> {
        let path = self.root.join("locks").join(format!("{id}.lock"));
        if fs::symlink_metadata(&path).is_ok_and(|m| !m.file_type().is_file()) {
            return error(ErrorCode::Io, "", "Invalid lock file");
        }
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(path)
            .map_err(io_error)?;
        fs2::FileExt::try_lock_exclusive(&file)
            .map_err(|_| Diagnostic::new(ErrorCode::Busy, "", "Document is already in use"))?;
        Ok(DocumentLock(file))
    }

    fn current(&self, id: Uuid) -> Result<(State, Vec<u8>)> {
        let dir = self.directory(id)?;
        let pointer: Pointer =
            serde_json::from_slice(&limited_read(&dir.join("current.json"), 1024)?)
                .map_err(|_| Diagnostic::new(ErrorCode::Io, "", "Invalid state pointer"))?;
        let generation = dir.join(pointer.generation.to_string());
        let state: State = serde_json::from_slice(&limited_read(
            &generation.join("state.json"),
            MAX_JSON_BYTES,
        )?)
        .map_err(|_| Diagnostic::new(ErrorCode::Io, "", "Invalid state snapshot"))?;
        if state.document_id != id {
            return error(ErrorCode::Io, "", "State identity mismatch");
        }
        let bytes = limited_read(
            &generation.join("document.pptx"),
            forge_pptx::MAX_PACKAGE_BYTES,
        )?;
        Ok((state, bytes))
    }

    fn check_source(&self, state: &State) -> Result<()> {
        if let Some(source) = &state.source
            && sha(&limited_read(&source.path, forge_pptx::MAX_PACKAGE_BYTES)?) != source.digest
        {
            return error(
                ErrorCode::SourceChanged,
                "",
                "Source file changed; reopen it before editing or exporting",
            );
        }
        Ok(())
    }

    fn commit(&self, state: &State, bytes: &[u8]) -> Result<()> {
        let dir = self.directory(state.document_id)?;
        private_dir(&dir)?;
        let generation = Uuid::now_v7();
        let staging = tempfile::Builder::new()
            .prefix(".revision-")
            .tempdir_in(&dir)
            .map_err(io_error)?;
        let json = serde_json::to_vec(state)
            .map_err(|_| Diagnostic::new(ErrorCode::Io, "", "State serialization failed"))?;
        if json.len() > MAX_JSON_BYTES {
            return error(
                ErrorCode::ResourceLimit,
                "",
                "Document state exceeds 16 MiB",
            );
        }
        atomic_file(&staging.path().join("state.json"), &json, false)?;
        atomic_file(&staging.path().join("document.pptx"), bytes, false)?;
        self.check_cancelled()?;
        let final_path = dir.join(generation.to_string());
        fs::rename(staging.path(), &final_path).map_err(io_error)?;
        let pointer = serde_json::to_vec(&Pointer { generation })
            .map_err(|_| Diagnostic::new(ErrorCode::Io, "", "Pointer serialization failed"))?;
        self.check_cancelled()?;
        atomic_file(&dir.join("current.json"), &pointer, true)?;
        Ok(())
    }

    pub fn register_asset(&self, path: &Path) -> Result<serde_json::Value> {
        self.register_bytes(&limited_read(path, 64 * 1024 * 1024)?)
    }

    fn register_bytes(&self, bytes: &[u8]) -> Result<serde_json::Value> {
        forge_pptx::validate_image(bytes)?;
        let digest = sha(bytes);
        let handle = format!("asset_{digest}");
        let path = self.root.join("assets").join(&handle);
        self.check_cancelled()?;
        if path.exists() {
            if sha(&limited_read(&path, 64 * 1024 * 1024)?) != digest {
                return error(ErrorCode::Io, "", "Stored asset checksum mismatch");
            }
        } else {
            match atomic_file(&path, bytes, false) {
                Ok(()) => {}
                Err(e) if e.code == ErrorCode::OutputExists => {}
                Err(e) => return Err(e),
            }
        }
        Ok(serde_json::json!({"handle":handle,"sha256":digest,"bytes":bytes.len()}))
    }

    fn assets(&self, doc: &Presentation) -> Result<Assets> {
        let mut assets = Assets::new();
        for asset in doc.assets.values() {
            if asset.handle.len() != 70
                || !asset.handle.starts_with("asset_")
                || !asset.handle[6..].bytes().all(|b| b.is_ascii_hexdigit())
            {
                return error(
                    ErrorCode::InvalidReference,
                    "/assets",
                    "Asset handle is invalid",
                );
            }
            let bytes = limited_read(
                &self.root.join("assets").join(&asset.handle),
                64 * 1024 * 1024,
            )?;
            if sha(&bytes) != asset.handle[6..] {
                return error(ErrorCode::Io, "/assets", "Asset checksum mismatch");
            }
            assets.insert(asset.handle.clone(), bytes);
        }
        Ok(assets)
    }

    pub fn create(&self, mut document: Presentation) -> Result<Receipt> {
        validate(&document, false)?;
        document.assign_ids();
        let id = Uuid::now_v7();
        let _guard = self.lock(id)?;
        let assets = self.assets(&document)?;
        let bytes = forge_pptx::generate(&document, &assets, id, 0)?;
        let imported = forge_pptx::import(&bytes)?;
        let state = State {
            document_id: id,
            revision: 0,
            document,
            bindings: imported.bindings,
            layouts: imported.layouts,
            source: None,
        };
        self.commit(&state, &bytes)?;
        Ok(receipt(&state, true))
    }

    pub fn open(&self, path: &Path) -> Result<Receipt> {
        let bytes = limited_read(path, forge_pptx::MAX_PACKAGE_BYTES)?;
        let imported = forge_pptx::import(&bytes)?;
        let id = imported.document_id;
        let _guard = self.lock(id)?;
        if self.directory(id)?.join("current.json").exists() {
            let (state, existing) = self.current(id)?;
            if sha(&existing) == sha(&bytes) {
                return Ok(receipt(&state, false));
            }
            return error(
                ErrorCode::RevisionConflict,
                "",
                "A different revision of this document is already open",
            );
        }
        for (handle, data) in &imported.assets {
            let registered = self.register_bytes(data)?;
            if registered["handle"].as_str() != Some(handle.as_str()) {
                return error(
                    ErrorCode::InvalidReference,
                    "/assets",
                    "Embedded asset handle does not match its content",
                );
            }
        }
        let state = State {
            document_id: id,
            revision: imported.revision,
            document: imported.document,
            bindings: imported.bindings,
            layouts: imported.layouts,
            source: Some(Source {
                path: fs::canonicalize(path).map_err(io_error)?,
                digest: sha(&bytes),
            }),
        };
        self.commit(&state, &bytes)?;
        Ok(receipt(&state, true))
    }

    pub fn inspect(
        &self,
        id: Uuid,
        target: Option<Target>,
        depth: usize,
    ) -> Result<serde_json::Value> {
        if depth > 8 {
            return error(
                ErrorCode::ResourceLimit,
                "/depth",
                "Inspection depth is limited to 8",
            );
        }
        let _guard = self.lock(id)?;
        let (state, _) = self.current(id)?;
        let mut budget = 1000;
        fn project(n: &Node, depth: usize, budget: &mut usize) -> serde_json::Value {
            if *budget == 0 {
                return serde_json::json!({"truncated":true});
            }
            *budget -= 1;
            let mut shallow = n.clone();
            shallow.children.clear();
            let mut v = serde_json::to_value(shallow).unwrap_or_default();
            v["child_count"] = serde_json::json!(n.children.len());
            v["children"] = if depth == 0 {
                serde_json::json!([])
            } else {
                serde_json::Value::Array(
                    n.children
                        .iter()
                        .take(1000)
                        .map(|n| project(n, depth - 1, budget))
                        .collect(),
                )
            };
            v
        }
        let content = if let Some(target) = target {
            validate_target(&target)?;
            let node = state
                .document
                .find(&target)
                .ok_or_else(|| Diagnostic::new(ErrorCode::NotFound, "/target", "Node not found"))?;
            project(node, depth, &mut budget)
        } else {
            serde_json::json!({"page":state.document.page,"slides":state.document.slides.iter().take(1000).map(|s|serde_json::json!({"id":s.id,"key":s.key,"slide_layout_ref":s.slide_layout_ref,"content":project(&s.content,depth,&mut budget)})).collect::<Vec<_>>()})
        };
        Ok(
            serde_json::json!({"document_id":id,"revision":state.revision,"content":content,"layouts":state.layouts,"truncated":budget==0}),
        )
    }

    pub fn apply(&self, patch: Patch) -> Result<Receipt> {
        let _guard = self.lock(patch.document_id)?;
        let (mut state, bytes) = self.current(patch.document_id)?;
        self.check_source(&state)?;
        let next = apply_patch(&state.document, &patch, state.document_id, state.revision)?;
        if next == state.document {
            return Ok(receipt(&state, false));
        }
        let revision = state
            .revision
            .checked_add(1)
            .ok_or_else(|| Diagnostic::new(ErrorCode::ResourceLimit, "", "Revision exhausted"))?;
        let assets = self.assets(&next)?;
        let bytes = forge_pptx::update(
            &bytes,
            &state.document,
            &state.bindings,
            &next,
            &assets,
            state.document_id,
            revision,
        )?;
        let imported = forge_pptx::import(&bytes)?;
        state.document = next;
        state.bindings = imported.bindings;
        state.revision = revision;
        self.check_source(&state)?;
        self.commit(&state, &bytes)?;
        Ok(receipt(&state, true))
    }

    pub fn export(&self, id: Uuid, path: &Path, overwrite: bool) -> Result<serde_json::Value> {
        let _guard = self.lock(id)?;
        let (mut state, bytes) = self.current(id)?;
        self.check_source(&state)?;
        forge_pptx::import(&bytes)?;
        self.check_cancelled()?;
        atomic_file(path, &bytes, overwrite)?;
        if state
            .source
            .as_ref()
            .is_some_and(|s| fs::canonicalize(path).is_ok_and(|p| p == s.path))
        {
            state.source.as_mut().unwrap().digest = sha(&bytes);
            self.commit(&state, &bytes)?;
        }
        Ok(
            serde_json::json!({"document_id":id,"revision":state.revision,"path":fs::canonicalize(path).map_err(io_error)?,"sha256":sha(&bytes),"mime_type":"application/vnd.openxmlformats-officedocument.presentationml.presentation"}),
        )
    }

    pub fn snapshot_bytes(&self, id: Uuid) -> Result<Vec<u8>> {
        let _guard = self.lock(id)?;
        let (state, bytes) = self.current(id)?;
        self.check_source(&state)?;
        Ok(bytes)
    }

    pub fn close(&self, id: Uuid) -> Result<serde_json::Value> {
        let _guard = self.lock(id)?;
        let dir = self.directory(id)?;
        self.current(id)?;
        self.check_cancelled()?;
        fs::remove_dir_all(dir).map_err(io_error)?;
        Ok(serde_json::json!({"document_id":id,"closed":true}))
    }
}
pub fn default_root() -> Result<PathBuf> {
    #[cfg(target_os = "windows")]
    let base = std::env::var_os("LOCALAPPDATA").map(PathBuf::from);
    #[cfg(target_os = "macos")]
    let base =
        std::env::var_os("HOME").map(|p| PathBuf::from(p).join("Library/Application Support"));
    #[cfg(not(any(target_os = "windows", target_os = "macos")))]
    let base = std::env::var_os("XDG_DATA_HOME")
        .map(PathBuf::from)
        .or_else(|| std::env::var_os("HOME").map(|p| PathBuf::from(p).join(".local/share")));
    let base = base.filter(|p| p.is_absolute()).ok_or_else(|| {
        Diagnostic::new(
            ErrorCode::Io,
            "",
            "User data directory unavailable; specify --state-dir",
        )
    })?;
    Ok(base.join("delino-forge"))
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    #[test]
    fn lock_release_does_not_wait_for_a_duplicated_descriptor() {
        let temp = tempfile::tempdir().unwrap();
        let store = Store::new(Some(temp.path().join("state")), CancellationToken::new()).unwrap();
        let id = Uuid::now_v7();
        let guard = store.lock(id).unwrap();
        // dup and fork share the same open file description on Unix. Retain a
        // duplicate to model another thread's child before it reaches exec.
        let inherited = guard.0.try_clone().unwrap();
        assert!(matches!(store.lock(id), Err(e) if e.code == ErrorCode::Busy));
        drop(guard);
        let next = store
            .lock(id)
            .expect("Completed operation must release its lock");
        drop(inherited);
        assert!(matches!(store.lock(id), Err(e) if e.code == ErrorCode::Busy));
        drop(next);
        assert!(store.lock(id).is_ok());
    }
}
