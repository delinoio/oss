use std::io::{Cursor, Write};

use forge_package::*;
use forge_tree_doc::ErrorCode;
use zip::{ZipWriter, write::SimpleFileOptions};

fn document() -> Package {
    Package::from([
        (
            "[Content_Types].xml".into(),
            b"<Types xmlns=\"http://schemas.openxmlformats.org/package/2006/content-types\"/>"
                .to_vec(),
        ),
        (
            "_rels/.rels".into(),
            format!(
                "<Relationships xmlns=\"{REL}\"><Relationship Id=\"root\" \
                 Type=\"{R}/officeDocument\" Target=\"word/document.xml\"/></Relationships>"
            )
            .into_bytes(),
        ),
        (
            "word/document.xml".into(),
            format!(
                "<?xml version=\"1.0\"?><w:document xmlns:w=\"{W}\"><w:body><!-- opaque \
                 --><w:p><w:r><w:t>Before</w:t></w:r></w:p><w:p><w:r><w:t>Unchanged</w:t></w:r></\
                 w:p></w:body></w:document>"
            )
            .into_bytes(),
        ),
        ("custom.bin".into(), vec![0, 1, 255]),
    ])
}

#[test]
fn edits_preserve_unrelated_bytes_and_reject_stale_overlapping_regions() {
    let parts = document();
    assert_eq!(
        validate_office(&parts, OfficeKind::Document).unwrap(),
        "word/document.xml"
    );
    let text = std::str::from_utf8(&parts["word/document.xml"]).unwrap();
    let start = text.find("Before").unwrap();
    let region = Region::new("word/document.xml", start..start + 6, &parts).unwrap();
    let replacement = Replacement {
        region: &region,
        xml: "After &amp; safe",
    };
    let next = replace_regions(&parts, &[replacement]).unwrap();
    assert_eq!(next["custom.bin"], parts["custom.bin"]);
    assert_eq!(next["_rels/.rels"], parts["_rels/.rels"]);
    assert_eq!(
        next["word/document.xml"],
        text.replace("Before", "After &amp; safe").as_bytes()
    );
    let stale = replace_regions(
        &next,
        &[Replacement {
            region: &region,
            xml: "stale",
        }],
    )
    .unwrap_err();
    assert_eq!(stale.code, ErrorCode::RevisionConflict);
    let overlap = replace_regions(
        &parts,
        &[
            Replacement {
                region: &region,
                xml: "one",
            },
            Replacement {
                region: &region,
                xml: "two",
            },
        ],
    )
    .unwrap_err();
    assert_eq!(overlap.code, ErrorCode::RevisionConflict);
    assert!(
        replace_regions(
            &parts,
            &[Replacement {
                region: &region,
                xml: "<broken"
            }]
        )
        .is_err()
    );
    assert_eq!(parts["word/document.xml"], text.as_bytes());
}

fn archive(names: &[&str]) -> Vec<u8> {
    let mut writer = ZipWriter::new(Cursor::new(Vec::new()));
    for name in names {
        writer
            .start_file(*name, SimpleFileOptions::default())
            .unwrap();
        writer.write_all(b"<root/>").unwrap();
    }
    writer.finish().unwrap().into_inner()
}

#[test]
fn reject_ambiguous_paths_entities_and_unresolved_references() {
    for names in [
        vec!["a.xml", "A.xml"],
        vec!["../a"],
        vec!["a%2fb"],
        vec!["a\\b"],
        vec!["a:b"],
    ] {
        assert_eq!(
            read(&archive(&names)).unwrap_err().code,
            ErrorCode::InvalidPackage
        );
    }
    assert!(xml(b"<!DOCTYPE a [<!ENTITY x SYSTEM 'file:///etc/passwd'>]><a>&x;</a>").is_err());
    let mut parts = document();
    parts.insert(
        "word/document.xml".into(),
        format!(
            "<w:document xmlns:w=\"{W}\" xmlns:r=\"{R}\"><w:body><w:hyperlink \
             r:id=\"missing\"/></w:body></w:document>"
        )
        .into_bytes(),
    );
    assert_eq!(
        validate_office(&parts, OfficeKind::Document)
            .unwrap_err()
            .code,
        ErrorCode::InvalidReference
    );
    assert_eq!(
        validate_office(&document(), OfficeKind::Workbook)
            .unwrap_err()
            .code,
        ErrorCode::UnsupportedPackage
    );
}

#[test]
fn bounded_xml_depth_and_macro_types_fail_before_editing() {
    let nested = format!("{}{}", "<a>".repeat(130), "</a>".repeat(130));
    assert_eq!(
        xml(nested.as_bytes()).unwrap_err().code,
        ErrorCode::ResourceLimit
    );
    let mut parts = document();
    parts.insert("[Content_Types].xml".into(), b"<Types><Override ContentType=\"application/vnd.ms-word.document.macroEnabled.main+xml\"/></Types>".to_vec());
    assert_eq!(
        validate_office(&parts, OfficeKind::Document)
            .unwrap_err()
            .code,
        ErrorCode::UnsupportedPackage
    );
    let bytes = write(&document()).unwrap();
    assert_eq!(read(&bytes).unwrap(), document());
}

#[test]
fn declared_zip_expansion_entry_and_xml_node_budgets_fail_before_inflation() {
    let mut compressed = archive(&[
        "one.bin",
        "two.bin",
        "three.bin",
        "four.bin",
        "five.bin",
        "six.bin",
        "seven.bin",
        "eight.bin",
        "nine.bin",
    ]);
    // Forge the central directory's uncompressed sizes. Reject these before
    // trying to inflate the deliberately tiny payload, independently of CRC.
    let positions: Vec<_> = compressed
        .windows(4)
        .enumerate()
        .filter(|(_, bytes)| *bytes == b"PK\x01\x02")
        .map(|(at, _)| at)
        .collect();
    for at in positions {
        compressed[at + 24..at + 28].copy_from_slice(&(MAX_PART_BYTES as u32).to_le_bytes());
    }
    assert_eq!(
        read(&compressed).unwrap_err().code,
        ErrorCode::ResourceLimit
    );
    let mut part = archive(&["part.bin"]);
    let at = part.windows(4).position(|b| b == b"PK\x01\x02").unwrap();
    part[at + 24..at + 28].copy_from_slice(&(MAX_PART_BYTES as u32 + 1).to_le_bytes());
    assert_eq!(read(&part).unwrap_err().code, ErrorCode::ResourceLimit);
    let names: Vec<_> = (0..=MAX_ENTRIES).map(|i| format!("{i}.bin")).collect();
    assert_eq!(
        read(&archive(
            &names.iter().map(String::as_str).collect::<Vec<_>>()
        ))
        .unwrap_err()
        .code,
        ErrorCode::ResourceLimit
    );
    let nodes = format!("<root>{}</root>", "<n/>".repeat(1_000_000));
    assert_eq!(
        xml(nodes.as_bytes()).unwrap_err().code,
        ErrorCode::ResourceLimit
    );
}

#[test]
fn legacy_signed_strict_and_non_xml_extension_case_cannot_bypass_package_checks() {
    assert_eq!(
        read(&[0xd0, 0xcf, 0x11, 0xe0]).unwrap_err().code,
        ErrorCode::UnsupportedPackage
    );
    for name in ["_xmlsignatures/origin.sigs", "word/vbaProject.bin"] {
        assert_eq!(
            read(&archive(&[name])).unwrap_err().code,
            ErrorCode::UnsupportedPackage
        );
    }
    let mut parts = document();
    parts.insert(
        "unsafe.XML".into(),
        b"<!DOCTYPE root [<!ENTITY x 'hidden'>]><root>&x;</root>".to_vec(),
    );
    assert_eq!(
        read(&write(&parts).unwrap()).unwrap_err().code,
        ErrorCode::InvalidPackage
    );
    let mut parts = document();
    parts.insert("word/document.xml".into(), b"<w:document xmlns:w=\"http://purl.oclc.org/ooxml/wordprocessingml/main\"><w:body/></w:document>".to_vec());
    assert_eq!(
        validate_office(&parts, OfficeKind::Document)
            .unwrap_err()
            .code,
        ErrorCode::UnsupportedPackage
    );
}
