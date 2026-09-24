use forge_document::{Run, Style, fonts::Fonts};
use forge_tree_doc::ErrorCode;
fn run(text: &str) -> Vec<Run> {
    vec![Run {
        text: text.into(),
        ..Default::default()
    }]
}
#[test]
fn explicit_font_and_missing_fallback_are_checked() {
    let mut fonts = Fonts::new(false, &[forge_tree_doc::FONT_BYTES.to_vec()]).unwrap();
    let shaped = fonts
        .shape(
            &run("Latin 한국어 日本語 中文"),
            &Style::default(),
            180.0,
            true,
        )
        .unwrap();
    assert!(shaped.layout.height() > 0.0);
    let error = fonts
        .shape(&run("\u{10ffff}"), &Style::default(), 180.0, true)
        .err()
        .unwrap();
    assert_eq!(error.code, ErrorCode::FontUnavailable);
    let error = fonts
        .shape(&run("😀"), &Style::default(), 180.0, true)
        .err()
        .unwrap();
    assert_eq!(error.code, ErrorCode::FontUnavailable);
}
#[test]
#[cfg(target_os = "macos")]
fn system_fallback_shapes_mixed_scripts_and_color_emoji() {
    let mut fonts = Fonts::new(true, &[]).unwrap();
    for text in ["Latin 한국어 日本語 中文", "Hello مرحبا שלום 123", "😀 👨‍👩‍👧‍👦"]
    {
        let shaped = fonts
            .shape(&run(text), &Style::default(), 300.0, true)
            .unwrap();
        assert!(shaped.layout.height() > 0.0);
    }
}

#[test]
#[cfg(target_os = "macos")]
fn office_fallback_runs_preserve_logical_text_and_styles() {
    let mut fonts = Fonts::new(true, &[]).unwrap();
    let input = "Latin 한국어 日本語 中文 مرحبا שלום 😀\nnext line";
    let base = Style {
        font_family: Some("Helvetica".into()),
        bold: true,
        ..Default::default()
    };
    let resolved = fonts.resolved_runs(&run(input), &base).unwrap();
    assert_eq!(
        resolved.iter().map(|r| r.text.as_str()).collect::<String>(),
        input
    );
    assert!(
        resolved
            .iter()
            .all(|r| r.style.bold && r.style.font_family.is_some())
    );
    assert!(
        resolved
            .iter()
            .any(|r| r.text.contains('한') && r.style.font_family.as_deref() != Some("Helvetica"))
    );
    assert!(
        resolved.iter().any(|r| r.text.contains('😀')
            && r.style.font_family.as_deref() == Some("Apple Color Emoji"))
    );
}
