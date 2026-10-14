//! Compare source and compacted deformation through the independent ufbx
//! reader.
use forge_scene::{Assets, Scene};
use serde_json::json;

fn source_geometry() -> Vec<u8> {
    let positions: [[f32; 3]; 6] = [
        [0., 0., 0.],
        [1., 0., 0.],
        [0., 1., 0.],
        [0., 0., 0.],
        [5., 5., 5.],
        [6., 6., 6.],
    ];
    let mut base = b"FSG1".to_vec();
    for value in [0_u32, 6, 6] {
        base.extend_from_slice(&value.to_le_bytes());
    }
    for row in positions {
        for value in row {
            base.extend_from_slice(&value.to_le_bytes());
        }
    }
    for _ in 0..6 {
        for value in [0_f32, 0., 1.] {
            base.extend_from_slice(&value.to_le_bytes());
        }
    }
    for value in [0_u32, 1, 2, 3, 1, 2] {
        base.extend_from_slice(&value.to_le_bytes());
    }
    let mut bytes = b"FSG2".to_vec();
    for value in [base.len() as u32, 1, 1] {
        bytes.extend_from_slice(&value.to_le_bytes());
    }
    bytes.extend(base);
    for _ in 0..6 {
        for joint in [0_u16; 4] {
            bytes.extend_from_slice(&joint.to_le_bytes());
        }
    }
    for _ in 0..6 {
        for weight in [1_f32, 0., 0., 0.] {
            bytes.extend_from_slice(&weight.to_le_bytes());
        }
    }
    bytes.extend_from_slice(&4_u32.to_le_bytes());
    bytes.extend_from_slice(&1_u32.to_le_bytes());
    bytes.extend_from_slice(b"rise");
    for vertex in 0..6 {
        for value in [0_f32, 0., if vertex == 0 || vertex == 3 { 1. } else { 0. }] {
            bytes.extend_from_slice(&value.to_le_bytes());
        }
    }
    for _ in 0..18 {
        bytes.extend_from_slice(&0_f32.to_le_bytes());
    }
    bytes
}

fn sampler(path: u32, width: u32, values: &[f32]) -> Vec<u8> {
    let mut bytes = b"FSA1".to_vec();
    for value in [path, 1, width, 2] {
        bytes.extend_from_slice(&value.to_le_bytes());
    }
    for value in [0_f32, 1.].into_iter().chain(values.iter().copied()) {
        bytes.extend_from_slice(&value.to_le_bytes());
    }
    bytes
}

fn export(bytes: Vec<u8>) -> Vec<u8> {
    let scene: Scene = serde_json::from_value(json!({
        "document_id":"01956e48-8f55-7000-8000-000000000001",
        "nodes":[{"id":"01956e48-8f55-7000-8000-000000000002","type":"group","children":[
            {"id":"01956e48-8f55-7000-8000-000000000003","type":"joint"},
            {"id":"01956e48-8f55-7000-8000-000000000004","type":"mesh","geometry":"g",
             "material":{"alpha_mode":"blend","base_color":[1,1,1,0.5]},
             "skin":{"joints":["01956e48-8f55-7000-8000-000000000003"]}}
        ]}],
        "animations":[{"id":"01956e48-8f55-7000-8000-000000000005","name":"deform","tracks":[
            {"target":"01956e48-8f55-7000-8000-000000000003","sampler":"move"},
            {"target":"01956e48-8f55-7000-8000-000000000004","sampler":"rise"}
        ]}]
    }))
    .unwrap();
    let assets = Assets::from([
        ("g".into(), bytes),
        ("move".into(), sampler(0, 3, &[0., 0., 0., 0., 2., 0.])),
        ("rise".into(), sampler(3, 1, &[0., 1.])),
    ]);
    forge_fbx::export(&forge_scene::prepare(&scene, &assets).unwrap()).unwrap()
}

#[test]
fn independent_fbx_evaluation_preserves_each_deformed_face_corner() {
    let source = source_geometry();
    let compact = forge_scene::compact_geometry(&source).unwrap();
    assert_eq!(
        (
            compact.input_vertices,
            compact.output_vertices,
            compact.triangles
        ),
        (6, 3, 2)
    );
    let original = ufbx::load_memory(&export(source), Default::default()).unwrap();
    let derived = ufbx::load_memory(&export(compact.bytes), Default::default()).unwrap();
    for scene in [&original, &derived] {
        assert_eq!(scene.meshes[0].num_triangles, 2);
        assert_eq!(scene.anim_stacks[0].element.name.as_ref(), "deform");
        assert_eq!(scene.blend_shapes.len(), 1);
        assert_eq!(scene.bones.len(), 1);
    }
    for time in [0., 0.5, 1.] {
        let evaluate = |scene: &ufbx::Scene| {
            ufbx::evaluate_scene(
                scene,
                &scene.anim_stacks[0].anim,
                time,
                ufbx::EvaluateOpts {
                    evaluate_skinning: true,
                    ..Default::default()
                },
            )
            .unwrap()
        };
        let before = evaluate(&original);
        let after = evaluate(&derived);
        let left = &before.meshes[0];
        let right = &after.meshes[0];
        assert_eq!(left.num_indices, right.num_indices);
        assert_eq!(left.num_indices, 6);
        for index in 0..left.num_indices {
            for (a, b) in [
                (left.skinned_position[index], right.skinned_position[index]),
                (left.skinned_normal[index], right.skinned_normal[index]),
            ] {
                for difference in [(a.x - b.x).abs(), (a.y - b.y).abs(), (a.z - b.z).abs()] {
                    assert!(
                        difference < 0.002,
                        "time={time} corner={index} error={difference}"
                    );
                }
            }
        }
    }
}
