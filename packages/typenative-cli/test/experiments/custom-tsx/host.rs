// SPDX-License-Identifier: Apache-2.0
use std::{cell::RefCell, fs::File, sync::Arc};

use anyrender::{PaintScene as _, render_to_buffer};
use anyrender_vello_cpu::VelloCpuImageRenderer;
use blitz_dom::{DocumentConfig, util::Color};
use blitz_html::{HtmlDocument, HtmlProvider};
use blitz_paint::paint_scene;
use blitz_traits::shell::{ColorScheme, Viewport};
use peniko::{Fill, kurbo::Rect};

struct State {
    document: HtmlDocument,
    first_pixels: Option<Vec<u8>>,
}
thread_local! {
    static STATE: RefCell<Option<State>> = const { RefCell::new(None) };
}

// All FFI strings are borrowed only for the call. ScriptC supplies UTF-8 spans.
unsafe fn text(bytes: *const u8, len: usize) -> String {
    assert!(!bytes.is_null() || len == 0);
    let bytes = if len == 0 {
        &[]
    } else {
        unsafe { std::slice::from_raw_parts(bytes, len) }
    };
    std::str::from_utf8(bytes)
        .expect("valid fixture UTF-8")
        .to_owned()
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn tn_init(bytes: *const u8, len: usize) {
    let html = unsafe { text(bytes, len) };
    let document = HtmlDocument::from_html(
        &html,
        DocumentConfig {
            html_parser_provider: Some(Arc::new(HtmlProvider)),
            viewport: Some(Viewport::new(640, 360, 1.0, ColorScheme::Light)),
            ..Default::default()
        },
    );
    STATE.with_borrow_mut(|state| {
        assert!(state.is_none(), "document must only be initialized once");
        *state = Some(State {
            document,
            first_pixels: None,
        });
    });
    tracing::info!(event = "custom_tsx_document_created");
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn tn_text(id: i32, bytes: *const u8, len: usize) {
    let value = unsafe { text(bytes, len) };
    // Only the fixed counter's decimal values are accepted; no HTML
    // interpolation.
    assert!((id == 1 && matches!(value.as_str(), "0" | "1")) || (id == 2 && value == "Increment"));
    STATE.with_borrow_mut(|state| {
        let state = state.as_mut().expect("initialized document");
        let node = state
            .document
            .query_selector(&format!("#n{id}"))
            .unwrap()
            .expect("fixture node");
        state.document.mutate().set_inner_html(node, &value);
    });
    tracing::info!(event = "custom_tsx_text_mutation", node_id = id);
}

#[unsafe(no_mangle)]
pub extern "C" fn tn_snapshot(frame: i32) -> i32 {
    STATE.with_borrow_mut(|state| {
        let state = state.as_mut().expect("initialized document");
        assert!(matches!(frame, 0 | 1));
        state.document.resolve(0.0);
        let pixels = render_to_buffer::<VelloCpuImageRenderer, _>(
            |scene| {
                scene.fill(
                    Fill::NonZero,
                    Default::default(),
                    Color::WHITE,
                    Default::default(),
                    &Rect::new(0.0, 0.0, 640.0, 360.0),
                );
                paint_scene(scene, state.document.as_mut(), 1.0, 640, 360, 0, 0);
            },
            640,
            360,
        );
        assert!(
            pixels
                .chunks_exact(4)
                .any(|pixel| pixel[..3] != [255, 255, 255]),
            "frame must contain painted content"
        );
        if frame == 0 {
            assert!(state.first_pixels.is_none());
            state.first_pixels = Some(pixels.clone());
        } else {
            assert_ne!(
                state.first_pixels.as_ref().expect("initial frame"),
                &pixels,
                "text mutation must change pixels"
            );
        }
        let output = std::env::var("TN_PROBE_OUTPUT").expect("explicit output directory");
        let file =
            File::create(std::path::Path::new(&output).join(format!("frame-{frame}.png"))).unwrap();
        let mut encoder = png::Encoder::new(file, 640, 360);
        encoder.set_color(png::ColorType::Rgba);
        encoder.set_depth(png::BitDepth::Eight);
        encoder
            .write_header()
            .unwrap()
            .write_image_data(&pixels)
            .unwrap();
        tracing::info!(event = "custom_tsx_frame_rendered", frame);
        1
    })
}
