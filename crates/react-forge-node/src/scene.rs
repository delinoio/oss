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
    let bytes = if matches!(op.kind, OperationKind::Inspect) {
        vec![]
    } else {
        match op.format {
            Format::Glb => forge_glb::export(&prepared)?,
            Format::Fbx => forge_fbx::export(&prepared)?,
            _ => unreachable!(),
        }
    };
    let bounds =
        serde_json::to_string(&prepared.bounds).map_err(|_| forge_scene::invalid("bounds"))?;
    Ok((bytes, String::new(), bounds))
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
