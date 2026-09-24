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
#[derive(Serialize, Deserialize)]
struct PendingExport {
    document_id: Uuid,
    revision: u64,
    source: Source,
    output_digest: String,
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
    persist_durable(tmp, path, parent, overwrite)
}

#[cfg(not(windows))]
fn persist_durable(
    tmp: tempfile::NamedTempFile,
    path: &Path,
    parent: &Path,
    overwrite: bool,
) -> Result<()> {
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
    // File fsync alone does not persist the renamed directory entry. Flush
    // publication of state pointers and exported files before reporting success.
    File::open(parent)
        .and_then(|directory| directory.sync_all())
        .map_err(io_error)
}

#[cfg(windows)]
fn persist_durable(
    tmp: tempfile::NamedTempFile,
    path: &Path,
    parent: &Path,
    overwrite: bool,
) -> Result<()> {
    use std::os::windows::ffi::OsStrExt;

    use windows_sys::Win32::Storage::FileSystem::{
        FILE_ATTRIBUTE_NORMAL, MOVEFILE_REPLACE_EXISTING, MOVEFILE_WRITE_THROUGH, MoveFileExW,
        SetFileAttributesW,
    };
    fn wide(path: &Path) -> Result<Vec<u16>> {
        let mut value: Vec<_> = path.as_os_str().encode_wide().collect();
        if value.contains(&0) {
            return error(ErrorCode::Io, "", "Invalid publication path");
        }
        value.push(0);
        Ok(value)
    }
    // Close the data handle before rename, retaining TempPath cleanup on error.
    // Canonical parents supply extended paths, including long Windows paths.
    let temporary = tmp.into_temp_path();
    let from = wide(&fs::canonicalize(&temporary).map_err(io_error)?)?;
    let to =
        wide(&fs::canonicalize(parent).map_err(io_error)?.join(
            path.file_name().ok_or_else(|| {
                Diagnostic::new(ErrorCode::Io, "", "Missing publication filename")
            })?,
        ))?;
    let flags = MOVEFILE_WRITE_THROUGH
        | if overwrite {
            MOVEFILE_REPLACE_EXISTING
        } else {
            0
        };
    // Like tempfile's persist implementation, clear the owned staging file's
    // temporary attribute before it becomes persistent user or state data.
    // SAFETY: from is a live, NUL-terminated UTF-16 path to our staging file.
    if unsafe { SetFileAttributesW(from.as_ptr(), FILE_ATTRIBUTE_NORMAL) } == 0 {
        return Err(io_error(std::io::Error::last_os_error()));
    }
    // SAFETY: both buffers are live, NUL-terminated UTF-16 paths, and flags
    // request a same-volume rename without deferred or copy/delete fallback.
    if unsafe { MoveFileExW(from.as_ptr(), to.as_ptr(), flags) } == 0 {
        let e = std::io::Error::last_os_error();
        if !overwrite && e.kind() == std::io::ErrorKind::AlreadyExists {
            return error(ErrorCode::OutputExists, "", "Output already exists");
        }
        return Err(io_error(e));
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
        let mut state: State = serde_json::from_slice(&limited_read(
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
        self.recover_export(&mut state, &bytes)?;
        Ok((state, bytes))
    }

    // Earlier builds could leave this journal around a source-replacing export.
    // Production only recovers it; tests still construct those interrupted states.
    #[cfg(test)]
    fn prepare_legacy_source_export(&self, state: &State, bytes: &[u8]) -> Result<()> {
        let pending = PendingExport {
            document_id: state.document_id,
            revision: state.revision,
            source: state
                .source
                .clone()
                .ok_or_else(|| Diagnostic::new(ErrorCode::Io, "", "Missing export source"))?,
            output_digest: sha(bytes),
        };
        atomic_file(
            &self.directory(state.document_id)?.join("export.json"),
            &serde_json::to_vec(&pending).map_err(|_| {
                Diagnostic::new(ErrorCode::Io, "", "Export journal serialization failed")
            })?,
            false,
        )
    }

    fn recover_export(&self, state: &mut State, bytes: &[u8]) -> Result<()> {
        let journal = self.directory(state.document_id)?.join("export.json");
        match fs::symlink_metadata(&journal) {
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
            Err(e) => return Err(io_error(e)),
            Ok(_) => {}
        }
        let pending: PendingExport =
            serde_json::from_slice(&limited_read(&journal, MAX_JSON_BYTES)?)
                .map_err(|_| Diagnostic::new(ErrorCode::Io, "", "Invalid export journal"))?;
        let source = state
            .source
            .as_mut()
            .ok_or_else(|| Diagnostic::new(ErrorCode::Io, "", "Missing export source"))?;
        if pending.document_id != state.document_id
            || pending.revision != state.revision
            || pending.source.path != source.path
            || (pending.source.digest != source.digest && pending.output_digest != source.digest)
            || pending.output_digest != sha(bytes)
        {
            return error(ErrorCode::Io, "", "Invalid pending export identity");
        }
        let actual = sha(&limited_read(&source.path, forge_pptx::MAX_PACKAGE_BYTES)?);
        if actual == pending.output_digest {
            if source.digest != actual {
                source.digest = actual;
                self.commit(state, bytes)?;
            }
        } else if actual != pending.source.digest || source.digest != pending.source.digest {
            return error(
                ErrorCode::SourceChanged,
                "",
                "Source changed during export recovery",
            );
        }
        // A crash can occur before publication or after either atomic commit.
        // Accept only the journal's original bytes or this exact managed revision.
        fs::remove_file(journal).map_err(io_error)?;
        tracing::info!(
            operation = "export",
            stage = "recovery",
            "Recovered source export state"
        );
        Ok(())
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
        let mut truncated = false;
        fn project(
            n: &Node,
            depth: usize,
            budget: &mut usize,
            truncated: &mut bool,
        ) -> serde_json::Value {
            *budget -= 1;
            let mut shallow = n.clone();
            shallow.children.clear();
            let mut v = serde_json::to_value(shallow).unwrap_or_default();
            v["child_count"] = serde_json::json!(n.children.len());
            let mut children = Vec::new();
            if depth > 0 {
                for child in &n.children {
                    if *budget == 0 {
                        break;
                    }
                    children.push(project(child, depth - 1, budget, truncated));
                }
            }
            let omitted = children.len() < n.children.len();
            *truncated |= omitted;
            v["truncated"] = serde_json::json!(omitted);
            v["children"] = serde_json::Value::Array(children);
            v
        }
        let content = if let Some(target) = target {
            validate_target(&target)?;
            let node = state
                .document
                .find(&target)
                .ok_or_else(|| Diagnostic::new(ErrorCode::NotFound, "/target", "Node not found"))?;
            project(node, depth, &mut budget, &mut truncated)
        } else {
            let mut slides = Vec::new();
            for slide in &state.document.slides {
                if budget == 0 {
                    break;
                }
                slides.push(serde_json::json!({"id":slide.id,"key":slide.key,"slide_layout_ref":slide.slide_layout_ref,"content":project(&slide.content,depth,&mut budget,&mut truncated)}));
            }
            truncated |= slides.len() < state.document.slides.len();
            serde_json::json!({"page":state.document.page,"slide_count":state.document.slides.len(),"slides":slides})
        };
        Ok(
            serde_json::json!({"document_id":id,"revision":state.revision,"content":content,"layouts":state.layouts,"truncated":truncated}),
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
        let (state, bytes) = self.current(id)?;
        self.check_source(&state)?;
        self.check_cancelled()?;
        // Resolve the parent independently of the leaf, so a source temporarily
        // absent during an external save cannot bypass the source-path guard.
        let parent = path
            .parent()
            .filter(|p| !p.as_os_str().is_empty())
            .unwrap_or(Path::new("."));
        let path =
            fs::canonicalize(parent)
                .map_err(io_error)?
                .join(path.file_name().ok_or_else(|| {
                    Diagnostic::new(ErrorCode::Io, "/output", "Missing output filename")
                })?);
        if state
            .source
            .as_ref()
            .is_some_and(|s| path == s.path || fs::canonicalize(&path).is_ok_and(|p| p == s.path))
        {
            // A fingerprint check followed by rename can discard an external
            // save in between. Advisory locks cannot exclude other editors.
            // Keep this fail-closed boundary until every supported platform can
            // condition publication on the verified source without a race.
            return error(
                ErrorCode::UnsupportedEdit,
                "/output",
                "Tracked source files cannot be overwritten safely; export to a different output \
                 path",
            );
        }
        forge_pptx::import(&bytes)?;
        self.check_cancelled()?;
        atomic_file(&path, &bytes, overwrite)?;
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn durable_publication_preserves_conflicts_and_cleans_failed_staging() {
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("published");
        atomic_file(&path, b"first", false).unwrap();
        assert_eq!(
            atomic_file(&path, b"conflict", false).unwrap_err().code,
            ErrorCode::OutputExists
        );
        assert_eq!(fs::read(&path).unwrap(), b"first");
        atomic_file(&path, b"replacement", true).unwrap();
        assert_eq!(fs::read(&path).unwrap(), b"replacement");
        #[cfg(windows)]
        {
            use std::os::windows::fs::MetadataExt;

            use windows_sys::Win32::Storage::FileSystem::FILE_ATTRIBUTE_TEMPORARY;
            assert_eq!(
                fs::metadata(&path).unwrap().file_attributes() & FILE_ATTRIBUTE_TEMPORARY,
                0
            );
        }
        assert!(atomic_file(&temp.path().join("invalid\0filename"), b"invalid", false).is_err());
        assert_eq!(fs::read_dir(temp.path()).unwrap().count(), 1);
        assert_eq!(fs::read(&path).unwrap(), b"replacement");
    }

    #[test]
    fn inspection_budget_stops_all_sibling_and_slide_traversal() {
        fn count(v: &serde_json::Value) -> usize {
            match v {
                serde_json::Value::Object(o) => {
                    usize::from(o.contains_key("type")) + o.values().map(count).sum::<usize>()
                }
                serde_json::Value::Array(a) => a.iter().map(count).sum(),
                _ => 0,
            }
        }
        let temp = tempfile::tempdir().unwrap();
        let store = Store::new(Some(temp.path().join("state")), CancellationToken::new()).unwrap();
        let mut document: Presentation = parse(include_bytes!(
            "../../forge-tree-doc/examples/overview.json"
        ))
        .unwrap();
        let branch = Node {
            kind: NodeKind::Column,
            children: vec![
                Node {
                    kind: NodeKind::Shape,
                    ..Default::default()
                };
                200
            ],
            ..Default::default()
        };
        document.slides[0].content = Node {
            kind: NodeKind::Column,
            children: vec![branch; 10],
            ..Default::default()
        };
        let mut second = document.slides[0].clone();
        second.key = Some("second".into());
        document.slides.push(second);
        document.assign_ids();
        validate(&document, false).unwrap();
        let state = State {
            document_id: Uuid::now_v7(),
            revision: 0,
            document,
            bindings: BTreeMap::new(),
            layouts: Vec::new(),
            source: None,
        };
        // Inspection reads the logical tree; no Office serialization is needed
        // to exercise its independent traversal and response limits.
        store.commit(&state, b"inspection fixture").unwrap();
        let result = store.inspect(state.document_id, None, 8).unwrap();
        assert_eq!(count(&result["content"]), 1000);
        assert_eq!(result["content"]["slides"].as_array().unwrap().len(), 1);
        assert_eq!(result["content"]["slide_count"], 2);
        assert_eq!(result["truncated"], true);
        let target = Target {
            node_id: state.document.slides[1].content.id,
            key: None,
        };
        let result = store
            .inspect(state.document_id, Some(target.clone()), 8)
            .unwrap();
        assert_eq!(count(&result["content"]), 1000);
        assert_eq!(result["content"]["children"].as_array().unwrap().len(), 5);
        assert_eq!(result["content"]["child_count"], 10);
        assert_eq!(result["content"]["truncated"], true);
        let result = store.inspect(state.document_id, Some(target), 0).unwrap();
        assert_eq!(count(&result["content"]), 1);
        assert_eq!(result["truncated"], true);
        let leaf = Target {
            node_id: state.document.slides[0].content.children[0].children[0].id,
            key: None,
        };
        assert_eq!(
            store.inspect(state.document_id, Some(leaf), 8).unwrap()["truncated"],
            false
        );
    }

    #[test]
    fn legacy_source_export_recovers_every_publication_boundary() {
        // Model durable disk states on either side of publication and pointer
        // commit, including cancellation and an I/O failure after publication.
        for boundary in ["before", "cancelled", "io_failure", "committed", "external"] {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().join("state");
            let store = Store::new(Some(root.clone()), CancellationToken::new()).unwrap();
            let path = temp.path().join("source.pptx");
            fs::write(
                &path,
                include_bytes!("../../forge-pptx/tests/fixtures/external.pptx"),
            )
            .unwrap();
            let opened = store.open(&path).unwrap();
            let id = opened.document_id;
            let (initial, _) = store.current(id).unwrap();
            let title = initial.document.slides[0]
                .content
                .children
                .iter()
                .find(|n| n.kind == NodeKind::Text)
                .unwrap();
            let patch = Patch {
                dsl_version: 1,
                kind: PatchKind::Patch,
                document_id: id,
                base_revision: 0,
                operations: vec![Operation::SetText {
                    target: Target {
                        node_id: title.id,
                        key: None,
                    },
                    text: "Export recovery".into(),
                    cell: None,
                }],
            };
            store.apply(patch.clone()).unwrap();
            let (mut state, bytes) = store.current(id).unwrap();
            store.prepare_legacy_source_export(&state, &bytes).unwrap();
            if boundary != "before" {
                atomic_file(&path, &bytes, true).unwrap();
                state.source.as_mut().unwrap().digest = sha(&bytes);
            }
            match boundary {
                "cancelled" => {
                    store.cancel.cancel();
                    assert_eq!(
                        store.commit(&state, &bytes).unwrap_err().code,
                        ErrorCode::Cancelled
                    );
                }
                "io_failure" => {
                    let pointer = store.directory(id).unwrap().join("current.json");
                    let saved = fs::read(&pointer).unwrap();
                    fs::remove_file(&pointer).unwrap();
                    fs::create_dir(&pointer).unwrap();
                    assert_eq!(
                        store.commit(&state, &bytes).unwrap_err().code,
                        ErrorCode::Io
                    );
                    fs::remove_dir(&pointer).unwrap();
                    fs::write(&pointer, saved).unwrap();
                }
                "committed" => store.commit(&state, &bytes).unwrap(),
                "external" => fs::write(&path, b"external change").unwrap(),
                _ => {}
            }
            let restarted = Store::new(Some(root), CancellationToken::new()).unwrap();
            if boundary == "external" {
                assert_eq!(
                    restarted.snapshot_bytes(id).unwrap_err().code,
                    ErrorCode::SourceChanged
                );
                assert_eq!(fs::read(&path).unwrap(), b"external change");
                continue;
            }
            assert_eq!(restarted.snapshot_bytes(id).unwrap(), bytes);
            assert!(
                !restarted
                    .directory(id)
                    .unwrap()
                    .join("export.json")
                    .exists()
            );
            let (recovered, _) = restarted.current(id).unwrap();
            assert_eq!(
                recovered.source.unwrap().digest,
                sha(&fs::read(&path).unwrap())
            );
            assert_eq!(recovered.revision, 1);
            let mut next = patch;
            next.base_revision = 1;
            if let Operation::SetText { text, .. } = &mut next.operations[0] {
                *text = "Edit after recovery".into();
            }
            assert_eq!(restarted.apply(next).unwrap().revision, 2);
            assert_eq!(
                restarted.export(id, &path, true).unwrap_err().code,
                ErrorCode::UnsupportedEdit
            );
            let output = temp.path().join("recovered.pptx");
            restarted.export(id, &output, false).unwrap();
            assert_eq!(
                restarted.snapshot_bytes(id).unwrap(),
                fs::read(&output).unwrap()
            );
        }
    }

    #[test]
    #[cfg(unix)]
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
