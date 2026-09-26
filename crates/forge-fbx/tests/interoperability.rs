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
    assert_eq!(
        imported.settings.axes.right,
        ufbx::CoordinateAxis::PositiveX
    );
    assert_eq!(imported.settings.axes.up, ufbx::CoordinateAxis::PositiveY);
    assert_eq!(
        imported.settings.axes.front,
        ufbx::CoordinateAxis::PositiveZ
    );
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

fn sampler(
    path: u32,
    mode: u32,
    width: u32,
    times: &[f32],
    values: &[f32],
    tangents: Option<&[f32]>,
) -> Vec<u8> {
    let mut bytes = b"FSA1".to_vec();
    for n in [path, mode, width, times.len() as u32] {
        bytes.extend_from_slice(&n.to_le_bytes());
    }
    for array in [times, values].into_iter().chain(tangents).chain(tangents) {
        for value in array {
            bytes.extend_from_slice(&value.to_le_bytes());
        }
    }
    bytes
}

#[test]
fn fbx_rejects_sub_tick_key_intervals_before_rounding() {
    let ticks_per_second = 46_186_158_000.;
    for mode in 0..=2 {
        for (times, valid) in [
            ([1e-11f32, 2e-11], false), // Distinct rounded ticks, but less than one tick apart.
            ([0., 1e-11], false),       // Both keys round to the same tick.
            ([3e-11, 6e-11], true),     // Fractional tick times are valid with sufficient spacing.
        ] {
            let (mut scene, mut assets) = fixture();
            let id = scene.nodes[0].children[0].id;
            assets.insert(
                "move".into(),
                sampler(
                    0,
                    mode,
                    3,
                    &times,
                    &[0., 0., 0., 1., 0., 0.],
                    (mode == 2).then_some(&[0.; 6]),
                ),
            );
            scene.animations = serde_json::from_value(json!([{
                "id":"01956e48-8f55-7000-8000-000000000006", "name":"move",
                "tracks":[{"target":id,"sampler":"move"}]
            }]))
            .unwrap();
            let prepared = forge_scene::prepare(&scene, &assets).unwrap();
            let result = forge_fbx::export(&prepared);
            if valid {
                let imported = ufbx::load_memory(&result.unwrap(), Default::default()).unwrap();
                let node = imported
                    .nodes
                    .iter()
                    .find(|n| n.element.name.as_ref() == "Triangle")
                    .unwrap();
                for (time, expected) in times.into_iter().zip([0., 1.]) {
                    let rounded = (time as f64 * ticks_per_second).round() / ticks_per_second;
                    let actual =
                        ufbx::evaluate_transform(&imported.anim_stacks[0].anim, node, rounded);
                    assert!((actual.translation.x - expected).abs() < 1e-6);
                }
            } else {
                assert_eq!(
                    result
                        .map(|_| ())
                        .expect_err("sub-tick source intervals must fail")
                        .code,
                    forge_tree_doc::ErrorCode::InvalidField,
                    "mode={mode} times={times:?}"
                );
            }
        }
    }
}

fn animated_fixture() -> (Scene, Assets) {
    let (mut scene, mut assets) = fixture();
    let base = assets.remove("g").unwrap();
    let mut bytes = b"FSG2".to_vec();
    for n in [base.len() as u32, 1, 1] {
        bytes.extend_from_slice(&n.to_le_bytes());
    }
    bytes.extend(base);
    // Each vertex blends the two joints equally, exercising sparse clusters.
    for _ in 0..3 {
        for j in [0u16, 1, 0, 0] {
            bytes.extend_from_slice(&j.to_le_bytes());
        }
    }
    for _ in 0..3 {
        for w in [0.5f32, 0.5, 0., 0.] {
            bytes.extend_from_slice(&w.to_le_bytes());
        }
    }
    for n in [5u32, 1] {
        bytes.extend_from_slice(&n.to_le_bytes());
    }
    bytes.extend(b"smile");
    for value in [0f32, 0., 0., 0., 0., 0., 0., 1., 0.]
        .into_iter()
        .chain([0f32; 9])
    {
        bytes.extend_from_slice(&value.to_le_bytes());
    }
    assets.insert("g".into(), bytes);
    assets.insert(
        "move".into(),
        sampler(0, 1, 3, &[0., 1.], &[1., 0., 0., 3., 0., 0.], None),
    );
    assets.insert("smile".into(), sampler(3, 1, 1, &[0., 1.], &[0., 1.], None));
    let joint = "01956e48-8f55-7000-8000-000000000004";
    let second = "01956e48-8f55-7000-8000-000000000005";
    let clip = "01956e48-8f55-7000-8000-000000000006";
    let mut value = serde_json::to_value(&scene).unwrap();
    let mesh = &mut value["nodes"][0]["children"][0];
    mesh["translation"] = json!([3., 0., 0.]);
    mesh["skin"] = json!({"joints":[joint,second]});
    value["nodes"][0]["children"]
        .as_array_mut()
        .unwrap()
        .extend([
            json!({"id":joint,"name":"Bone","type":"joint","translation":[1.,0.,0.]}),
            json!({"id":second,"name":"Bone2","type":"joint","translation":[2.,0.,0.]}),
        ]);
    value["animations"] = json!([{"id":clip,"name":"move","tracks":[{"target":joint,"sampler":"move"},{"target":"01956e48-8f55-7000-8000-000000000003","sampler":"smile"}]}]);
    scene = serde_json::from_value(value).unwrap();
    (scene, assets)
}

#[test]
fn independent_fbx_evaluation_matches_skin_morph_and_nonidentity_bind_bounds() {
    let (scene, assets) = animated_fixture();
    let prepared = forge_scene::prepare(&scene, &assets).unwrap();
    let imported =
        ufbx::load_memory(&forge_fbx::export(&prepared).unwrap(), Default::default()).unwrap();
    assert_eq!(imported.anim_stacks.len(), 1);
    assert_eq!(imported.skin_deformers.len(), 1);
    assert_eq!(imported.skin_clusters.len(), 2);
    assert_eq!(imported.blend_shapes.len(), 1);
    assert_eq!(imported.poses.len(), 1);
    assert_eq!(imported.bones.len(), 2);
    for time in [0., 0.25, 0.5, 1.] {
        let expected = prepared
            .evaluate(Some(&forge_scene::AnimationSample {
                clip: scene.animations[0].id,
                time,
            }))
            .unwrap();
        let bound = expected[&scene.nodes[0].children[0].id];
        let evaluated = ufbx::evaluate_scene(
            &imported,
            &imported.anim_stacks[0].anim,
            time,
            ufbx::EvaluateOpts {
                evaluate_skinning: true,
                ..Default::default()
            },
        )
        .unwrap();
        let mesh = &evaluated.meshes[0];
        assert!(!mesh.skinned_is_local);
        let mut min = [f64::INFINITY; 3];
        let mut max = [f64::NEG_INFINITY; 3];
        for v in mesh.skinned_position.values.iter() {
            for (i, v) in [v.x, v.y, v.z].into_iter().enumerate() {
                min[i] = min[i].min(v);
                max[i] = max[i].max(v);
            }
        }
        for i in 0..3 {
            assert!(
                (min[i] - bound.min[i]).abs() < 1e-5,
                "time={time} min={min:?} expected={bound:?}"
            );
            assert!(
                (max[i] - bound.max[i]).abs() < 1e-5,
                "time={time} max={max:?} expected={bound:?}"
            );
        }
    }
}

#[test]
fn independent_curves_match_cubic_and_quaternion_baking_across_euler_wrap() {
    let (mut scene, mut assets) = fixture();
    let id = scene.nodes[0].children[0].id;
    let rotation = [
        0.,
        0.,
        (170f32.to_radians() / 2.).sin(),
        (170f32.to_radians() / 2.).cos(),
        0.,
        0.,
        (190f32.to_radians() / 2.).sin(),
        (190f32.to_radians() / 2.).cos(),
    ];
    assets.insert(
        "rotate".into(),
        sampler(1, 1, 4, &[0., 1.], &rotation, None),
    );
    assets.insert(
        "cubic".into(),
        sampler(
            0,
            2,
            3,
            &[0., 1.],
            &[0., 0., 0., 2., 0., 0.],
            Some(&[0.; 6]),
        ),
    );
    scene.animations=serde_json::from_value(json!([{"id":"01956e48-8f55-7000-8000-000000000006","name":"curve","tracks":[{"target":id,"sampler":"rotate"},{"target":id,"sampler":"cubic"}]}])).unwrap();
    let prepared = forge_scene::prepare(&scene, &assets).unwrap();
    let imported =
        ufbx::load_memory(&forge_fbx::export(&prepared).unwrap(), Default::default()).unwrap();
    let node = imported
        .nodes
        .iter()
        .find(|n| n.element.name.as_ref() == "Triangle")
        .unwrap();
    for time in [0., 0.25, 0.5, 0.75, 1.] {
        let actual = ufbx::evaluate_transform(&imported.anim_stacks[0].anim, node, time);
        let expected = prepared
            .pose(Some(&forge_scene::AnimationSample {
                clip: scene.animations[0].id,
                time,
            }))
            .unwrap();
        let pose = &expected[&id];
        assert!((actual.translation.x - pose.translation[0]).abs() < 1e-5);
        let dot = [
            actual.rotation.x,
            actual.rotation.y,
            actual.rotation.z,
            actual.rotation.w,
        ]
        .iter()
        .zip(pose.rotation)
        .map(|(a, b)| a * b)
        .sum::<f64>();
        assert!((dot.abs() - 1.).abs() < 1e-5, "rotation at {time}");
    }
}

#[test]
fn distinct_mesh_instances_keep_independent_deformers_and_bake_expansion_is_bounded() {
    let (mut scene, mut assets) = animated_fixture();
    let mut value = serde_json::to_value(&scene).unwrap();
    let mut duplicate = value["nodes"][0]["children"][0].clone();
    duplicate["id"] = json!("01956e48-8f55-7000-8000-000000000007");
    duplicate["translation"] = json!([9., 0., 0.]);
    duplicate["morph_weights"] = json!([0.75]);
    value["nodes"][0]["children"]
        .as_array_mut()
        .unwrap()
        .push(duplicate);
    scene = serde_json::from_value(value).unwrap();
    let p = forge_scene::prepare(&scene, &assets).unwrap();
    let imported = ufbx::load_memory(&forge_fbx::export(&p).unwrap(), Default::default()).unwrap();
    assert_eq!(imported.meshes.len(), 2);
    assert_eq!(imported.skin_deformers.len(), 2);
    assert_eq!(imported.blend_channels.len(), 2);
    assert!((imported.blend_channels[1].weight - 0.75).abs() < 1e-6);
    assets.insert(
        "move".into(),
        sampler(
            0,
            2,
            3,
            &[0., 100_000.],
            &[1., 0., 0., 3., 0., 0.],
            Some(&[0.; 6]),
        ),
    );
    let p = forge_scene::prepare(&scene, &assets).unwrap();
    assert_eq!(
        forge_fbx::export(&p).unwrap_err().code,
        forge_tree_doc::ErrorCode::ResourceLimit
    );
}

#[test]
fn animated_camera_and_light_rotations_keep_the_export_basis() {
    use glam::DQuat;
    let (mut scene, mut assets) = fixture();
    let id = scene.nodes[0].children[0].id;
    let end = DQuat::from_euler(glam::EulerRot::XYZEx, 0.4, 1.2, -0.3);
    let values: Vec<f32> = DQuat::IDENTITY
        .to_array()
        .into_iter()
        .chain(end.to_array())
        .map(|v| v as f32)
        .collect();
    assets.insert(
        "rotation".into(),
        sampler(1, 1, 4, &[0., 1.], &values, None),
    );
    scene.animations = serde_json::from_value(json!([{
        "id":"01956e48-8f55-7000-8000-000000000006", "name":"rotate",
        "tracks":[{"target":id,"sampler":"rotation"}]
    }]))
    .unwrap();
    for (kind, basis) in [
        (
            forge_scene::Kind::PerspectiveCamera {
                yfov: 0.6,
                aspect: 1.5,
                near: 0.1,
                far: 100.,
            },
            DQuat::from_rotation_y(std::f64::consts::FRAC_PI_2),
        ),
        (
            forge_scene::Kind::SpotLight {
                color: [1.; 3],
                intensity: 100.,
                inner_cone: 0.1,
                outer_cone: 0.3,
            },
            DQuat::from_rotation_x(std::f64::consts::FRAC_PI_2),
        ),
    ] {
        scene.nodes[0].children[0].kind = kind;
        let p = forge_scene::prepare(&scene, &assets).unwrap();
        let imported =
            ufbx::load_memory(&forge_fbx::export(&p).unwrap(), Default::default()).unwrap();
        let node = imported
            .nodes
            .iter()
            .find(|n| n.element.name.as_ref() == "Triangle")
            .unwrap();
        for time in [0., 0.25, 0.5, 0.75, 1.] {
            let actual =
                ufbx::evaluate_transform(&imported.anim_stacks[0].anim, node, time).rotation;
            let actual = DQuat::from_xyzw(actual.x, actual.y, actual.z, actual.w);
            let expected = DQuat::IDENTITY.slerp(end, time) * basis;
            assert!((actual.dot(expected).abs() - 1.).abs() < 1e-8);
        }
    }
}
