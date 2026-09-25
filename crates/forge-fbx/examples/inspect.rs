//! Independent test-only inspection; this executable is not distributed.
use std::{fs, path::Path};
fn main() {
    let mut report = vec![];
    for path in std::env::args().skip(1) {
        let bytes = fs::read(&path).expect("read explicit fixture");
        let scene = ufbx::load_memory(
            &bytes,
            ufbx::LoadOpts {
                load_external_files: false,
                ..Default::default()
            },
        )
        .expect("independent FBX parse");
        assert!((scene.settings.unit_meters - 1.0).abs() < 1e-9);
        assert!(
            scene
                .meshes
                .iter()
                .all(|m| m.vertex_normal.exists && m.num_triangles > 0)
        );
        assert!(
            scene
                .textures
                .iter()
                .filter(|t| t.type_ == ufbx::TextureType::File)
                .all(|t| !t.content.is_empty())
        );
        report.push(serde_json::json!({"file":Path::new(&path).file_name().unwrap().to_string_lossy(),"meshes":scene.meshes.len(),"materials":scene.materials.len(),"textures":scene.textures.len(),"triangles":scene.meshes.iter().map(|m|m.num_triangles).sum::<usize>(),"metersPerUnit":scene.settings.unit_meters}));
    }
    println!("{}", serde_json::to_string_pretty(&report).unwrap());
}
