//! Independent, local-only topology inspection for the scene benchmark.
use std::fs;

fn main() {
    let mut reports = Vec::new();
    for path in std::env::args().skip(1) {
        let bytes = fs::read(&path).expect("read benchmark FBX");
        let scene = ufbx::load_memory(
            &bytes,
            ufbx::LoadOpts {
                load_external_files: false,
                ..Default::default()
            },
        )
        .expect("independent FBX parse");
        let meshes = scene
            .meshes
            .iter()
            .filter(|mesh| !mesh.element.instances.is_empty())
            .collect::<Vec<_>>();
        assert!(
            meshes
                .iter()
                .all(|mesh| mesh.vertex_normal.exists && mesh.vertex_uv.exists)
        );
        reports.push(serde_json::json!({
            "file": path,
            "geometries": meshes.len(),
            "meshes": meshes.iter().map(|mesh| mesh.element.instances.len()).sum::<usize>(),
            "uniqueVertexCount": meshes.iter().map(|mesh| mesh.vertices.len()).sum::<usize>(),
            "uniqueTriangleCount": meshes.iter().map(|mesh| mesh.num_triangles).sum::<usize>(),
            "instanceVertexCount": meshes.iter().map(|mesh| mesh.vertices.len() * mesh.element.instances.len()).sum::<usize>(),
            "instanceTriangleCount": meshes.iter().map(|mesh| mesh.num_triangles * mesh.element.instances.len()).sum::<usize>(),
        }));
    }
    println!("{}", serde_json::to_string(&reports).unwrap());
}
