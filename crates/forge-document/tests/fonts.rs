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
#[cfg_attr(
    not(target_os = "macos"),
    ignore = "Requires installed system CJK/RTL/color emoji fonts; enabled in React Forge CI"
)]
fn system_fallback_shapes_mixed_scripts_and_color_emoji() {
    let mut fonts = Fonts::new(true, &[]).unwrap();
    for text in ["Latin 한국어 日本語 中文", "Hello مرحبا שלום 123", "😀 👨‍👩‍👧‍👦"]
    {
        let shaped = fonts
            .shape(&run(text), &Style::default(), 300.0, true)
            .unwrap_or_else(|error| panic!("System fallback fixture {text:?}: {error}"));
        assert!(shaped.layout.height() > 0.0);
    }
}

#[test]
#[cfg_attr(
    not(target_os = "macos"),
    ignore = "Requires installed system CJK/RTL/color emoji fonts; enabled in React Forge CI"
)]
fn office_fallback_runs_preserve_logical_text_and_styles() {
    let mut fonts = Fonts::new(true, &[]).unwrap();
    let input = "Latin 한국어 日本語 中文 مرحبا שלום 😀\nnext line";
    let base = Style {
        font_family: Some("Helvetica".into()),
        bold: Some(true),
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
            .all(|r| r.style.bold == Some(true) && r.style.font_family.is_some())
    );
    assert!(
        resolved
            .iter()
            .any(|r| r.text.contains('한') && r.style.font_family.as_deref() != Some("Helvetica"))
    );
    assert!(resolved.iter().any(|r| {
        r.text.contains('😀')
            && r.style
                .font_family
                .as_deref()
                .is_some_and(|name| name.contains("Emoji"))
    }));
}

#[test]
fn explicit_false_run_styles_override_inherited_emphasis() {
    let mut fonts = Fonts::new(false, &[forge_tree_doc::FONT_BYTES.to_vec()]).unwrap();
    let base = Style {
        bold: Some(true),
        italic: Some(true),
        underline: Some(true),
        ..Default::default()
    };
    let local = Run {
        text: "Regular".into(),
        style: Style {
            bold: Some(false),
            italic: Some(false),
            underline: Some(false),
            ..Default::default()
        },
        hyperlink: None,
    };
    let shaped = fonts.shape(&[local], &base, 100.0, false).unwrap();
    assert_eq!(shaped.runs[0].style.bold, Some(false));
    assert_eq!(shaped.runs[0].style.italic, Some(false));
    assert_eq!(shaped.runs[0].style.underline, Some(false));
}

#[test]
fn restricted_subset_and_bitmap_only_font_permissions_fail_before_pdf_embedding() {
    let source = forge_tree_doc::FONT_BYTES;
    let tables = u16::from_be_bytes(source[4..6].try_into().unwrap()) as usize;
    let entry = (0..tables)
        .map(|i| 12 + 16 * i)
        .find(|at| &source[*at..*at + 4] == b"OS/2")
        .unwrap();
    let offset = u32::from_be_bytes(source[entry + 8..entry + 12].try_into().unwrap()) as usize;
    // Mutate the in-memory OFL fixture's fsType solely to exercise each license
    // policy branch; no modified or restricted font is distributed.
    for permissions in [0x0002_u16, 0x0100, 0x0200] {
        let mut bytes = source.to_vec();
        bytes[offset + 8..offset + 10].copy_from_slice(&permissions.to_be_bytes());
        let mut fonts = Fonts::new(false, &[bytes]).unwrap();
        let error = fonts
            .shape(&run("Permissions"), &Style::default(), 180.0, true)
            .err()
            .unwrap();
        assert_eq!(error.code, ErrorCode::FontUnavailable);
        assert_eq!(error.path, "fonts/embedding");
        assert!(
            fonts
                .shape(&run("Reference only"), &Style::default(), 180.0, false)
                .is_ok()
        );
    }
}
