use forge_tree_doc::{FONT_BYTES, Result};

use crate::package::*;

/// Wrap the pinned OFL font in uncompressed EOT for PresentationML consumers.
/// EOT uses little-endian headers while the unchanged sfnt payload is
/// big-endian. Format: https://www.w3.org/submissions/EOT/#Version20001
fn eot() -> Result<Vec<u8>> {
    fn u16be(data: &[u8], at: usize) -> Result<u16> {
        Ok(u16::from_be_bytes(
            data.get(at..at + 2)
                .ok_or_else(|| failure("font table"))?
                .try_into()
                .map_err(failure)?,
        ))
    }
    fn u32be(data: &[u8], at: usize) -> Result<u32> {
        Ok(u32::from_be_bytes(
            data.get(at..at + 4)
                .ok_or_else(|| failure("font table"))?
                .try_into()
                .map_err(failure)?,
        ))
    }
    fn table(tag: &[u8]) -> Result<&'static [u8]> {
        for i in 0..usize::from(u16be(FONT_BYTES, 4)?) {
            let at = 12 + 16 * i;
            if FONT_BYTES.get(at..at + 4) == Some(tag) {
                let off = u32be(FONT_BYTES, at + 8)? as usize;
                let len = u32be(FONT_BYTES, at + 12)? as usize;
                return FONT_BYTES
                    .get(off..off + len)
                    .ok_or_else(|| failure("font table"));
            }
        }
        Err(failure("font table"))
    }
    let os2 = table(b"OS/2")?;
    let head = table(b"head")?;
    let names = table(b"name")?;
    let mut out = vec![0u8; 82];
    out[4..8].copy_from_slice(&(FONT_BYTES.len() as u32).to_le_bytes());
    out[8..12].copy_from_slice(&0x0002_0001u32.to_le_bytes());
    out[16..26].copy_from_slice(&os2[32..42]);
    out[26] = 1;
    out[27] = (u16be(os2, 62)? & 1) as u8;
    out[28..32].copy_from_slice(&u32::from(u16be(os2, 4)?).to_le_bytes());
    out[32..34].copy_from_slice(&u16be(os2, 8)?.to_le_bytes());
    out[34..36].copy_from_slice(&0x504cu16.to_le_bytes());
    for (source, dest) in [(42, 36), (46, 40), (50, 44), (54, 48), (78, 52), (82, 56)] {
        out[dest..dest + 4].copy_from_slice(&u32be(os2, source)?.to_le_bytes());
    }
    out[60..64].copy_from_slice(&u32be(head, 8)?.to_le_bytes());
    let strings = usize::from(u16be(names, 4)?);
    for id in [1, 2, 5, 4] {
        let mut value = None;
        for i in 0..usize::from(u16be(names, 2)?) {
            let at = 6 + 12 * i;
            if u16be(names, at)? == 3
                && u16be(names, at + 4)? == 0x409
                && u16be(names, at + 6)? == id
            {
                let length = usize::from(u16be(names, at + 8)?);
                let start = strings + usize::from(u16be(names, at + 10)?);
                value = Some(
                    names
                        .get(start..start + length)
                        .ok_or_else(|| failure("font name"))?,
                );
                break;
            }
        }
        let value = value.ok_or_else(|| failure("font name"))?;
        out.extend_from_slice(&(value.len() as u16).to_le_bytes());
        for pair in value.chunks_exact(2) {
            out.extend_from_slice(&[pair[1], pair[0]]);
        }
        out.extend_from_slice(&[0, 0]);
    }
    out.extend_from_slice(&[0, 0]); // Empty RootString; OFL permits unrestricted embedding.
    out.extend_from_slice(FONT_BYTES);
    let size = out.len() as u32;
    out[0..4].copy_from_slice(&size.to_le_bytes());
    Ok(out)
}
pub(crate) fn embed(parts: &mut Package) -> Result<()> {
    let main = main_part(parts)?;
    let bytes = parts
        .get(&main)
        .ok_or_else(|| failure("presentation"))?
        .clone();
    let doc = xml(&bytes)?;
    // Existing embedded definitions are authoritative. Never override them.
    if doc
        .descendants()
        .any(|n| n.has_tag_name((P, "font")) && n.attribute("typeface") == Some("Noto Sans KR"))
    {
        return Ok(());
    }
    let path = "ppt/fonts/forge-noto-sans-kr.fntdata";
    parts.insert(path.into(), eot()?);
    add_content_type(parts, path, "application/x-fontdata")?;
    add_relationship(
        parts,
        &main,
        "rIdForgeFont",
        &format!("{R}/font"),
        &format!("/{path}"),
    )?;
    let entry = format!(
        "<p:embeddedFont xmlns:p=\"{P}\" xmlns:r=\"{R}\"><p:font typeface=\"Noto Sans \
         KR\"/><p:regular r:id=\"rIdForgeFont\"/></p:embeddedFont>"
    );
    let mut content = std::str::from_utf8(&bytes).map_err(failure)?.to_string();
    if let Some(list) = doc
        .descendants()
        .find(|n| n.has_tag_name((P, "embeddedFontLst")))
    {
        let end = list.range().end;
        let pos = content[..end]
            .rfind("</")
            .ok_or_else(|| failure("font list"))?;
        content.insert_str(pos, &entry);
    } else {
        let root = doc.root_element();
        let pos = root
            .children()
            .find(|n| {
                n.is_element()
                    && matches!(
                        n.tag_name().name(),
                        "custShowLst"
                            | "photoAlbum"
                            | "custDataLst"
                            | "kinsoku"
                            | "defaultTextStyle"
                            | "modifyVerifier"
                            | "extLst"
                    )
            })
            .map(|n| n.range().start)
            .unwrap_or_else(|| content.rfind("</").unwrap());
        content.insert_str(
            pos,
            &format!("<p:embeddedFontLst xmlns:p=\"{P}\">{entry}</p:embeddedFontLst>"),
        );
    }
    let doc = xml(content.as_bytes())?;
    let root = doc.root_element();
    if let Some(attr) = root.attributes().find(|a| a.name() == "embedTrueTypeFonts") {
        content = String::from_utf8(replace_range(
            content.as_bytes(),
            attr.range(),
            "embedTrueTypeFonts=\"1\"",
        ))
        .map_err(failure)?;
    } else {
        let pos = root.range().start
            + content[root.range().start..]
                .find(' ')
                .ok_or_else(|| failure("presentation"))?;
        content.insert_str(pos, " embedTrueTypeFonts=\"1\"");
    }
    parts.insert(main, content.into_bytes());
    Ok(())
}
#[test]
fn embedded_font_retains_the_pinned_payload_and_embedding_rights() {
    let data = eot().unwrap();
    assert_eq!(&data[data.len() - FONT_BYTES.len()..], FONT_BYTES);
    assert_eq!(&data[32..36], &[0, 0, 0x4c, 0x50]);
    assert_eq!(
        u32::from_le_bytes(data[0..4].try_into().unwrap()) as usize,
        data.len()
    );
}
