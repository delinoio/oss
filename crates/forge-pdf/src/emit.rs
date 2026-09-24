use std::{collections::HashMap, sync::Arc};

use forge_document::{Assets, fonts::Fonts};
use forge_tree_doc::{Diagnostic, ErrorCode, Result, error};
use krilla::{
    Document as Pdf, SerializeSettings,
    action::LinkAction,
    annotation::{Annotation, LinkAnnotation, Target},
    color::rgb,
    geom::{PathBuilder, Point, Rect, Size, Transform},
    image::Image,
    metadata::Metadata,
    page::PageSettings,
    paint::{Fill, Stroke},
    tagging::{ArtifactType, ContentTag, Node, SpanTag, TagGroup, TagTree},
    text::{Font, GlyphId, KrillaGlyph},
};

use crate::{
    Document, Geometry, Layout, ShapeKind,
    layout::{Command, Semantic},
};

pub fn generate(
    document: &Document,
    assets: &Assets,
    fonts: &mut Fonts,
) -> Result<(Vec<u8>, Layout)> {
    let layout = crate::layout(document, fonts)?;
    let mut pdf = Pdf::new_with(SerializeSettings {
        enable_tagging: true,
        ..Default::default()
    });
    let mut metadata = Metadata::new().language(document.language.clone());
    if let Some(title) = &document.title {
        metadata = metadata.title(title.clone());
    }
    pdf.set_metadata(metadata);
    let mut groups: Vec<Vec<(usize, Node)>> = vec![vec![]; layout.semantics.len()];
    let mut font_cache = HashMap::new();
    let mut image_cache = HashMap::new();
    for planned in &layout.pages {
        forge_tree_doc::cancellation::checkpoint()?;
        let mut page = pdf.start_page_with(
            PageSettings::from_wh(planned.width as f32, planned.height as f32)
                .ok_or_else(failed)?,
        );
        let mut annotations = Vec::new();
        let mut surface = page.surface();
        for command in &planned.commands {
            forge_tree_doc::cancellation::checkpoint()?;
            match command {
                Command::Line {
                    text,
                    line,
                    x,
                    y,
                    tags,
                    artifact,
                } => {
                    let line = text.layout.get(*line).ok_or_else(failed)?;
                    let metrics = line.metrics();
                    let mut offset = metrics.offset;

                    for run in line.runs() {
                        let source = run.font();
                        let key = (
                            source.data.id(),
                            source.index,
                            run.normalized_coords().to_vec(),
                        );
                        let font = if let Some(font) = font_cache.get(&key) {
                            Font::clone(font)
                        } else {
                            let variations = variations(
                                source.data.as_ref(),
                                source.index,
                                run.normalized_coords(),
                            )?;
                            let font = Font::new_variable(
                                Arc::new(source.data.as_ref().to_vec()).into(),
                                source.index,
                                &variations,
                            )
                            .ok_or_else(failed)?;
                            font_cache.insert(key, font.clone());
                            font
                        };
                        let size = run.font_size();
                        let mut chunks: Vec<(usize, f32, Vec<KrillaGlyph>, f32)> = Vec::new();
                        for cluster in run.visual_clusters() {
                            forge_tree_doc::cancellation::checkpoint()?;
                            if cluster.is_ligature_continuation() {
                                if let Some((_, _, glyphs, _)) = chunks.last_mut()
                                    && let Some(glyph) = glyphs.last_mut()
                                {
                                    glyph.text_range.start =
                                        glyph.text_range.start.min(cluster.text_range().start);
                                    glyph.text_range.end =
                                        glyph.text_range.end.max(cluster.text_range().end);
                                }
                                continue;
                            }
                            for glyph in cluster.glyphs() {
                                let style =
                                    text.layout.styles()[usize::from(glyph.style_index)].brush;
                                if chunks
                                    .last()
                                    .is_none_or(|(previous, _, _, _)| *previous != style)
                                {
                                    chunks.push((style, offset, Vec::new(), 0.0));
                                }
                                let (_, _, glyphs, advance) = chunks.last_mut().unwrap();
                                glyphs.push(KrillaGlyph::new(
                                    GlyphId::new(glyph.id),
                                    glyph.advance / size,
                                    glyph.x / size,
                                    glyph.y / size,
                                    0.0,
                                    cluster.text_range(),
                                    None,
                                ));
                                *advance += glyph.advance;
                                offset += glyph.advance;
                            }
                        }
                        for (index, offset, glyphs, advance) in chunks {
                            let style = &text.runs[index].style;
                            let start =
                                glyphs.iter().map(|g| g.text_range.start).min().unwrap_or(0);
                            let end = glyphs
                                .iter()
                                .map(|g| g.text_range.end)
                                .max()
                                .unwrap_or(start);
                            let actual:String=text.text[start..end].chars().filter(|c| !matches!(c, '\u{202a}'..='\u{202e}' | '\u{2066}'..='\u{2069}')).collect();
                            let tag = tags[index];
                            let marked = surface.start_tagged(if *artifact {
                                ContentTag::Artifact(ArtifactType::Header)
                            } else {
                                ContentTag::Span(
                                    SpanTag::empty()
                                        .with_lang(style.language.as_deref())
                                        .with_actual_text(Some(&actual)),
                                )
                            });
                            if !artifact {
                                groups[tag].push((start * 2, marked.into()));
                            }
                            let baseline = *y as f32 + metrics.baseline - metrics.min_coord;
                            if let Some(background) = &style.background {
                                surface.set_stroke(None);
                                surface.set_fill(Some(fill(background)?));
                                surface.draw_path(&rectangle(
                                    *x as f32 + offset,
                                    *y as f32,
                                    advance,
                                    metrics.line_height,
                                )?);
                            }
                            surface
                                .set_fill(Some(fill(style.color.as_deref().unwrap_or("#000000"))?));
                            surface.set_stroke(None);
                            surface.draw_glyphs(
                                Point::from_xy(*x as f32 + offset, baseline),
                                &glyphs,
                                font.clone(),
                                &text.text,
                                size,
                                false,
                            );
                            if style.underline.unwrap_or(false) {
                                surface.set_fill(None);
                                surface.set_stroke(Some(Stroke {
                                    paint: rgb_color(style.color.as_deref().unwrap_or("#000000"))?
                                        .into(),
                                    width: (size / 16.0).max(0.5),
                                    ..Default::default()
                                }));
                                let mut path = PathBuilder::new();
                                path.move_to(*x as f32 + offset, baseline + size * 0.1);
                                path.line_to(*x as f32 + offset + advance, baseline + size * 0.1);
                                surface.draw_path(&path.finish().ok_or_else(failed)?);
                            }
                            surface.end_tagged();
                            if !artifact
                                && let Some(href) = &text.runs[index].hyperlink
                                && advance > 0.0
                            {
                                annotations.push((
                                    tag,
                                    start * 2 + 1,
                                    href.clone(),
                                    text.runs[index].text.clone(),
                                    Rect::from_xywh(
                                        *x as f32 + offset,
                                        *y as f32,
                                        advance,
                                        metrics.line_height,
                                    )
                                    .ok_or_else(failed)?,
                                ));
                            }
                        }
                    }
                }
                Command::Image { asset, frame, tag } => {
                    let image = if let Some(image) = image_cache.get(asset) {
                        Image::clone(image)
                    } else {
                        let bytes = assets.get(asset).ok_or_else(|| {
                            Diagnostic::new(
                                ErrorCode::InvalidReference,
                                "image/asset",
                                "Image asset is not registered",
                            )
                        })?;
                        let (format, _, _) = forge_document::image(bytes)?;
                        let data = Arc::new(bytes.clone()).into();
                        let image = if format == "png" {
                            Image::from_png(data, true)
                        } else {
                            Image::from_jpeg(data, true)
                        }
                        .map_err(|_| failed())?;
                        image_cache.insert(asset.clone(), image.clone());
                        image
                    };
                    let marked = surface.start_tagged(ContentTag::Other);
                    groups[*tag].push((0, marked.into()));
                    surface
                        .push_transform(&Transform::from_translate(frame.x as f32, frame.y as f32));
                    surface.draw_image(
                        image,
                        Size::from_wh(frame.width as f32, frame.height as f32)
                            .ok_or_else(failed)?,
                    );
                    surface.pop();
                    surface.end_tagged();
                }
                Command::Shape {
                    kind,
                    frame,
                    fill: color,
                    stroke,
                    tag,
                } => {
                    let marked = surface.start_tagged(if tag.is_some() {
                        ContentTag::Other
                    } else {
                        ContentTag::Artifact(ArtifactType::Other)
                    });
                    if let Some(tag) = tag {
                        groups[*tag].push((0, marked.into()));
                    }
                    surface.set_fill(color.as_deref().map(fill).transpose()?);
                    surface.set_stroke(
                        stroke
                            .as_deref()
                            .map(|color| {
                                Ok::<_, Diagnostic>(Stroke {
                                    paint: rgb_color(color)?.into(),
                                    width: 0.5,
                                    ..Default::default()
                                })
                            })
                            .transpose()?,
                    );
                    surface.draw_path(&shape(*kind, frame)?);
                    surface.end_tagged();
                }
            }
        }
        surface.finish();
        for (tag, order, href, alt, rect) in annotations {
            let annotation = Annotation::new_link(
                LinkAnnotation::new(rect, Target::Action(LinkAction::new(href).into())),
                Some(alt),
            );
            groups[tag].push((order, page.add_tagged_annotation(annotation).into()));
        }
        page.finish();
    }
    fn build_group(
        index: usize,
        semantics: &[Semantic],
        content: &mut [Vec<(usize, Node)>],
    ) -> TagGroup {
        let mut group = TagGroup::new(semantics[index].tag.clone());
        for child in &semantics[index].children {
            group.push(build_group(*child, semantics, content));
        }
        content[index].sort_by_key(|(order, _)| *order);
        for (_, node) in std::mem::take(&mut content[index]) {
            group.push(node);
        }
        group
    }
    let mut tree = TagTree::new().with_lang(Some(document.language.clone()));
    for root in &layout.roots {
        tree.push(build_group(*root, &layout.semantics, &mut groups));
    }
    pdf.set_tag_tree(tree);
    let bytes = bounded_output(pdf.finish().map_err(|_| failed())?)?;
    Ok((bytes, layout))
}
fn bounded_output(bytes: Vec<u8>) -> Result<Vec<u8>> {
    if bytes.len() > 256 * 1024 * 1024 {
        return error(
            ErrorCode::ResourceLimit,
            "pdf",
            "PDF output exceeds 256 MiB",
        );
    }
    Ok(bytes)
}
fn failed() -> Diagnostic {
    Diagnostic::new(
        ErrorCode::InvalidField,
        "pdf",
        "Native PDF serialization failed",
    )
}
fn rgb_color(value: &str) -> Result<rgb::Color> {
    let color = forge_document::color(value)?;
    Ok(rgb::Color::new(
        (color >> 16) as u8,
        (color >> 8) as u8,
        color as u8,
    ))
}
fn fill(color: &str) -> Result<Fill> {
    Ok(Fill {
        paint: rgb_color(color)?.into(),
        ..Default::default()
    })
}
fn rectangle(x: f32, y: f32, w: f32, h: f32) -> Result<krilla::geom::Path> {
    let mut path = PathBuilder::new();
    path.move_to(x, y);
    path.line_to(x + w, y);
    path.line_to(x + w, y + h);
    path.line_to(x, y + h);
    path.close();
    path.finish().ok_or_else(failed)
}
fn shape(kind: ShapeKind, frame: &Geometry) -> Result<krilla::geom::Path> {
    let (x, y, w, h) = (
        frame.x as f32,
        frame.y as f32,
        frame.width as f32,
        frame.height as f32,
    );
    if matches!(kind, ShapeKind::Rectangle) {
        return rectangle(x, y, w, h);
    }
    let mut p = PathBuilder::new();
    if matches!(kind, ShapeKind::Line) {
        p.move_to(x, y);
        p.line_to(x + w, y + h);
    } else {
        let k = 0.5522848;
        let (cx, cy, rx, ry) = (x + w / 2.0, y + h / 2.0, w / 2.0, h / 2.0);
        p.move_to(cx + rx, cy);
        p.cubic_to(cx + rx, cy + k * ry, cx + k * rx, cy + ry, cx, cy + ry);
        p.cubic_to(cx - k * rx, cy + ry, cx - rx, cy + k * ry, cx - rx, cy);
        p.cubic_to(cx - rx, cy - k * ry, cx - k * rx, cy - ry, cx, cy - ry);
        p.cubic_to(cx + k * rx, cy - ry, cx + rx, cy - k * ry, cx + rx, cy);
        p.close();
    }
    p.finish().ok_or_else(failed)
}

/// Match Parley's normalized coordinates, including avar mappings, instead of
/// drawing a variable font at a different weight from the shaping metrics.
fn variations(bytes: &[u8], index: u32, coords: &[i16]) -> Result<Vec<(krilla::text::Tag, f32)>> {
    let mut face = ttf_parser::Face::parse(bytes, index).map_err(|_| failed())?;
    let axes: Vec<_> = face.variation_axes().into_iter().collect();
    let mut result = Vec::new();
    for (axis_index, axis) in axes.iter().enumerate() {
        let target = coords.get(axis_index).copied().unwrap_or(0);
        let (mut low, mut high) = (axis.min_value, axis.max_value);
        for _ in 0..24 {
            let mid = (low + high) / 2.0;
            face.set_variation(axis.tag, mid);
            let current = face
                .variation_coordinates()
                .get(axis_index)
                .map(|c| c.get())
                .unwrap_or(0);
            if current < target {
                low = mid;
            } else {
                high = mid;
            }
        }
        let value = if target == 0 {
            axis.def_value
        } else {
            (low + high) / 2.0
        };
        result.push((krilla::text::Tag::new(&axis.tag.to_bytes()), value));
    }
    Ok(result)
}

#[cfg(test)]
mod output_tests {
    use super::*;

    #[test]
    fn serialized_pdf_accepts_exact_limit_and_rejects_overflow() {
        let bytes = vec![0; 256 * 1024 * 1024 + 1];
        assert_eq!(
            bounded_output(bytes).unwrap_err().code,
            ErrorCode::ResourceLimit
        );
        let bytes = vec![0; 256 * 1024 * 1024];
        assert_eq!(bounded_output(bytes).unwrap().len(), 256 * 1024 * 1024);
    }
}
