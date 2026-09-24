use forge_package::{S, read, xml};
use forge_xlsx::*;
use serde_json::json;

fn range() -> serde_json::Value {
    json!({"first":{"row":1,"column":0},"last":{"row":9,"column":0}})
}
fn stop(kind: &str, value: Option<&str>) -> serde_json::Value {
    json!({"kind":kind,"value":value})
}

fn workbook() -> Workbook {
    let rules = vec![
        json!({"type":"cell_value","operator":"greater_than","values":["3"],"format":{"style":{"color":"#FF0000"}}}),
        json!({"type":"formula","formula":"A2>0","format":{"style":{"background":"#FFFF00"}}}),
        json!({"type":"color_scale","thresholds":[stop("minimum",None),stop("maximum",None)],"colors":["#FF0000","#00FF00"]}),
        json!({"type":"data_bar","minimum":stop("minimum",None),"maximum":stop("maximum",None),"color":"#336699"}),
        json!({"type":"icon_set","icons":"three_arrows","thresholds":[stop("percent",Some("0")),stop("percent",Some("33")),stop("percent",Some("67"))]}),
    ];
    let validations:Vec<_>=["list","integer","decimal","date","time","text_length","custom"].into_iter().map(|kind|json!({"range":range(),"kind":kind,"operator":"between","formulas":if matches!(kind,"list"|"custom"){vec![if kind=="list"{"\"Red,Blue\""}else{"A2>0"}]}else{vec!["1","10"]}})).collect();
    let charts:Vec<_>=["bar","line","pie"].into_iter().enumerate().map(|(i,kind)|json!({"at":{"row":i*15,"column":5},"width":480,"height":280,"alt":"Values","chart":{"kind":kind,"categories":["A","B"],"series":[{"name":"Values","values":[2,5]}],"legend":true,"labels":true}})).collect();
    serde_json::from_value(json!({"sheets":[{
        "name":"Data",
        "cells":[
            {"address":{"row":0,"column":0},"value":{"type":"text","value":"Header"},"format":{"style":{"bold":true,"background":"#CCCCCC"},"border":true}},
            {"address":{"row":1,"column":0},"value":{"type":"number","value":42}},
            {"address":{"row":2,"column":0},"value":{"type":"boolean","value":true}},
            {"address":{"row":3,"column":0},"value":{"type":"date","value":"2026-09-24"}},
            {"address":{"row":4,"column":0},"value":{"type":"formula","value":{"expression":"SUM(A2:A3)","cached":{"type":"number","value":43}}}},
            {"address":{"row":5,"column":0},"value":{"type":"formula","value":{"expression":"A2>0","cached":{"type":"boolean","value":true}}}},
            {"address":{"row":6,"column":0},"value":{"type":"formula","value":{"expression":"\"caller cache\"","cached":{"type":"text","value":"caller cache"}}}},
            {"address":{"row":7,"column":0},"value":{"type":"formula","value":{"expression":"SUM(A2:A3)"}}},
            {"address":{"row":8,"column":0},"value":{"type":"text","value":"Link"},"hyperlink":"https://example.com"}
        ],
        "merges":[{"first":{"row":0,"column":0},"last":{"row":0,"column":2}}],
        "rows":[{"row":0,"height":30}],"columns":[{"column":0,"width":24}],
        "freeze":{"row":1,"column":1},"autofilter":{"first":{"row":0,"column":0},"last":{"row":9,"column":2}},
        "conditional_formats":rules.into_iter().map(|rule|json!({"range":range(),"rule":rule})).collect::<Vec<_>>(),
        "validations":validations,"charts":charts
    },{"name":"Second"}]})).unwrap()
}

#[test]
fn creates_all_value_types_rules_validations_and_editable_charts() {
    let bytes = generate(&workbook()).unwrap();
    let parts = read(&bytes).unwrap();
    let sheet = xml(&parts["xl/worksheets/sheet1.xml"]).unwrap();
    let cell = |address| {
        sheet
            .descendants()
            .find(|n| n.has_tag_name((S, "c")) && n.attribute("r") == Some(address))
            .unwrap()
    };
    assert_eq!(cell("A3").attribute("t"), Some("b"));
    assert_eq!(cell("A6").attribute("t"), Some("b"));
    assert_eq!(cell("A7").attribute("t"), Some("str"));
    assert!(!cell("A8").children().any(|n| n.has_tag_name((S, "v"))));
    assert_eq!(
        sheet
            .descendants()
            .filter(|n| n.has_tag_name((S, "cfRule")))
            .count(),
        5
    );
    assert_eq!(
        sheet
            .descendants()
            .filter(|n| n.has_tag_name((S, "dataValidation")))
            .count(),
        7
    );
    assert!(
        sheet
            .descendants()
            .any(|n| n.has_tag_name((S, "pane")) && n.attribute("state") == Some("frozen"))
    );
    assert!(
        sheet
            .descendants()
            .any(|n| n.has_tag_name((S, "mergeCell")) && n.attribute("ref") == Some("A1:C1"))
    );
    assert!(
        sheet
            .descendants()
            .any(|n| n.has_tag_name((S, "hyperlink")))
    );
    let workbook = xml(&parts["xl/workbook.xml"]).unwrap();
    assert!(
        workbook
            .descendants()
            .any(|n| n.has_tag_name((S, "calcPr")) && n.attribute("fullCalcOnLoad") == Some("1"))
    );
    assert_eq!(
        parts
            .keys()
            .filter(|p| p.starts_with("xl/charts/") && p.ends_with(".xml"))
            .count(),
        3
    );
    let styles = xml(&parts["xl/styles.xml"]).unwrap();
    assert_eq!(
        styles
            .descendants()
            .filter(|n| n.has_tag_name((S, "dxf")))
            .count(),
        2
    );
}

#[test]
fn validation_rejects_duplicate_sheets_overlapping_merges_and_invalid_rules() {
    let mut model = workbook();
    model.sheets[1].name = "DATA".into();
    assert!(generate(&model).is_err());
    let mut model = workbook();
    let merge = model.sheets[0].merges[0];
    model.sheets[0].merges.push(merge);
    assert!(generate(&model).is_err());
    let mut model = workbook();
    model.sheets[0].validations[0].formulas.clear();
    assert!(generate(&model).is_err());
    let mut model = workbook();
    model.sheets[0].cells[0].id = model.sheets[0].cells[1].id;
    assert!(generate(&model).is_err());
}

#[test]
fn tiny_models_cannot_expand_into_unbounded_merges_or_duplicate_dimensions() {
    use forge_tree_doc::ErrorCode;
    let mut model = workbook();
    model.sheets[0].merges = vec![Range {
        first: Address { row: 0, column: 0 },
        last: Address {
            row: 1_048_575,
            column: 16_383,
        },
    }];
    assert_eq!(generate(&model).unwrap_err().code, ErrorCode::ResourceLimit);
    let mut model = workbook();
    model.sheets[0].rows = vec![
        RowDimension {
            row: 0,
            height: 15.0,
        },
        RowDimension {
            row: 0,
            height: 30.0,
        },
    ];
    assert_eq!(
        generate(&model).unwrap_err().code,
        ErrorCode::DuplicateIdentity
    );
    let mut model = workbook();
    model.sheets[0].cells[0].value = Value::Formula(Formula {
        expression: "A2".into(),
        cached: Some(CachedValue::Text("x".repeat(32_768))),
    });
    assert_eq!(generate(&model).unwrap_err().code, ErrorCode::ResourceLimit);
}
