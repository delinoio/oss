//! Node-only adapter. Workers own plain Rust data; Buffer conversion happens
//! only at the JavaScript boundary. Format engines never depend on this crate.
use std::sync::{
    Arc,
    atomic::{AtomicBool, Ordering},
};

use forge_tree_doc::{Diagnostic, ErrorCode, Presentation};
use napi::{
    Env, Task,
    bindgen_prelude::{AsyncTask, Buffer},
};
use napi_derive::napi;
use uuid::Uuid;

fn native_error(error: Diagnostic) -> napi::Error {
    // Never expose parser errors, XML, document text or host file paths.
    napi::Error::from_reason(
        serde_json::to_string(&error).unwrap_or_else(|_| "{\"code\":\"io\"}".into()),
    )
}

fn cancelled() -> napi::Error {
    native_error(Diagnostic::new(
        ErrorCode::Cancelled,
        "",
        "The native operation was cancelled",
    ))
}

#[napi]
pub struct Cancellation {
    flag: Arc<AtomicBool>,
}

#[napi]
impl Cancellation {
    #[napi(constructor)]
    pub fn new() -> Self {
        Self {
            flag: Arc::new(AtomicBool::new(false)),
        }
    }

    #[napi]
    pub fn cancel(&self) {
        self.flag.store(true, Ordering::Release);
    }
}

#[napi(object)]
pub struct NativeAsset {
    pub id: String,
    pub bytes: Buffer,
}

#[napi(object)]
pub struct NativeOutput {
    pub bytes: Buffer,
    pub model: String,
    pub geometry: String,
}

enum OperationKind {
    Generate,
    Inspect,
    Update,
}

pub struct Operation {
    kind: OperationKind,
    model: String,
    source: Vec<u8>,
    assets: forge_pptx::Assets,
    document_id: Uuid,
    revision: u64,
    cancelled: Arc<AtomicBool>,
}

impl Task for Operation {
    type JsValue = NativeOutput;
    type Output = (Vec<u8>, String, String);

    fn compute(&mut self) -> napi::Result<Self::Output> {
        if self.cancelled.load(Ordering::Acquire) {
            return Err(cancelled());
        }
        let result = (|| -> forge_tree_doc::Result<Self::Output> {
            let (bytes, doc) = match self.kind {
                OperationKind::Inspect => {
                    let imported = forge_pptx::import(&self.source)?;
                    (self.source.clone(), imported.document)
                }
                OperationKind::Generate => {
                    let mut doc: Presentation = forge_tree_doc::parse(self.model.as_bytes())?;
                    doc.assign_ids();
                    let bytes =
                        forge_pptx::generate(&doc, &self.assets, self.document_id, self.revision)?;
                    (bytes, doc)
                }
                OperationKind::Update => {
                    let imported = forge_pptx::import(&self.source)?;
                    let mut doc: Presentation = forge_tree_doc::parse(self.model.as_bytes())?;
                    doc.assign_ids();
                    let mut assets = imported.assets;
                    assets.extend(self.assets.clone());
                    let bytes = forge_pptx::update(
                        &self.source,
                        &imported.document,
                        &imported.bindings,
                        &doc,
                        &assets,
                        imported.document_id,
                        self.revision,
                    )?;
                    (bytes, doc)
                }
            };
            if self.cancelled.load(Ordering::Acquire) {
                return forge_tree_doc::error(
                    ErrorCode::Cancelled,
                    "",
                    "The native operation was cancelled",
                );
            }
            let geometry = forge_tree_doc::layout_for_edit(&doc, Some(&doc))?;
            let model = serde_json::to_string(&doc).map_err(|_| forge_package::failure("model"))?;
            let geometry =
                serde_json::to_string(&geometry).map_err(|_| forge_package::failure("layout"))?;
            Ok((bytes, model, geometry))
        })();
        result.map_err(native_error)
    }

    fn resolve(
        &mut self,
        _env: Env,
        (bytes, model, geometry): Self::Output,
    ) -> napi::Result<NativeOutput> {
        if self.cancelled.load(Ordering::Acquire) {
            return Err(cancelled());
        }
        Ok(NativeOutput {
            bytes: bytes.into(),
            model,
            geometry,
        })
    }
}

#[napi]
pub fn process_pptx(
    operation: String,
    model: String,
    source: Buffer,
    assets: Vec<NativeAsset>,
    document_id: String,
    revision: u32,
    cancellation: &Cancellation,
) -> napi::Result<AsyncTask<Operation>> {
    if model.len() > forge_tree_doc::MAX_JSON_BYTES
        || source.len() > forge_package::MAX_PACKAGE_BYTES
    {
        return Err(native_error(Diagnostic::new(
            ErrorCode::ResourceLimit,
            "",
            "Native input exceeds its resource limit",
        )));
    }
    let kind = match operation.as_str() {
        "generate" => OperationKind::Generate,
        "inspect" => OperationKind::Inspect,
        "update" => OperationKind::Update,
        _ => {
            return Err(native_error(Diagnostic::new(
                ErrorCode::InvalidField,
                "",
                "Unknown native operation",
            )));
        }
    };
    let document_id = Uuid::parse_str(&document_id)
        .ok()
        .filter(|id| id.get_version_num() == 7)
        .ok_or_else(|| {
            native_error(Diagnostic::new(
                ErrorCode::InvalidField,
                "",
                "Invalid document identity",
            ))
        })?;
    let mut loaded = forge_pptx::Assets::new();
    let mut total = 0_usize;
    for asset in assets {
        total = total.checked_add(asset.bytes.len()).ok_or_else(|| {
            native_error(Diagnostic::new(
                ErrorCode::ResourceLimit,
                "",
                "Asset limit exceeded",
            ))
        })?;
        if asset.bytes.len() > forge_package::MAX_PART_BYTES
            || total > forge_package::MAX_PACKAGE_BYTES
        {
            return Err(native_error(Diagnostic::new(
                ErrorCode::ResourceLimit,
                "",
                "Asset limit exceeded",
            )));
        }
        if loaded.insert(asset.id, asset.bytes.to_vec()).is_some() {
            return Err(native_error(Diagnostic::new(
                ErrorCode::DuplicateIdentity,
                "",
                "Duplicate asset identity",
            )));
        }
    }
    Ok(AsyncTask::new(Operation {
        kind,
        model,
        source: source.to_vec(),
        assets: loaded,
        document_id,
        revision: revision.into(),
        cancelled: cancellation.flag.clone(),
    }))
}
