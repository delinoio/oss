//! Node-only adapter. Workers own plain Rust data; Buffer conversion happens
//! only at the JavaScript boundary. Format engines never depend on this crate.
use std::sync::{
    Arc,
    atomic::{AtomicBool, Ordering},
};

use forge_tree_doc::{Diagnostic, ErrorCode};
mod diagnostics;
mod docx;
mod pdf;
mod pptx;
mod scene;
pub use scene::{SceneAssetOperation, validate_scene_asset};
mod sfx;
mod sprite;
mod xlsx;

enum Format {
    Pptx,
    Docx,
    Xlsx,
    Pdf,
    Glb,
    Fbx,
    Sprite,
    Wav,
}
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

#[derive(Default)]
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
    pub diagnostics: String,
}

enum OperationKind {
    Generate,
    Inspect,
    Update,
}

pub struct Operation {
    format: Format,
    kind: OperationKind,
    model: String,
    source: Vec<u8>,
    assets: forge_pptx::Assets,
    document_id: Uuid,
    revision: u64,
    cancelled: Arc<AtomicBool>,
    font_options: FontOptions,
}

#[derive(serde::Deserialize)]
#[serde(deny_unknown_fields)]
struct FontOptions {
    system: bool,
    ids: Vec<String>,
}
impl Operation {
    fn fonts(&self) -> forge_tree_doc::Result<forge_document::fonts::Fonts> {
        let bytes = self
            .font_options
            .ids
            .iter()
            .map(|id| {
                self.assets.get(id).cloned().ok_or_else(|| {
                    Diagnostic::new(
                        ErrorCode::InvalidReference,
                        "fonts",
                        "Font asset is not registered",
                    )
                })
            })
            .collect::<forge_tree_doc::Result<Vec<_>>>()?;
        forge_document::fonts::Fonts::new(self.font_options.system, &bytes)
    }
}

impl Task for Operation {
    type JsValue = NativeOutput;
    type Output = (Vec<u8>, String, String, String);

    fn compute(&mut self) -> napi::Result<Self::Output> {
        if self.cancelled.load(Ordering::Acquire) {
            return Err(cancelled());
        }
        let collector = diagnostics::Collector::default();
        let format = match self.format {
            Format::Pptx => "pptx",
            Format::Docx => "docx",
            Format::Xlsx => "xlsx",
            Format::Pdf => "pdf",
            Format::Glb => "glb",
            Format::Fbx => "fbx",
            Format::Sprite => "sprite",
            Format::Wav => "wav",
        };
        let operation = match self.kind {
            OperationKind::Generate => "generate",
            OperationKind::Inspect => "inspect",
            OperationKind::Update => "update",
        };
        let stage = if matches!(self.kind, OperationKind::Inspect)
            && matches!(self.format, Format::Glb | Format::Fbx)
        {
            "layout"
        } else if matches!(self.kind, OperationKind::Inspect) {
            "import"
        } else {
            "export"
        };
        let started = std::time::Instant::now();
        let result=collector.scoped(|| {
            tracing::info!(target:"react_forge",format,operation,stage,revision=self.revision,status="started",duration_ms=0_u64);
            let mut result = forge_tree_doc::cancellation::with_cancellation(self.cancelled.clone(), || match self.format {
                Format::Pptx => pptx::process(self), Format::Docx => docx::process(self),
                Format::Xlsx => xlsx::process(self), Format::Pdf => pdf::process(self),
                Format::Glb | Format::Fbx => scene::process(self),
                Format::Wav => sfx::process(self),
                Format::Sprite => sprite::process(self),
            });
            if self.cancelled.load(Ordering::Acquire){result=Err(Diagnostic::new(ErrorCode::Cancelled,"","The native operation was cancelled"));}
            let code=result.as_ref().err().and_then(|e|serde_json::to_value(e.code).ok()).and_then(|v|v.as_str().map(str::to_owned)).unwrap_or_default();
            let status=if result.is_ok(){"completed"}else{"failed"};
            tracing::info!(target:"react_forge",format,operation,stage,revision=self.revision,status,code=code.as_str(),duration_ms=started.elapsed().as_millis() as u64);
            result
        });
        let events = collector.json();
        match result {
            Ok((bytes, model, geometry)) => Ok((bytes, model, geometry, events)),
            Err(error) => {
                let mut value = serde_json::to_value(error).unwrap_or_default();
                value["native_events"] = serde_json::from_str(&events).unwrap_or_default();
                Err(napi::Error::from_reason(value.to_string()))
            }
        }
    }

    fn resolve(
        &mut self,
        _env: Env,
        (bytes, model, geometry, diagnostics): Self::Output,
    ) -> napi::Result<NativeOutput> {
        if self.cancelled.load(Ordering::Acquire) {
            return Err(cancelled());
        }
        Ok(NativeOutput {
            bytes: bytes.into(),
            model,
            geometry,
            diagnostics,
        })
    }
}

// This private N-API boundary keeps Buffer ownership and cancellation explicit;
// workers receive the normalized Operation, never a live JavaScript object.
#[allow(clippy::too_many_arguments)]
#[napi]
pub fn process_document(
    format: String,
    operation: String,
    model: String,
    source: Buffer,
    assets: Vec<NativeAsset>,
    document_id: String,
    revision: u32,
    cancellation: &Cancellation,
    font_options: String,
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
    let format = match format.as_str() {
        "pptx" => Format::Pptx,
        "docx" => Format::Docx,
        "xlsx" => Format::Xlsx,
        "pdf" => Format::Pdf,
        "glb" => Format::Glb,
        "fbx" => Format::Fbx,
        "sprite" => Format::Sprite,
        "wav" => Format::Wav,
        _ => {
            return Err(native_error(Diagnostic::new(
                ErrorCode::UnsupportedPackage,
                "",
                "Unsupported document format",
            )));
        }
    };
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
    let font_options = forge_tree_doc::parse(font_options.as_bytes()).map_err(native_error)?;
    Ok(AsyncTask::new(Operation {
        font_options,
        format,
        kind,
        model,
        source: source.to_vec(),
        assets: loaded,
        document_id,
        revision: revision.into(),
        cancelled: cancellation.flag.clone(),
    }))
}

/// Pure planning stays on a worker; no credentials or JS callbacks cross it.
pub struct FigmaPlanOperation {
    input: String,
    cancelled: Arc<AtomicBool>,
}
impl Task for FigmaPlanOperation {
    type JsValue = String;
    type Output = String;

    fn compute(&mut self) -> napi::Result<String> {
        if self.cancelled.load(Ordering::Acquire) {
            return Err(cancelled());
        }
        let result = forge_figma::plan_json(&self.input).map_err(|code| {
            napi::Error::from_reason(serde_json::to_string(&code).unwrap_or_default())
        })?;
        if self.cancelled.load(Ordering::Acquire) {
            return Err(cancelled());
        }
        Ok(result)
    }

    fn resolve(&mut self, _env: Env, output: String) -> napi::Result<String> {
        Ok(output)
    }
}
#[napi]
pub fn plan_figma(
    input: String,
    cancellation: &Cancellation,
) -> napi::Result<AsyncTask<FigmaPlanOperation>> {
    if input.len() > forge_figma::MAX_MODEL_BYTES {
        return Err(napi::Error::from_reason("resource_limit"));
    }
    Ok(AsyncTask::new(FigmaPlanOperation {
        input,
        cancelled: cancellation.flag.clone(),
    }))
}

pub struct FigmaImageOperation {
    bytes: Vec<u8>,
    cancelled: Arc<AtomicBool>,
}
impl Task for FigmaImageOperation {
    type JsValue = bool;
    type Output = bool;

    fn compute(&mut self) -> napi::Result<bool> {
        if self.cancelled.load(Ordering::Acquire) {
            return Err(cancelled());
        }
        forge_document::image(&self.bytes).map_err(native_error)?;
        if self.cancelled.load(Ordering::Acquire) {
            return Err(cancelled());
        }
        Ok(true)
    }

    fn resolve(&mut self, _env: Env, output: bool) -> napi::Result<bool> {
        Ok(output)
    }
}
#[napi]
pub fn validate_figma_image(
    bytes: Buffer,
    cancellation: &Cancellation,
) -> napi::Result<AsyncTask<FigmaImageOperation>> {
    if bytes.len() > 10 * 1024 * 1024 {
        return Err(napi::Error::from_reason("resource_limit"));
    }
    Ok(AsyncTask::new(FigmaImageOperation {
        bytes: bytes.to_vec(),
        cancelled: cancellation.flag.clone(),
    }))
}
