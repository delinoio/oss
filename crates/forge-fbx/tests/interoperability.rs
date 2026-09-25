use forge_scene::{Assets, Scene};
use serde_json::json;
fn fixture() -> (Scene, Assets) {
    let doc = "01956e48-8f55-7000-8000-000000000001";
    let root = "01956e48-8f55-7000-8000-000000000002";
    let mesh = "01956e48-8f55-7000-8000-000000000003";
    let scene=serde_json::from_value(json!({"document_id":doc,"nodes":[{"id":root,"type":"group","children":[{"id":mesh,"name":"Triangle","type":"mesh","geometry":"g","material":{"metallic":0.7,"roughness":0.3}}]}]})).unwrap();
    let mut b = b"FSG1".to_vec();
    for n in [0u32, 3, 3] {
        b.extend_from_slice(&n.to_le_bytes());
    }
    for x in [
        0f32, 0., 0., 1., 0., 0., 0., 1., 0., 0., 0., 1., 0., 0., 1., 0., 0., 1.,
    ] {
        b.extend_from_slice(&x.to_le_bytes());
    }
    for n in [0u32, 1, 2] {
        b.extend_from_slice(&n.to_le_bytes());
    }
    (scene, Assets::from([("g".into(), b)]))
}
#[test]
fn independent_fbx_reader_accepts_geometry_material_and_units() {
    let (s, a) = fixture();
    let p = forge_scene::prepare(&s, &a).unwrap();
    let b = forge_fbx::export(&p).unwrap();
    let imported = ufbx::load_memory(&b, Default::default()).unwrap();
    assert_eq!(imported.meshes.len(), 1);
    assert_eq!(imported.meshes[0].num_triangles, 1);
    assert_eq!(imported.meshes[0].num_vertices, 3);
    assert_eq!(imported.materials.len(), 1);
    assert!((imported.settings.unit_meters - 1.0).abs() < 1e-9);
    assert!(imported.meshes[0].vertex_normal.exists);
}
#[test]
fn malformed_geometry_and_nonfinite_transforms_fail_before_export() {
    let (mut s, mut a) = fixture();
    let b = a.get_mut("g").unwrap();
    let len = b.len();
    b[len - 4..].copy_from_slice(&3u32.to_le_bytes());
    assert!(forge_scene::prepare(&s, &a).is_err());
    let (_, a) = fixture();
    s.nodes[0].scale = [0., 1., 1.];
    assert!(forge_scene::prepare(&s, &a).is_err());
}
