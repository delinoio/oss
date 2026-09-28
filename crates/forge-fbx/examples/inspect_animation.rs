//! Local acceptance of the exported character through the independent ufbx
//! evaluator.
use std::{fs, path::PathBuf};

fn main() {
    let directory = PathBuf::from(
        std::env::args()
            .nth(1)
            .expect("explicit evidence directory"),
    );
    let reference: serde_json::Value =
        serde_json::from_slice(&fs::read(directory.join("animation-reference.json")).unwrap())
            .unwrap();
    let file = reference["files"]
        .as_array()
        .unwrap()
        .iter()
        .find(|file| file["format"] == "fbx")
        .unwrap();
    let scene = ufbx::load_memory(
        &fs::read(directory.join(file["filename"].as_str().unwrap())).unwrap(),
        ufbx::LoadOpts {
            load_external_files: false,
            ..Default::default()
        },
    )
    .expect("independent FBX import");
    assert_eq!(scene.anim_stacks.len(), 3);
    assert_eq!(scene.bones.len(), 7);
    assert_eq!(scene.meshes.len(), 3);
    assert_eq!(scene.blend_shapes.len(), 1);
    let mut clips = vec![];
    for clip in file["clips"].as_array().unwrap() {
        let name = clip["name"].as_str().unwrap();
        let stack = scene
            .anim_stacks
            .iter()
            .find(|stack| stack.element.name.as_ref() == name)
            .unwrap();
        let mut samples = vec![];
        for sample in clip["samples"].as_array().unwrap() {
            let time = sample["time"].as_f64().unwrap();
            let evaluated = ufbx::evaluate_scene(
                &scene,
                &stack.anim,
                time,
                ufbx::EvaluateOpts {
                    evaluate_skinning: true,
                    ..Default::default()
                },
            )
            .unwrap();
            let mut min = [f64::INFINITY; 3];
            let mut max = [f64::NEG_INFINITY; 3];
            for mesh in evaluated.meshes.iter() {
                assert!(!mesh.skinned_is_local);
                for v in mesh.skinned_position.values.iter() {
                    for (i, value) in [v.x, v.y, v.z].into_iter().enumerate() {
                        min[i] = min[i].min(value);
                        max[i] = max[i].max(value);
                    }
                }
            }
            let mut error = 0f64;
            for (key, actual) in [("min", min), ("max", max)] {
                for (i, value) in actual.into_iter().enumerate() {
                    error = error.max((value - sample[key][i].as_f64().unwrap()).abs());
                }
            }
            assert!(error < 0.002, "{name} at {time}: {error} meters");
            samples.push(serde_json::json!({"time":time,"boundsErrorMeters":error}));
        }
        clips.push(serde_json::json!({"name":name,"samples":samples}));
    }
    println!(
        "{}",
        serde_json::to_string_pretty(&serde_json::json!({
            "ufbxRust":"0.11.4", "meshes":3, "joints":7, "morphTargets":1, "clips":clips
        }))
        .unwrap()
    );
}
