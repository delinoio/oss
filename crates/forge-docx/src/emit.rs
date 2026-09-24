use forge_document::{Align, Assets, Direction, Style};
use forge_package::*;
use forge_tree_doc::{ErrorCode, Result};
use uuid::Uuid;

use crate::*;

const WP: &str = "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing";
const PIC: &str = "http://schemas.openxmlformats.org/drawingml/2006/picture";
const DOC: &str = "word/document.xml";

pub(crate) struct Writer<'a> {
    pub parts: Package,
    pub assets: &'a Assets,
    sequence: u32,
    drawing_ids: std::collections::HashSet<u32>,
    main: String,
    list_ids: [Option<u32>; 2],
}

fn twips(value: f64) -> i64 {
    (value * 20.0).round() as i64
}
fn emu(value: f64) -> i64 {
    (value * 12700.0).round() as i64
}

pub(crate) fn rpr(style: &Style) -> String {
    let mut out = String::from("<w:rPr>");
    if let Some(family) = &style.font_family {
        let f = escape(family);
        out.push_str(&format!(
            "<w:rFonts w:ascii=\"{f}\" w:hAnsi=\"{f}\" w:eastAsia=\"{f}\" w:cs=\"{f}\"/>"
        ));
    }
    if let Some(size) = style.font_size {
        let s = (size * 2.0).round();
        out.push_str(&format!("<w:sz w:val=\"{s}\"/><w:szCs w:val=\"{s}\"/>"));
    }
    if let Some(enabled) = style.bold {
        let value = u8::from(enabled);
        out.push_str(&format!(
            "<w:b w:val=\"{value}\"/><w:bCs w:val=\"{value}\"/>"
        ));
    }
    if let Some(enabled) = style.italic {
        let value = u8::from(enabled);
        out.push_str(&format!(
            "<w:i w:val=\"{value}\"/><w:iCs w:val=\"{value}\"/>"
        ));
    }
    if let Some(enabled) = style.underline {
        out.push_str(if enabled {
            "<w:u w:val=\"single\"/>"
        } else {
            "<w:u w:val=\"none\"/>"
        });
    }
    if style.direction == Direction::Rtl {
        out.push_str("<w:rtl/>");
    }
    if let Some(color) = &style.color {
        out.push_str(&format!("<w:color w:val=\"{}\"/>", &color[1..]));
    }
    if let Some(color) = &style.background {
        out.push_str(&format!(
            "<w:shd w:val=\"clear\" w:color=\"auto\" w:fill=\"{}\"/>",
            &color[1..]
        ));
    }
    if let Some(language) = &style.language {
        let l = escape(language);
        out.push_str(&format!(
            "<w:lang w:val=\"{l}\" w:eastAsia=\"{l}\" w:bidi=\"{l}\"/>"
        ));
    }
    out.push_str("</w:rPr>");
    out
}

impl<'a> Writer<'a> {
    pub fn new(parts: Package, assets: &'a Assets, main: &str) -> Result<Self> {
        let mut drawing_ids = std::collections::HashSet::new();
        for (path, bytes) in &parts {
            if path.ends_with(".xml") {
                let doc = xml(bytes)?;
                for node in doc.descendants().filter(|n| n.has_tag_name((WP, "docPr"))) {
                    let id = node
                        .attribute("id")
                        .and_then(|v| v.parse().ok())
                        .ok_or_else(|| failure("drawing identity"))?;
                    drawing_ids.insert(id);
                }
            }
        }
        Ok(Self {
            parts,
            assets,
            sequence: 0,
            drawing_ids,
            main: main.into(),
            list_ids: [None, None],
        })
    }

    fn list_id(&mut self, kind: ListKind) -> Result<u32> {
        let index = if kind == ListKind::Bullet { 0 } else { 1 };
        if let Some(id) = self.list_ids[index] {
            return Ok(id);
        }
        let relationships = relationships(&self.parts, &self.main)?;
        let existing: Vec<_> = relationships
            .iter()
            .filter(|r| r.kind == format!("{R}/numbering"))
            .collect();
        if existing.len() > 1 || existing.iter().any(|r| r.external) {
            return Err(failure("numbering relationship"));
        }
        let path = if let Some(relation) = existing.first() {
            resolve(&self.main, &relation.target)?
        } else {
            let path = loop {
                let candidate = format!("word/numbering{}.xml", Uuid::now_v7().simple());
                if !self
                    .parts
                    .keys()
                    .any(|p| p.eq_ignore_ascii_case(&candidate))
                {
                    break candidate;
                }
            };
            self.parts.insert(
                path.clone(),
                format!("<w:numbering xmlns:w=\"{W}\"/>").into_bytes(),
            );
            add_content_type(
                &mut self.parts,
                &path,
                "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml",
            )?;
            let main = self.main.clone();
            self.relationship(&main, "numbering", &format!("/{path}"), false)?;
            path
        };
        let bytes = self.parts.get(&path).ok_or_else(|| failure("numbering"))?;
        let doc = xml(bytes)?;
        if !doc.root_element().has_tag_name((W, "numbering")) {
            return Err(failure("numbering"));
        }
        let mut used = std::collections::HashSet::new();
        for node in doc.root_element().children().filter(|n| n.is_element()) {
            for attr in ["numId", "abstractNumId"] {
                if let Some(value) = node.attribute((W, attr)) {
                    used.insert(
                        value
                            .parse::<u32>()
                            .map_err(|_| failure("numbering identity"))?,
                    );
                }
            }
        }
        let id = (1..u32::MAX)
            .find(|id| !used.contains(id))
            .ok_or_else(|| failure("numbering identity"))?;
        // Definitions precede concrete lists. Append without reserializing any
        // original numbering, so existing list IDs and overrides keep their bytes.
        let definition = numbering_definition(id, kind);
        let at = doc
            .root_element()
            .children()
            .find(|n| n.has_tag_name((W, "num")) || n.has_tag_name((W, "numIdMacAtCleanup")))
            .map(|n| n.range().start);
        let mut bytes = if let Some(at) = at {
            replace_range(bytes, at..at, &definition)
        } else {
            insert_before_close(bytes, &definition)?
        };
        let concrete = format!(
            "<w:num xmlns:w=\"{W}\" w:numId=\"{id}\"><w:abstractNumId w:val=\"{id}\"/></w:num>"
        );
        let doc = xml(&bytes)?;
        let at = doc
            .root_element()
            .children()
            .find(|n| n.has_tag_name((W, "numIdMacAtCleanup")))
            .map(|n| n.range().start);
        bytes = if let Some(at) = at {
            replace_range(&bytes, at..at, &concrete)
        } else {
            insert_before_close(&bytes, &concrete)?
        };
        self.parts.insert(path, bytes);
        self.list_ids[index] = Some(id);
        Ok(id)
    }

    fn relationship(
        &mut self,
        owner: &str,
        kind: &str,
        target: &str,
        external: bool,
    ) -> Result<String> {
        let id = format!("rId{}", Uuid::now_v7().simple());
        add_relationship(&mut self.parts, owner, &id, &format!("{R}/{kind}"), target)?;
        if external {
            let path = relation_path(owner);
            let bytes = &self.parts[&path];
            let doc = xml(bytes)?;
            let node = doc
                .root_element()
                .children()
                .find(|n| n.attribute("Id") == Some(&id))
                .ok_or_else(|| failure("relationship"))?;
            let source = std::str::from_utf8(bytes).map_err(failure)?;
            let fragment = source[node.range()].replace("/>", " TargetMode=\"External\"/>");
            self.parts
                .insert(path, replace_range(bytes, node.range(), &fragment));
        }
        Ok(id)
    }

    fn drawing(&mut self, width: f64, height: f64, alt: &str, graphic: &str) -> String {
        self.sequence += 1;
        while !self.drawing_ids.insert(self.sequence) {
            self.sequence += 1;
        }
        let (cx, cy) = (emu(width), emu(height));
        format!(
            "<w:p xmlns:w=\"{W}\" xmlns:r=\"{R}\" xmlns:a=\"{A}\" \
             xmlns:wp=\"{WP}\"><w:r><w:drawing><wp:inline distT=\"0\" distB=\"0\" distL=\"0\" \
             distR=\"0\"><wp:extent cx=\"{cx}\" cy=\"{cy}\"/><wp:docPr id=\"{}\" name=\"Drawing \
             {}\" descr=\"{}\"/><wp:cNvGraphicFramePr/><a:graphic>{graphic}</a:graphic></wp:\
             inline></w:drawing></w:r></w:p>",
            self.sequence,
            self.sequence,
            escape(alt)
        )
    }

    pub fn blocks(&mut self, blocks: &[Block], owner: &str) -> Result<String> {
        self.blocks_with_style(blocks, owner, &Style::default())
    }

    fn blocks_with_style(&mut self, blocks: &[Block], owner: &str, base: &Style) -> Result<String> {
        let mut out = String::new();
        for block in blocks {
            out.push_str(&self.block_with_style(block, owner, base)?);
        }
        Ok(out)
    }

    fn block_with_style(&mut self, block: &Block, owner: &str, base: &Style) -> Result<String> {
        forge_tree_doc::cancellation::checkpoint()?;
        match block {
            Block::Paragraph {
                style,
                heading,
                list,
                runs,
                ..
            } => {
                let style = forge_document::fonts::overlay(base, style);
                let mut out = format!("<w:p xmlns:w=\"{W}\" xmlns:r=\"{R}\"><w:pPr>");
                if let Some(heading) = heading {
                    out.push_str(&format!(
                        "<w:pStyle w:val=\"Heading{heading}\"/><w:outlineLvl w:val=\"{}\"/>",
                        heading - 1
                    ));
                }
                if let Some(list) = list {
                    out.push_str(&format!(
                        "<w:numPr><w:ilvl w:val=\"{}\"/><w:numId w:val=\"{}\"/></w:numPr>",
                        list.level,
                        self.list_id(list.kind)?
                    ));
                }
                if style.direction == Direction::Rtl {
                    out.push_str("<w:bidi/>");
                }
                let align = match style.align.unwrap_or_default() {
                    Align::Left => "left",
                    Align::Center => "center",
                    Align::Right => "right",
                    Align::Justify => "both",
                };
                out.push_str(&format!("<w:jc w:val=\"{align}\"/>{}</w:pPr>", rpr(&style)));
                for run in runs {
                    forge_tree_doc::cancellation::checkpoint()?;
                    let rid = run
                        .hyperlink
                        .as_ref()
                        .map(|href| self.relationship(owner, "hyperlink", href, true))
                        .transpose()?;
                    if let Some(rid) = &rid {
                        out.push_str(&format!("<w:hyperlink r:id=\"{rid}\">"));
                    }
                    // Paragraph mark properties do not cascade into runs in Word.
                    // Materialize the inherited style for both direct engine and
                    // React callers, with explicit local overrides.
                    out.push_str(&format!(
                        "<w:r>{}",
                        rpr(&forge_document::fonts::overlay(&style, &run.style))
                    ));
                    for (i, line) in run.text.split('\n').enumerate() {
                        if i != 0 {
                            out.push_str("<w:br/>");
                        }
                        for (j, segment) in line.split('\t').enumerate() {
                            if j != 0 {
                                out.push_str("<w:tab/>");
                            }
                            out.push_str(&format!(
                                "<w:t xml:space=\"preserve\">{}</w:t>",
                                escape(segment)
                            ));
                        }
                    }
                    out.push_str("</w:r>");
                    if rid.is_some() {
                        out.push_str("</w:hyperlink>");
                    }
                }
                out.push_str("</w:p>");
                Ok(out)
            }
            Block::PageBreak { .. } => Ok(format!(
                "<w:p xmlns:w=\"{W}\"><w:r><w:br w:type=\"page\"/></w:r></w:p>"
            )),
            Block::Table { columns, rows, .. } => {
                let grid = table_grid(columns, rows)?;
                let mut out = format!(
                    "<w:tbl xmlns:w=\"{W}\"><w:tblPr><w:tblW w:w=\"{}\" \
                     w:type=\"dxa\"/><w:tblLayout w:type=\"fixed\"/></w:tblPr><w:tblGrid>",
                    twips(columns.iter().sum())
                );
                for width in columns {
                    out.push_str(&format!("<w:gridCol w:w=\"{}\"/>", twips(*width)));
                }
                out.push_str("</w:tblGrid>");
                for (r, row) in rows.iter().enumerate() {
                    forge_tree_doc::cancellation::checkpoint()?;
                    out.push_str("<w:tr>");
                    if row.header {
                        out.push_str("<w:trPr><w:tblHeader/></w:trPr>");
                    }
                    let mut col = 0;
                    while col < columns.len() {
                        let (anchor, cell_index) = grid[r][col];
                        let cell = &rows[anchor].cells[cell_index];
                        let width: f64 = columns[col..col + cell.col_span].iter().sum();
                        out.push_str(&format!(
                            "<w:tc><w:tcPr><w:tcW w:w=\"{}\" w:type=\"dxa\"/>",
                            twips(width)
                        ));
                        if cell.col_span > 1 {
                            out.push_str(&format!("<w:gridSpan w:val=\"{}\"/>", cell.col_span));
                        }
                        if cell.row_span > 1 {
                            out.push_str(if anchor == r {
                                "<w:vMerge w:val=\"restart\"/>"
                            } else {
                                "<w:vMerge/>"
                            });
                        }
                        if let Some(fill) = &cell.style.background {
                            out.push_str(&format!("<w:shd w:fill=\"{}\"/>", &fill[1..]));
                        }
                        out.push_str("</w:tcPr>");
                        if anchor == r {
                            let style = forge_document::fonts::overlay(base, &cell.style);
                            out.push_str(&self.blocks_with_style(&cell.blocks, owner, &style)?);
                        }
                        if anchor != r
                            || !matches!(
                                cell.blocks.last(),
                                Some(
                                    Block::Paragraph { .. }
                                        | Block::PageBreak { .. }
                                        | Block::Image { .. }
                                        | Block::Chart { .. }
                                )
                            )
                        {
                            out.push_str("<w:p/>");
                        }
                        out.push_str("</w:tc>");
                        col += cell.col_span;
                    }
                    out.push_str("</w:tr>");
                }
                out.push_str("</w:tbl>");
                Ok(out)
            }
            Block::Image {
                asset,
                width,
                height,
                alt,
                ..
            } => {
                let bytes = self.assets.get(asset).ok_or_else(|| {
                    forge_tree_doc::Diagnostic::new(
                        ErrorCode::InvalidReference,
                        "image/asset",
                        "Image asset is unavailable",
                    )
                })?;
                let (extension, _, _) = forge_document::image(bytes)?;
                let path = format!("word/media/{}.{}", Uuid::now_v7(), extension);
                self.parts.insert(path.clone(), bytes.clone());
                add_content_type(&mut self.parts, &path, &format!("image/{extension}"))?;
                let rid = self.relationship(owner, "image", &format!("/{path}"), false)?;
                let (cx, cy) = (emu(*width), emu(*height));
                let graphic = format!(
                    "<a:graphicData uri=\"{PIC}\"><pic:pic \
                     xmlns:pic=\"{PIC}\"><pic:nvPicPr><pic:cNvPr id=\"0\" \
                     name=\"Image\"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip \
                     r:embed=\"{rid}\"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:\
                     spPr><a:xfrm><a:off x=\"0\" y=\"0\"/><a:ext cx=\"{cx}\" \
                     cy=\"{cy}\"/></a:xfrm><a:prstGeom \
                     prst=\"rect\"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData>"
                );
                Ok(self.drawing(*width, *height, alt, &graphic))
            }
            Block::Chart {
                chart,
                width,
                height,
                alt,
                ..
            } => {
                let (mut chart_xml, workbook) = chart.office_parts()?;
                let id = Uuid::now_v7();
                let chart_path = format!("word/charts/{id}.xml");
                let workbook_path = format!("word/embeddings/{id}.xlsx");
                let external = format!(
                    "<c:externalData xmlns:c=\"{C}\" xmlns:r=\"{R}\" \
                     r:id=\"rIdData\"><c:autoUpdate val=\"0\"/></c:externalData>"
                );
                let parsed = xml(&chart_xml)?;
                let print = parsed
                    .root_element()
                    .children()
                    .find(|n| n.has_tag_name((C, "printSettings")))
                    .map(|n| n.range().start);
                chart_xml = if let Some(at) = print {
                    replace_range(&chart_xml, at..at, &external)
                } else {
                    insert_before_close(&chart_xml, &external)?
                };
                self.parts.insert(chart_path.clone(), chart_xml);
                self.parts.insert(workbook_path.clone(), workbook);
                add_content_type(
                    &mut self.parts,
                    &chart_path,
                    "application/vnd.openxmlformats-officedocument.drawingml.chart+xml",
                )?;
                add_content_type(
                    &mut self.parts,
                    &workbook_path,
                    "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
                )?;
                add_relationship(
                    &mut self.parts,
                    &chart_path,
                    "rIdData",
                    &format!("{R}/package"),
                    &format!("/{workbook_path}"),
                )?;
                let rid = self.relationship(owner, "chart", &format!("/{chart_path}"), false)?;
                Ok(self.drawing(
                    *width,
                    *height,
                    alt,
                    &format!(
                        "<a:graphicData uri=\"{C}\"><c:chart xmlns:c=\"{C}\" \
                         r:id=\"{rid}\"/></a:graphicData>"
                    ),
                ))
            }
        }
    }
}

fn numbering_definition(id: u32, kind: ListKind) -> String {
    let mut out = format!(
        "<w:abstractNum xmlns:w=\"{W}\" w:abstractNumId=\"{id}\"><w:multiLevelType \
         w:val=\"multilevel\"/>"
    );
    for level in 0..9 {
        let format = if kind == ListKind::Bullet {
            "bullet"
        } else {
            "decimal"
        };
        let text = if kind == ListKind::Bullet {
            "•".into()
        } else {
            format!("%{}.", level + 1)
        };
        out.push_str(&format!(
            "<w:lvl w:ilvl=\"{level}\"><w:start w:val=\"1\"/><w:numFmt \
             w:val=\"{format}\"/><w:lvlText w:val=\"{text}\"/><w:pPr><w:ind w:left=\"{}\" \
             w:hanging=\"360\"/></w:pPr></w:lvl>",
            (level + 1) * 720
        ));
    }
    out.push_str("</w:abstractNum>");
    out
}

pub fn generate(document: &Document, assets: &Assets) -> Result<Vec<u8>> {
    validate(document)?;
    let mut parts = Package::new();
    parts.insert("[Content_Types].xml".into(),b"<Types xmlns=\"http://schemas.openxmlformats.org/package/2006/content-types\"><Default Extension=\"rels\" ContentType=\"application/vnd.openxmlformats-package.relationships+xml\"/><Default Extension=\"xml\" ContentType=\"application/xml\"/></Types>".to_vec());
    add_relationship(
        &mut parts,
        "",
        "rIdDocument",
        &format!("{R}/officeDocument"),
        DOC,
    )?;
    add_content_type(
        &mut parts,
        DOC,
        "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml",
    )?;
    let mut styles = format!("<w:styles xmlns:w=\"{W}\"><w:docDefaults><w:rPrDefault><w:rPr>");
    if let Some(language) = &document.language {
        styles.push_str(&format!("<w:lang w:val=\"{}\"/>", escape(language)));
    }
    styles.push_str("</w:rPr></w:rPrDefault></w:docDefaults>");
    for heading in 1..=9 {
        styles.push_str(&format!(
            "<w:style w:type=\"paragraph\" w:styleId=\"Heading{heading}\"><w:name w:val=\"heading \
             {heading}\"/><w:pPr><w:keepNext/><w:outlineLvl \
             w:val=\"{}\"/></w:pPr><w:rPr><w:b/><w:sz w:val=\"{}\"/></w:rPr></w:style>",
            heading - 1,
            40 - heading * 2
        ));
    }
    styles.push_str("</w:styles>");
    parts.insert("word/styles.xml".into(), styles.into_bytes());
    add_content_type(
        &mut parts,
        "word/styles.xml",
        "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml",
    )?;
    add_relationship(
        &mut parts,
        DOC,
        "rIdStyles",
        &format!("{R}/styles"),
        "styles.xml",
    )?;
    let mut writer = Writer::new(parts, assets, DOC)?;
    let mut body = String::new();
    for (index, section) in document.sections.iter().enumerate() {
        body.push_str(&writer.blocks(&section.blocks, DOC)?);
        let mut properties = String::from("<w:sectPr>");
        for (kind, blocks) in [("header", &section.header), ("footer", &section.footer)] {
            // An explicit empty section header/footer prevents Word's default
            // inheritance from exposing the preceding section's content.
            let path = format!("word/{kind}{}.xml", index + 1);
            let tag = if kind == "header" { "hdr" } else { "ftr" };
            let mut content = writer.blocks(blocks, &path)?;
            if content.is_empty() {
                content.push_str("<w:p/>");
            }
            writer.parts.insert(
                path.clone(),
                format!("<w:{tag} xmlns:w=\"{W}\" xmlns:r=\"{R}\">{content}</w:{tag}>")
                    .into_bytes(),
            );
            add_content_type(
                &mut writer.parts,
                &path,
                &format!(
                    "application/vnd.openxmlformats-officedocument.wordprocessingml.{kind}+xml"
                ),
            )?;
            let rid = writer.relationship(DOC, kind, &format!("/{path}"), false)?;
            properties.push_str(&format!(
                "<w:{kind}Reference w:type=\"default\" r:id=\"{rid}\"/>"
            ));
        }
        properties.push_str(&format!(
            "<w:pgSz w:w=\"{}\" w:h=\"{}\"/><w:pgMar w:top=\"{m}\" w:right=\"{m}\" \
             w:bottom=\"{m}\" w:left=\"{m}\" w:header=\"360\" w:footer=\"360\" \
             w:gutter=\"0\"/></w:sectPr>",
            twips(section.width),
            twips(section.height),
            m = twips(section.margin)
        ));
        if index + 1 == document.sections.len() {
            body.push_str(&properties);
        } else {
            body.push_str(&format!("<w:p><w:pPr>{properties}</w:pPr></w:p>"));
        }
    }
    writer.parts.insert(
        DOC.into(),
        format!(
            "<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?><w:document \
             xmlns:w=\"{W}\" xmlns:r=\"{R}\"><w:body>{body}</w:body></w:document>"
        )
        .into_bytes(),
    );
    validate_office(&writer.parts, OfficeKind::Document)?;
    write(&writer.parts)
}
