use super::*;
pub fn process(op: &Operation) -> forge_tree_doc::Result<(Vec<u8>, String, String)> {
    if matches!(op.kind, OperationKind::Update) || !op.source.is_empty() {
        return Err(forge_scene::invalid("scene"));
    }
    let scene: forge_scene::Scene = forge_tree_doc::parse(op.model.as_bytes())?;
    if scene.document_id != op.document_id {
        return Err(forge_scene::invalid("document_id"));
    }
    let prepared = forge_scene::prepare(&scene, &op.assets)?;
    let bytes = if matches!(op.kind, OperationKind::Inspect | OperationKind::Analyze) {
        vec![]
    } else {
        match op.format {
            Format::Glb => forge_glb::export(&prepared)?,
            Format::Fbx => forge_fbx::export(&prepared)?,
            _ => unreachable!(),
        }
    };
    let geometry = if matches!(op.kind, OperationKind::Analyze) {
        serde_json::to_string(&forge_scene::analyze(&prepared)?)
            .map_err(|_| forge_scene::invalid("scene"))?
    } else {
        serde_json::to_string(&prepared.bounds).map_err(|_| forge_scene::invalid("bounds"))?
    };
    Ok((bytes, String::new(), geometry))
}
pub struct SceneAssetOperation {
    kind: String,
    bytes: Vec<u8>,
    cancelled: Arc<AtomicBool>,
}
impl Task for SceneAssetOperation {
    type JsValue = bool;
    type Output = bool;

    fn compute(&mut self) -> napi::Result<bool> {
        forge_tree_doc::cancellation::with_cancellation(self.cancelled.clone(), || {
            forge_tree_doc::cancellation::checkpoint()?;
            if self.kind == "geometry" {
                forge_scene::geometry(&self.bytes)?;
            } else if self.kind == "animation_sampler" {
                forge_scene::animation_sampler(&self.bytes)?;
            } else {
                forge_document::image(&self.bytes)?;
            }
            forge_tree_doc::cancellation::checkpoint()?;
            Ok(true)
        })
        .map_err(native_error)
    }

    fn resolve(&mut self, _env: Env, output: bool) -> napi::Result<bool> {
        if self.cancelled.load(Ordering::Acquire) {
            Err(cancelled())
        } else {
            Ok(output)
        }
    }
}
#[napi]
pub fn validate_scene_asset(
    kind: String,
    bytes: Buffer,
    cancellation: &Cancellation,
) -> napi::Result<AsyncTask<SceneAssetOperation>> {
    if !matches!(kind.as_str(), "geometry" | "texture" | "animation_sampler") {
        return Err(native_error(forge_scene::invalid("asset")));
    }
    if bytes.len() > forge_scene::MAX_GEOMETRY_BYTES {
        return Err(native_error(forge_scene::limited()));
    }
    Ok(AsyncTask::new(SceneAssetOperation {
        kind,
        bytes: bytes.to_vec(),
        cancelled: cancellation.flag.clone(),
    }))
}

#[napi(object)]
pub struct NativeCompactedGeometry {
    pub bytes: Buffer,
    pub input_vertices: u32,
    pub output_vertices: u32,
    pub triangles: u32,
}

pub struct SceneCompactionOperation {
    bytes: Vec<u8>,
    cancelled: Arc<AtomicBool>,
}

impl Task for SceneCompactionOperation {
    type JsValue = NativeCompactedGeometry;
    type Output = forge_scene::CompactedGeometry;

    fn compute(&mut self) -> napi::Result<Self::Output> {
        forge_tree_doc::cancellation::with_cancellation(self.cancelled.clone(), || {
            forge_tree_doc::cancellation::checkpoint()?;
            forge_scene::compact_geometry(&self.bytes)
        })
        .map_err(native_error)
    }

    fn resolve(&mut self, _env: Env, output: Self::Output) -> napi::Result<Self::JsValue> {
        if self.cancelled.load(Ordering::Acquire) {
            return Err(cancelled());
        }
        Ok(NativeCompactedGeometry {
            bytes: output.bytes.into(),
            input_vertices: output.input_vertices,
            output_vertices: output.output_vertices,
            triangles: output.triangles,
        })
    }
}

#[napi]
pub fn compact_scene_geometry(
    bytes: Buffer,
    cancellation: &Cancellation,
) -> napi::Result<AsyncTask<SceneCompactionOperation>> {
    forge_scene::compaction_scratch_bytes(&bytes).map_err(native_error)?;
    Ok(AsyncTask::new(SceneCompactionOperation {
        bytes: bytes.to_vec(),
        cancelled: cancellation.flag.clone(),
    }))
}
