use forge_tree_doc::{ErrorCode, Result, error};

use crate::{Operation, OperationKind};

pub fn process(op: &Operation) -> Result<(Vec<u8>, String, String)> {
    if !matches!(op.kind, OperationKind::Generate) {
        return error(
            ErrorCode::UnsupportedPackage,
            "",
            "WAV import is not supported",
        );
    }
    let sound: forge_sfx::Sound = forge_tree_doc::parse(op.model.as_bytes())?;
    let bytes = forge_sfx::generate(&sound)?;
    let mut nodes = serde_json::Map::new();
    let rate = f64::from(sound.sample_rate);
    let duration = (sound.duration * rate).round() / rate;
    let frame = |start: f64, duration: f64| {
        serde_json::json!({"frame": {
            "coordinate_space":"timeline", "x":start, "y":0, "width":duration, "height":1
        }})
    };
    nodes.insert(sound.id.to_string(), frame(0.0, duration));
    for layer in &sound.layers {
        let start = (layer.start * rate).round() / rate;
        let length = ((layer.duration * rate).round() / rate).min(duration - start);
        nodes.insert(layer.id.to_string(), frame(start, length));
    }
    Ok((
        bytes,
        op.model.clone(),
        serde_json::json!({"nodes":nodes}).to_string(),
    ))
}
