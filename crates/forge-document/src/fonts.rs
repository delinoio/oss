//! Opt-in React Forge font discovery, fallback and validation. Existing Forge
//! presentation consumers retain their independent pinned-default font policy.
use std::{borrow::Cow, collections::HashSet, sync::Arc};

use fontique::{Blob, Collection, CollectionOptions, GenericFamily};
use forge_tree_doc::{Diagnostic, ErrorCode, Result, error};
use parley::{
    FontContext, Layout, LayoutContext,
    layout::Alignment,
    style::{
        FontFamily, FontStack, FontStyle, FontWeight, LineHeight, OverflowWrap, StyleProperty,
    },
};

use crate::{Align, Direction, Run, Style};

pub struct Fonts {
    fonts: FontContext,
    layout: LayoutContext<usize>,
    families: Vec<String>,
}
pub struct ShapedText {
    pub text: String,
    pub layout: Layout<usize>,
    pub runs: Vec<Run>,
}
fn missing() -> Diagnostic {
    Diagnostic::new(
        ErrorCode::FontUnavailable,
        "fonts",
        "No usable font represents the required glyphs or color emoji. Register a compatible font \
         or install a system fallback and retry",
    )
}

impl Fonts {
    pub fn new(system: bool, supplied: &[Vec<u8>]) -> Result<Self> {
        let mut fonts = FontContext {
            collection: Collection::new(CollectionOptions {
                system_fonts: system,
                shared: false,
            }),
            source_cache: Default::default(),
        };
        let mut families = Vec::new();
        let mut ids = Vec::new();
        let mut total = 0_usize;
        for bytes in supplied {
            total = total.checked_add(bytes.len()).ok_or_else(missing)?;
            if bytes.len() > 64 * 1024 * 1024 || total > 256 * 1024 * 1024 {
                return error(
                    ErrorCode::ResourceLimit,
                    "fonts",
                    "Font data exceeds asset limits",
                );
            }
            let registered = fonts
                .collection
                .register_fonts(Blob::new(Arc::new(bytes.clone())), None);
            if registered.is_empty() {
                return Err(missing());
            }
            for (id, _) in registered {
                if let Some(name) = fonts.collection.family_name(id) {
                    families.push(name.to_owned());
                }
                ids.push(id);
            }
        }
        // Registered fonts also serve as explicit fallbacks when system discovery
        // is disabled. Generic defaults do not otherwise include private fonts.
        if !system {
            for family in [
                GenericFamily::SansSerif,
                GenericFamily::Serif,
                GenericFamily::Monospace,
                GenericFamily::Emoji,
            ] {
                fonts
                    .collection
                    .set_generic_families(family, ids.iter().copied());
            }
        }
        Ok(Self {
            fonts,
            layout: LayoutContext::new(),
            families,
        })
    }

    pub fn shape(
        &mut self,
        runs: &[Run],
        base: &Style,
        width: f64,
        embedding: bool,
    ) -> Result<ShapedText> {
        base.validate()?;
        if !width.is_finite() || width <= 0.0 || width > 100_000.0 {
            return error(
                ErrorCode::InvalidGeometry,
                "text/width",
                "Text width is invalid",
            );
        }
        let runs: Vec<_> = runs
            .iter()
            .map(|run| Run {
                text: run.text.clone(),
                hyperlink: run.hyperlink.clone(),
                style: overlay(base, &run.style),
            })
            .collect();
        let mut text = String::new();
        match base.direction {
            Direction::Ltr => text.push('\u{202a}'),
            Direction::Rtl => text.push('\u{202b}'),
            Direction::Auto => {}
        }
        let mut ranges = Vec::new();
        for run in &runs {
            run.validate()?;
            match run.style.direction {
                Direction::Ltr => text.push('\u{2066}'),
                Direction::Rtl => text.push('\u{2067}'),
                Direction::Auto => {}
            }
            let start = text.len();
            text.push_str(&run.text);
            ranges.push(start..text.len());
            if run.style.direction != Direction::Auto {
                text.push('\u{2069}');
            }
        }
        if base.direction != Direction::Auto {
            text.push('\u{202c}');
        }
        if text.len() > 16 * 1024 * 1024 {
            return error(
                ErrorCode::ResourceLimit,
                "text",
                "Text exceeds rendered-tree limit",
            );
        }
        let mut builder = self
            .layout
            .ranged_builder(&mut self.fonts, &text, 1.0, false);
        builder.push_default(StyleProperty::FontSize(
            base.font_size.unwrap_or(12.0) as f32
        ));
        builder.push_default(StyleProperty::LineHeight(LineHeight::FontSizeRelative(1.3)));
        builder.push_default(StyleProperty::OverflowWrap(OverflowWrap::Anywhere));
        for (index, run) in runs.iter().enumerate() {
            let style = &run.style;
            let range = ranges[index].clone();
            let mut stack: Vec<_> = style
                .font_family
                .iter()
                .chain(self.families.iter())
                .map(|name| FontFamily::Named(Cow::Owned(name.clone())))
                .collect();
            stack.push(FontFamily::Generic(GenericFamily::SansSerif));
            builder.push(
                StyleProperty::FontStack(FontStack::List(Cow::Owned(stack))),
                range.clone(),
            );
            builder.push(
                StyleProperty::FontSize(style.font_size.unwrap_or(12.0) as f32),
                range.clone(),
            );
            builder.push(
                StyleProperty::FontWeight(FontWeight::new(if style.bold { 700.0 } else { 400.0 })),
                range.clone(),
            );
            builder.push(
                StyleProperty::FontStyle(if style.italic {
                    FontStyle::Italic
                } else {
                    FontStyle::Normal
                }),
                range.clone(),
            );
            builder.push(
                StyleProperty::Locale(style.language.as_deref()),
                range.clone(),
            );
            builder.push(StyleProperty::Brush(index), range);
        }
        let mut layout = builder.build(&text);
        layout.break_all_lines(Some(width as f32));
        layout.align(
            Some(width as f32),
            match base.align {
                Align::Left => Alignment::Left,
                Align::Right => Alignment::Right,
                Align::Center => Alignment::Center,
                Align::Justify => Alignment::Justify,
            },
            Default::default(),
        );
        let mut checked = HashSet::new();
        let mut glyph_count = 0;
        for line in layout.lines() {
            for run in line.runs() {
                let font = run.font();
                let face = ttf_parser::Face::parse(font.data.as_ref(), font.index)
                    .map_err(|_| missing())?;
                if embedding && checked.insert((font.data.id(), font.index)) {
                    use ttf_parser::Permissions;
                    if !matches!(
                        face.permissions(),
                        Some(
                            Permissions::Installable
                                | Permissions::PreviewAndPrint
                                | Permissions::Editable
                        )
                    ) || !face.is_subsetting_allowed()
                        || !face.is_outline_embedding_allowed()
                    {
                        return error(
                            ErrorCode::FontUnavailable,
                            "fonts/embedding",
                            "The selected font does not permit the required PDF subset embedding. \
                             Register an embeddable alternative and retry",
                        );
                    }
                }
                for cluster in run.visual_clusters() {
                    let fragment = &text[cluster.text_range()];
                    let visible = fragment
                        .chars()
                        .any(|c| !c.is_whitespace() && !invisible(c));
                    let mut color = false;
                    for glyph in cluster.glyphs() {
                        glyph_count += 1;
                        if glyph.id == 0 && visible {
                            return Err(missing());
                        }
                        let id = ttf_parser::GlyphId(glyph.id.try_into().map_err(|_| missing())?);
                        color |= face.is_color_glyph(id)
                            || face
                                .glyph_raster_image(id, u16::MAX)
                                .is_some_and(|i| i.format == ttf_parser::RasterImageFormat::PNG);
                    }
                    if visible
                        && cluster.is_emoji()
                        && !fragment.contains('\u{fe0e}')
                        && !color
                        && !cluster.is_ligature_continuation()
                    {
                        return Err(missing());
                    }
                }
            }
        }
        if glyph_count == 0 && text.chars().any(|c| !c.is_whitespace() && !invisible(c)) {
            return Err(missing());
        }
        Ok(ShapedText { text, layout, runs })
    }
}

pub fn invisible(c: char) -> bool {
    c.is_control()
        || matches!(c, '\u{200b}'..='\u{200f}' | '\u{202a}'..='\u{202e}' | '\u{2060}'..='\u{206f}' | '\u{fe00}'..='\u{fe0f}' | '\u{e0100}'..='\u{e01ef}')
}

pub fn overlay(base: &Style, local: &Style) -> Style {
    Style {
        font_family: local
            .font_family
            .clone()
            .or_else(|| base.font_family.clone()),
        font_size: local.font_size.or(base.font_size),
        bold: base.bold || local.bold,
        italic: base.italic || local.italic,
        underline: base.underline || local.underline,
        color: local.color.clone().or_else(|| base.color.clone()),
        background: local.background.clone().or_else(|| base.background.clone()),
        language: local.language.clone().or_else(|| base.language.clone()),
        direction: if local.direction == Direction::Auto {
            base.direction
        } else {
            local.direction
        },
        align: local.align,
    }
}

impl forge_tree_doc::TextLayout for Fonts {
    fn measure(
        &mut self,
        paragraphs: &[forge_tree_doc::Paragraph],
        base: &forge_tree_doc::TextStyle,
        width: f64,
        scale: f64,
    ) -> Result<(f64, f64)> {
        fn style(value: &forge_tree_doc::TextStyle, scale: f64) -> Style {
            Style {
                font_family: value.font_family.clone(),
                font_size: value.font_size.map(|n| n * scale),
                bold: value.font_weight.is_some_and(|n| n >= 600),
                italic: value.italic.unwrap_or(false),
                underline: value.underline.unwrap_or(false),
                color: value.color.as_ref().map(|c| c.clone()),
                ..Default::default()
            }
        }
        let mut base_style = style(base, scale);
        base_style.font_size = Some(base.font_size.unwrap_or(20.0) * scale);
        let (mut width_used, mut height) = (0.0_f64, 0.0);
        for paragraph in paragraphs {
            let runs: Vec<_> = paragraph
                .runs
                .iter()
                .map(|r| Run {
                    text: r.text.clone(),
                    style: style(&r.style, scale),
                    hyperlink: None,
                })
                .collect();
            let text = self.shape(&runs, &base_style, width, false)?;
            width_used = width_used.max(f64::from(text.layout.width()));
            height += f64::from(text.layout.height());
        }
        Ok((width_used, height))
    }
}

impl Fonts {
    pub fn default_family(&mut self) -> Result<String> {
        let ids: Vec<_> = self
            .fonts
            .collection
            .generic_families(GenericFamily::SansSerif)
            .collect();
        for id in ids {
            if let Some(name) = self.fonts.collection.family_name(id) {
                return Ok(name.into());
            }
        }
        self.families.first().cloned().ok_or_else(missing)
    }

    pub fn check_text(&mut self, text: &str, style: &Style) -> Result<()> {
        self.shape(
            &[Run {
                text: text.into(),
                ..Default::default()
            }],
            style,
            100_000.0,
            false,
        )?;
        Ok(())
    }

    pub fn check_chart(&mut self, chart: &crate::Chart) -> Result<()> {
        chart.validate()?;
        let style = Style::default();
        for text in chart
            .title
            .iter()
            .chain(chart.categories.iter())
            .chain(chart.series.iter().map(|s| &s.name))
        {
            self.check_text(text, &style)?;
        }
        Ok(())
    }
}
