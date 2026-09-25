use std::{
    io::Read,
    sync::{Arc, atomic::AtomicBool},
};

use super::*;
fn id() -> Value {
    json!(Uuid::now_v7())
}
fn node(mut content: Value, x: i32, y: i32) -> Value {
    content["id"] = id();
    content["x"] = json!(x);
    content["y"] = json!(y);
    content
}
fn rect(fill: &str, width: u32, height: u32) -> Value {
    node(
        json!({"type":"rect","width":width,"height":height,"fill":fill}),
        0,
        0,
    )
}
fn model(children: Vec<Value>) -> Value {
    json!({"id":id(),"width":2,"height":2,"scale":1,"columns":1,"padding":1,"palette":{"R":"#ff0000","G":"#00ff00"},"animations":[{"id":id(),"name":"idle","loop":true,"frames":[{"id":id(),"duration_ms":100,"pivot":{"x":1.0,"y":2.0},"children":children}]}]})
}
fn project(model: &Value) -> Project {
    serde_json::from_value(model.clone()).unwrap()
}
fn export(model: &Value, assets: &Assets) -> (forge_package::Package, Value) {
    let (bytes, geometry) = generate(&project(model), assets).unwrap();
    let mut zip = zip::ZipArchive::new(Cursor::new(bytes)).unwrap();
    let mut parts = forge_package::Package::new();
    for index in 0..zip.len() {
        let mut file = zip.by_index(index).unwrap();
        let mut bytes = Vec::new();
        file.read_to_end(&mut bytes).unwrap();
        parts.insert(file.name().to_owned(), bytes);
    }
    (parts, geometry)
}
fn png(parts: &forge_package::Package, name: &str) -> RgbaImage {
    image::load_from_memory(&parts[name]).unwrap().into_rgba8()
}
fn metadata(parts: &forge_package::Package) -> Value {
    serde_json::from_slice(&parts["sprite.json"]).unwrap()
}

#[test]
fn pixels_alpha_order_scale_padding_metadata_and_geometry() {
    let layer = node(
        json!({"type":"layer","visible":true,"children":[rect("#ff000080",2,2)]}),
        -1,
        0,
    );
    let ellipse = node(
        json!({"type":"ellipse","width":1,"height":1,"fill":"#00ff00"}),
        1,
        1,
    );
    let grid = node(json!({"type":"pixel_grid","rows":[".R"]}), 0, 1);
    let leaf_id = ellipse["id"].as_str().unwrap().to_owned();
    let mut m = model(vec![rect("#0000ff", 2, 2), layer, ellipse, grid]);
    m["scale"] = json!(2);
    let (parts, geometry) = export(&m, &Assets::new());
    let frame = png(&parts, "frames/0000.png");
    assert_eq!(frame.dimensions(), (4, 4));
    assert_eq!(frame.get_pixel(0, 0).0, [128, 0, 127, 255]);
    assert_eq!(frame.get_pixel(1, 1), frame.get_pixel(0, 0));
    assert_eq!(frame.get_pixel(2, 0).0, [0, 0, 255, 255]);
    assert_eq!(frame.get_pixel(2, 2).0, [255, 0, 0, 255]);
    let sheet = png(&parts, "sheet.png");
    assert_eq!(sheet.dimensions(), (6, 6));
    assert_eq!(sheet.get_pixel(0, 0).0, [0, 0, 0, 0]);
    for y in 0..4 {
        for x in 0..4 {
            assert_eq!(sheet.get_pixel(x + 1, y + 1), frame.get_pixel(x, y));
        }
    }
    let meta = metadata(&parts);
    assert_eq!(meta["frames"][0]["duration"], 100);
    assert_eq!(meta["frames"][0]["pivot"], json!({"x":2.0,"y":4.0}));
    assert_eq!(
        meta["meta"]["frameTags"][0],
        json!({"name":"idle","from":0,"to":0,"direction":"forward"})
    );
    assert_eq!(
        geometry["nodes"][leaf_id]["frame"],
        json!({"coordinate_space":"sprite_frame","x":1,"y":1,"width":1,"height":1,"page":0})
    );
    assert_eq!(
        generate(&project(&m), &Assets::new()).unwrap().0,
        generate(&project(&m), &Assets::new()).unwrap().0
    );
}

#[test]
fn hidden_descendants_retain_translated_geometry_without_drawing() {
    let leaf = rect("#ff0000", 2, 2);
    let leaf_id = leaf["id"].as_str().unwrap().to_owned();
    let nested = node(
        json!({"type":"layer","visible":true,"children":[leaf]}),
        -2,
        3,
    );
    let hidden = node(
        json!({"type":"layer","visible":false,"children":[nested]}),
        1,
        -2,
    );
    let mut m = model(vec![]);
    let mut second = m["animations"][0]["frames"][0].clone();
    second["id"] = id();
    second["children"] = json!([hidden]);
    m["animations"][0]["frames"]
        .as_array_mut()
        .unwrap()
        .push(second);
    let (parts, geometry) = export(&m, &Assets::new());
    assert_eq!(
        geometry["nodes"][leaf_id]["frame"],
        json!({"coordinate_space":"sprite_frame","x":-1,"y":1,"width":2,"height":2,"page":1})
    );
    assert!(
        png(&parts, "frames/0001.png")
            .pixels()
            .all(|pixel| pixel.0 == [0; 4])
    );
}

#[test]
fn image_crop_nearest_flips_and_hidden_crop_validation() {
    let mut original = RgbaImage::new(3, 2);
    for (x, y, p) in original.enumerate_pixels_mut() {
        *p = image::Rgba([x as u8 * 80, y as u8 * 100, 0, 255]);
    }
    let mut asset_parts = forge_package::Package::new();
    add_png(&mut asset_parts, "asset", &original, &mut 0).unwrap();
    let assets = Assets::from([("asset".into(), asset_parts.remove("asset").unwrap())]);
    let content = json!({"type":"image","asset":"asset","width":2,"height":2,"source":{"x":1,"y":0,"width":2,"height":2},"flip_x":true,"flip_y":true});
    let m = model(vec![node(content.clone(), 0, 0)]);
    let (parts, _) = export(&m, &assets);
    let frame = png(&parts, "frames/0000.png");
    assert_eq!(frame.get_pixel(0, 0).0, [160, 100, 0, 255]);
    assert_eq!(frame.get_pixel(1, 1).0, [80, 0, 0, 255]);
    let mut invalid_content = content;
    invalid_content["source"]["x"] = json!(3);
    let hidden = model(vec![node(
        json!({"type":"layer","visible":false,"children":[node(invalid_content,0,0)]}),
        0,
        0,
    )]);
    assert!(generate(&project(&hidden), &assets).is_err());
    assert_eq!(
        generate(&project(&m), &Assets::new()).unwrap_err().code,
        ErrorCode::InvalidReference
    );
    assert!(
        generate(
            &project(&m),
            &Assets::from([("asset".into(), vec![0, 1, 2])])
        )
        .is_err()
    );
}

#[test]
fn frame_order_duration_loops_and_transparency_are_preserved() {
    let mut m = model(vec![rect("#11223380", 1, 1)]);
    let mut next = m["animations"][0].clone();
    next["id"] = id();
    next["name"] = json!("jump");
    next["loop"] = json!(false);
    next["frames"][0]["id"] = id();
    next["frames"][0]["children"] = json!([]);
    next["frames"][0]["duration_ms"] = json!(250);
    m["animations"].as_array_mut().unwrap().push(next);
    m["columns"] = json!(2);
    let (parts, _) = export(&m, &Assets::new());
    assert_eq!(
        png(&parts, "frames/0000.png").get_pixel(0, 0).0,
        [17, 34, 51, 128]
    );
    assert!(
        png(&parts, "frames/0001.png")
            .pixels()
            .all(|p| p.0 == [0, 0, 0, 0])
    );
    let meta = metadata(&parts);
    assert_eq!(meta["frames"][1]["duration"], 250);
    assert_eq!(
        meta["meta"]["reactForge"]["animations"][1],
        json!({"name":"jump","from":1,"to":1,"loop":false})
    );
    assert_eq!(meta["frames"][1]["frame"]["x"], 5);
}

#[test]
fn validation_rejects_ambiguous_and_malformed_models_before_output() {
    for (field, value) in [
        ("width", json!(0)),
        ("width", json!(4097)),
        ("scale", json!(0)),
        ("scale", json!(17)),
        ("padding", json!(65)),
        ("columns", json!(0)),
        ("columns", json!(2)),
    ] {
        let mut m = model(vec![]);
        m[field] = value;
        assert!(generate(&project(&m), &Assets::new()).is_err(), "{field}");
    }
    for rows in [
        json!([]),
        json!([""]),
        json!(["RX"]),
        json!(["RR", "R"]),
        json!(["한"]),
    ] {
        let m = model(vec![node(json!({"type":"pixel_grid","rows":rows}), 0, 0)]);
        assert!(generate(&project(&m), &Assets::new()).is_err());
    }
    for color in ["red", "#12345z", "#abc", "#😀abc"] {
        assert!(generate(&project(&model(vec![rect(color, 1, 1)])), &Assets::new()).is_err());
    }
    let n = rect("#ffffff", 1, 1);
    assert!(generate(&project(&model(vec![n.clone(), n])), &Assets::new()).is_err());
    let mut m = model(vec![]);
    m["animations"][0]["frames"][0]["duration_ms"] = json!(0);
    assert!(generate(&project(&m), &Assets::new()).is_err());
    m["animations"][0]["frames"][0]["duration_ms"] = json!(60_001);
    assert!(generate(&project(&m), &Assets::new()).is_err());
    m["width"] = json!(1.5);
    assert!(serde_json::from_value::<Project>(m).is_err());
    let mut m = model(vec![]);
    m["typo"] = json!(true);
    assert!(serde_json::from_value::<Project>(m).is_err());
    let m = model(vec![node(
        json!({"type":"rect","width":1,"height":1,"fill":"#ffffff","typo":1}),
        0,
        0,
    )]);
    assert!(serde_json::from_value::<Project>(m).is_err());
}

#[test]
fn accepted_limits_and_excess_are_checked_before_allocation() {
    let mut m = model(vec![]);
    m["width"] = json!(4096);
    m["height"] = json!(1);
    m["scale"] = json!(16);
    m["padding"] = json!(64);
    m["animations"][0]["frames"][0]["duration_ms"] = json!(60_000);
    m["animations"][0]["frames"][0]["pivot"] = Value::Null;
    assert!(prepare(&project(&m), &Assets::new()).is_ok());
    m["width"] = json!(4000);
    m["height"] = json!(4000);
    m["scale"] = json!(1);
    m["padding"] = json!(0);
    m["columns"] = json!(2);
    let frames = m["animations"][0]["frames"].as_array_mut().unwrap();
    for _ in 1..4 {
        let mut f = frames[0].clone();
        f["id"] = id();
        frames.push(f);
    }
    assert!(prepare(&project(&m), &Assets::new()).is_ok()); // Exactly 64 million pixels.
    m["width"] = json!(4001);
    assert_eq!(
        prepare(&project(&m), &Assets::new()).err().unwrap().code,
        ErrorCode::ResourceLimit
    );
    m["width"] = json!(1);
    m["height"] = json!(1);
    let frames = m["animations"][0]["frames"].as_array_mut().unwrap();
    while frames.len() < MAX_FRAMES {
        let mut f = frames[0].clone();
        f["id"] = id();
        frames.push(f);
    }
    assert!(prepare(&project(&m), &Assets::new()).is_ok());
    let mut f = m["animations"][0]["frames"][0].clone();
    f["id"] = id();
    m["animations"][0]["frames"].as_array_mut().unwrap().push(f);
    assert_eq!(
        prepare(&project(&m), &Assets::new()).err().unwrap().code,
        ErrorCode::ResourceLimit
    );
}

#[test]
fn cancelled_work_does_not_poison_the_next_export() {
    let p = project(&model(vec![rect("#ffffff", 1, 1)]));
    let result =
        forge_tree_doc::cancellation::with_cancellation(Arc::new(AtomicBool::new(true)), || {
            generate(&p, &Assets::new())
        });
    assert_eq!(result.unwrap_err().code, ErrorCode::Cancelled);
    assert!(generate(&p, &Assets::new()).is_ok());
}

#[test]
fn deepest_empty_layer_and_large_image_crop_remain_valid() {
    let mut drawing = node(json!({"type":"layer","visible":true,"children":[]}), 0, 0);
    for _ in 0..44 {
        drawing = node(
            json!({"type":"layer","visible":true,"children":[drawing]}),
            0,
            0,
        );
    }
    let m = model(vec![drawing]);
    let parsed: Project = forge_tree_doc::parse(&serde_json::to_vec(&m).unwrap()).unwrap();
    assert!(generate(&parsed, &Assets::new()).is_ok());
    let original = RgbaImage::from_pixel(4097, 1, image::Rgba([10, 20, 30, 255]));
    let mut assets = Assets::new();
    add_png(&mut assets, "wide", &original, &mut 0).unwrap();
    let m = model(vec![node(
        json!({"type":"image","asset":"wide","width":2,"height":1,"source":{"x":0,"y":0,"width":4097,"height":1},"flip_x":false,"flip_y":false}),
        0,
        0,
    )]);
    let (parts, _) = export(&m, &assets);
    assert_eq!(
        png(&parts, "frames/0000.png").get_pixel(1, 0).0,
        [10, 20, 30, 255]
    );
}
