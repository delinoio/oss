// SPDX-License-Identifier: Apache-2.0
//! Fixed, opt-in native integration checks. These do not inject OS input.
use std::{fs::File, path::Path};

use anyrender::{PaintScene as _, render_to_buffer};
use anyrender_vello_cpu::VelloCpuImageRenderer;
use blitz_dom::{Document, util::Color};
use blitz_paint::paint_scene;
use blitz_traits::{
    events::{BlitzImeEvent, BlitzKeyEvent, KeyState, UiEvent},
    shell::{ColorScheme, Viewport},
};
use keyboard_types::{Code, Key, Location, Modifiers, NamedKey};
use peniko::{Fill, kurbo::Rect};

use super::TodoDocument;

fn key(doc: &mut TodoDocument, key: Key, composing: bool) {
    doc.handle_ui_event(UiEvent::KeyDown(BlitzKeyEvent {
        code: Code::Unidentified,
        key,
        modifiers: Modifiers::empty(),
        location: Location::Standard,
        is_auto_repeating: false,
        is_composing: composing,
        state: KeyState::Pressed,
        text: None,
    }));
    doc.html.resolve(0.0);
}

fn focus(doc: &mut TodoDocument, selector: &str) {
    let node = doc
        .html
        .query_selector(selector)
        .unwrap()
        .expect("fixed test node");
    doc.html.set_focus_to(node);
}

fn type_text(doc: &mut TodoDocument, text: &str) {
    focus(doc, "#new-todo");
    let input = doc.html.query_selector("#new-todo").unwrap().unwrap();
    doc.html.with_text_input(input, |mut driver| {
        driver.select_all();
        driver.insert_or_replace_selection(text);
    });
}

fn count(doc: &TodoDocument) -> String {
    let node = doc.html.query_selector("#count").unwrap().unwrap();
    let text = doc.html.get_node(node).unwrap().children[0];
    doc.html
        .get_node(text)
        .unwrap()
        .text_data()
        .unwrap()
        .content
        .to_string()
}

fn frame(doc: &mut TodoDocument, output: &Path, width: u32, height: u32) -> Vec<u8> {
    doc.html
        .set_viewport(Viewport::new(width, height, 1.0, ColorScheme::Light));
    doc.html.resolve(0.0);
    let pixels = render_to_buffer::<VelloCpuImageRenderer, _>(
        |scene| {
            scene.fill(
                Fill::NonZero,
                Default::default(),
                Color::WHITE,
                Default::default(),
                &Rect::new(0.0, 0.0, width as f64, height as f64),
            );
            paint_scene(scene, doc.html.as_mut(), 1.0, width, height, 0, 0);
        },
        width,
        height,
    );
    assert!(
        pixels
            .chunks_exact(4)
            .any(|pixel| pixel[..3] != [255, 255, 255])
    );
    let mut encoder = png::Encoder::new(File::create(output).unwrap(), width, height);
    encoder.set_color(png::ColorType::Rgba);
    encoder.set_depth(png::BitDepth::Eight);
    encoder
        .write_header()
        .unwrap()
        .write_image_data(&pixels)
        .unwrap();
    pixels
}

pub(super) fn run(doc: &mut TodoDocument, output: &Path) {
    assert!(output.is_dir(), "caller-owned artifact directory");
    let input = doc.html.query_selector("#new-todo").unwrap().unwrap();
    assert_eq!(doc.html.get_focussed_node_id(), Some(input));
    assert_eq!(count(doc), "2 remaining");
    let initial = frame(doc, &output.join("initial.png"), 640, 560);
    type_text(doc, "   ");
    key(doc, Key::Named(NamedKey::Enter), false);
    assert_eq!(count(doc), "2 remaining");
    type_text(doc, "Native 한글 😀 <b> & \"quoted\"");
    key(doc, Key::Named(NamedKey::Enter), true);
    assert_eq!(count(doc), "2 remaining", "composition must not submit");
    doc.handle_ui_event(UiEvent::Ime(BlitzImeEvent::Preedit(
        "조합".into(),
        Some((0, 6)),
    )));
    key(doc, Key::Named(NamedKey::Enter), false);
    assert_eq!(
        count(doc),
        "2 remaining",
        "preedit must mark shell key events as composing"
    );
    doc.handle_ui_event(UiEvent::Ime(BlitzImeEvent::Preedit(String::new(), None)));
    key(doc, Key::Named(NamedKey::Enter), false);
    assert_eq!(count(doc), "3 remaining");
    assert_eq!(doc.html.query_selector("#new-todo").unwrap(), Some(input));
    assert!(
        doc.html.query_selector("#list b").unwrap().is_none(),
        "text must not become markup"
    );
    let input_text = doc
        .html
        .get_node(input)
        .unwrap()
        .element_data()
        .unwrap()
        .text_input_data()
        .unwrap()
        .editor
        .text()
        .to_string();
    assert_eq!(input_text, "");
    let added = frame(doc, &output.join("added.png"), 640, 560);
    assert_ne!(initial, added);
    focus(doc, "[data-action='100']");
    key(doc, Key::Character(" ".into()), false);
    assert_eq!(count(doc), "2 remaining");
    for action in [101, 103, 105, 107] {
        focus(doc, &format!("[data-action='{action}']"));
        key(doc, Key::Named(NamedKey::Enter), false);
    }
    assert_eq!(count(doc), "0 remaining");
    assert!(doc.html.query_selector(".empty").unwrap().is_some());
    frame(doc, &output.join("empty.png"), 640, 560);
    type_text(doc, "Add button task");
    focus(doc, "[data-action='1']");
    key(doc, Key::Named(NamedKey::Enter), false);
    assert_eq!(count(doc), "1 remaining");
    frame(doc, &output.join("narrow.png"), 360, 400);
    focus(doc, "#new-todo");
    key(doc, Key::Named(NamedKey::Tab), false);
    let add = doc
        .html
        .query_selector("[data-action='1']")
        .unwrap()
        .unwrap();
    assert_eq!(doc.html.get_focussed_node_id(), Some(add));
    tracing::info!(
        event = "todo_native_self_test_passed",
        scope = "compiled_callbacks_dom_keyboard_offscreen"
    );
}
