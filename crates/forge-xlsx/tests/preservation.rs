use forge_package::{S, read, xml};
use forge_xlsx::*;
use serde_json::json;
use uuid::Uuid;

const EXTERNAL: &[u8] = include_bytes!("fixtures/external.xlsx");

#[test]
fn external_cells_preserve_original_styles_opaque_extensions_and_unrelated_parts() {
    let original = import(EXTERNAL).unwrap();
    let target = original
        .targets
        .iter()
        .find(|t| t.text == "External text")
        .unwrap();
    let cell = Cell {
        id: Uuid::now_v7(),
        address: target.address.unwrap(),
        value: Value::Text("Updated & preserved".into()),
        format: None,
        hyperlink: None,
    };
    let bytes = replace(&original, &[(target.id, EditValue::Cell(cell))]).unwrap();
    let parts = read(&bytes).unwrap();
    for (name, bytes) in &original.parts {
        if name != &target.region.part && name != &original.main {
            assert_eq!(&parts[name], bytes, "{name}");
        }
    }
    let before = &original.parts[&target.region.part];
    let after = &parts[&target.region.part];
    assert_eq!(
        &before[..target.region.range.start],
        &after[..target.region.range.start]
    );
    assert!(after.ends_with(&before[target.region.range.end..]));
    assert!(
        import(&bytes)
            .unwrap()
            .targets
            .iter()
            .any(|t| t.text == "Updated & preserved")
    );
    assert!(
        std::str::from_utf8(after)
            .unwrap()
            .contains("Preserve unsupported rule extensions.")
    );
}

#[test]
fn imported_cell_format_grafts_resources_without_renumbering_existing_styles() {
    let original = import(EXTERNAL).unwrap();
    let target = original
        .targets
        .iter()
        .find(|t| t.text == "External text")
        .unwrap();
    let format:CellFormat=serde_json::from_value(json!({"style":{"font_family":"Arial","color":"#AA2244","background":"#CCEEFF","bold":true},"number_format":"0.0000","border":true})).unwrap();
    let cell = Cell {
        id: Uuid::now_v7(),
        address: target.address.unwrap(),
        value: Value::Number(12.3456),
        format: Some(format),
        hyperlink: None,
    };
    let bytes = replace(&original, &[(target.id, EditValue::Cell(cell))]).unwrap();
    let next = import(&bytes).unwrap();
    let old_styles = xml(&original.parts["xl/styles.xml"]).unwrap();
    let new_styles = xml(&next.parts["xl/styles.xml"]).unwrap();
    for collection in ["fonts", "fills", "borders", "cellStyleXfs", "cellXfs"] {
        let old = old_styles
            .root_element()
            .children()
            .find(|n| n.has_tag_name((S, collection)))
            .unwrap();
        let new = new_styles
            .root_element()
            .children()
            .find(|n| n.has_tag_name((S, collection)))
            .unwrap();
        let previous: Vec<_> = old.children().filter(|n| n.is_element()).collect();
        let appended: Vec<_> = new.children().filter(|n| n.is_element()).collect();
        assert!(appended.len() > previous.len());
        for (a, b) in previous.iter().zip(&appended) {
            assert_eq!(
                &original.parts["xl/styles.xml"][a.range()],
                &next.parts["xl/styles.xml"][b.range()]
            );
        }
    }
}

#[test]
fn all_conditional_format_families_and_validations_edit_individually_inside_external_containers() {
    let original = import(EXTERNAL).unwrap();
    let rules: Vec<_> = original
        .targets
        .iter()
        .filter(|t| t.kind == TargetKind::ConditionalFormat)
        .collect();
    let validations: Vec<_> = original
        .targets
        .iter()
        .filter(|t| t.kind == TargetKind::Validation)
        .collect();
    assert_eq!(rules.len(), 5);
    assert_eq!(validations.len(), 7);
    let kinds = [
        json!({"type":"cell_value","operator":"less_than","values":["20"],"format":{"style":{"color":"#112233"}}}),
        json!({"type":"formula","formula":"B2<20","format":{"style":{"bold":true}}}),
        json!({"type":"color_scale","thresholds":[{"kind":"minimum"},{"kind":"maximum"}],"colors":["#001122","#CCDDEE"]}),
        json!({"type":"data_bar","minimum":{"kind":"minimum"},"maximum":{"kind":"maximum"},"color":"#884422"}),
        json!({"type":"icon_set","icons":"three_arrows","thresholds":[{"kind":"percent","value":"0"},{"kind":"percent","value":"40"},{"kind":"percent","value":"80"}]}),
    ];
    for (target, rule) in rules.iter().zip(kinds) {
        let model = ConditionalFormat {
            id: Uuid::now_v7(),
            range: target.range.unwrap(),
            rule: serde_json::from_value(rule).unwrap(),
        };
        let bytes = replace(
            &original,
            &[(target.id, EditValue::ConditionalFormat(model))],
        )
        .unwrap();
        let parts = read(&bytes).unwrap();
        let old = &original.parts[&target.region.part];
        let new = &parts[&target.region.part];
        assert_eq!(
            &new[..target.region.range.start],
            &old[..target.region.range.start]
        );
        assert!(new.ends_with(&old[target.region.range.end..]));
        assert_eq!(
            import(&bytes)
                .unwrap()
                .targets
                .iter()
                .filter(|t| t.kind == TargetKind::ConditionalFormat)
                .count(),
            5
        );
    }
    for (target, kind) in validations.iter().zip([
        ValidationKind::List,
        ValidationKind::Integer,
        ValidationKind::Decimal,
        ValidationKind::Date,
        ValidationKind::Time,
        ValidationKind::TextLength,
        ValidationKind::Custom,
    ]) {
        let rule = Validation {
            id: Uuid::now_v7(),
            range: target.range.unwrap(),
            kind,
            operator: Some(Comparison::Between),
            formulas: if matches!(kind, ValidationKind::List | ValidationKind::Custom) {
                vec!["B2>0".into()]
            } else {
                vec!["2".into(), "20".into()]
            },
            allow_blank: true,
            prompt: Some("Choose a value".into()),
            error: Some("Invalid value".into()),
        };
        let bytes = replace(&original, &[(target.id, EditValue::Validation(rule))]).unwrap();
        assert_eq!(
            import(&bytes)
                .unwrap()
                .targets
                .iter()
                .filter(|t| t.kind == TargetKind::Validation)
                .count(),
            7
        );
    }
}
