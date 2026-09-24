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

#[test]
fn package_input_and_output_part_expansion_limits_are_independent() {
    assert_eq!(
        read(&vec![0; MAX_PACKAGE_BYTES + 1]).unwrap_err().code,
        ErrorCode::ResourceLimit
    );
    let mut parts = Package::from([("oversized.bin".into(), vec![0; MAX_PART_BYTES + 1])]);
    assert_eq!(write(&parts).unwrap_err().code, ErrorCode::ResourceLimit);
    parts.clear();
    for i in 0..9 {
        parts.insert(format!("{i}.bin"), vec![0; MAX_PART_BYTES]);
    }
    assert_eq!(write(&parts).unwrap_err().code, ErrorCode::ResourceLimit);
    parts.clear();
    for i in 0..=MAX_ENTRIES {
        parts.insert(format!("{i}.bin"), Vec::new());
    }
    assert_eq!(write(&parts).unwrap_err().code, ErrorCode::ResourceLimit);
    assert_eq!(
        xml(&vec![b' '; MAX_PART_BYTES + 1]).unwrap_err().code,
        ErrorCode::ResourceLimit
    );
    let at_depth = format!("{}{}", "<a>".repeat(128), "</a>".repeat(128));
    assert!(xml(at_depth.as_bytes()).is_ok());
    let at_nodes = format!("<root>{}</root>", "<n/>".repeat(999_998));
    assert!(xml(at_nodes.as_bytes()).is_ok());
}

#[test]
fn relationship_elements_require_the_opc_namespace_and_names() {
    for owner in ["", "word/document.xml"] {
        let path = if owner.is_empty() {
            "_rels/.rels".into()
        } else {
            relation_path(owner)
        };
        let valid = format!(
            "<r:Relationships xmlns:r=\"{REL}\"><r:Relationship Id=\"rId1\" \
             Type=\"{R}/officeDocument\" Target=\"word/document.xml\"/></r:Relationships>"
        );
        let mut parts = document();
        parts.insert(path.clone(), valid.as_bytes().to_vec());
        assert_eq!(relationships(&parts, owner).unwrap().len(), 1);
        for invalid in [
            valid.replace(REL, "urn:foreign"),
            valid.replace("r:Relationships", "r:WrongRoot"),
            valid.replace(
                "<r:Relationship Id",
                "<Relationship xmlns=\"urn:foreign\" Id",
            ),
            valid.replace("<r:Relationship Id", "<r:WrongChild Id"),
        ] {
            parts.insert(path.clone(), invalid.into_bytes());
            assert_eq!(
                relationships(&parts, owner).unwrap_err().code,
                ErrorCode::InvalidPackage
            );
        }
    }
}

#[test]
fn xml_accepts_exactly_128_element_levels_with_text_or_empty_leaves() {
    for leaf in ["<leaf/>", "<leaf>text<!-- comment --></leaf>"] {
        let accepted = format!("{}{}{}", "<a>".repeat(127), leaf, "</a>".repeat(127));
        assert!(xml(accepted.as_bytes()).is_ok());
        let rejected = format!("<a>{accepted}</a>");
        assert_eq!(
            xml(rejected.as_bytes()).unwrap_err().code,
            ErrorCode::ResourceLimit
        );
    }
}
