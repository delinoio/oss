use forge_document::{Chart, ChartKind, Series};
use forge_package::{C, S, read, xml};
use forge_xlsx::*;

#[test]
fn external_bar_line_and_pie_chart_edits_preserve_shared_original_data() {
    let original = import(include_bytes!("fixtures/external.xlsx")).unwrap();
    let charts: Vec<_> = original
        .targets
        .iter()
        .filter(|t| t.kind == TargetKind::Chart)
        .collect();
    assert_eq!(charts.len(), 3);
    for (target, kind) in charts
        .iter()
        .zip([ChartKind::Bar, ChartKind::Line, ChartKind::Pie])
    {
        let chart = Chart {
            kind,
            title: Some("Replacement".into()),
            categories: vec!["New A".into(), "New B".into()],
            series: vec![Series {
                name: "Replacement values".into(),
                values: vec![11.0, 19.0],
            }],
            legend: true,
            labels: true,
        };
        let output = replace(&original, &[(target.id, EditValue::Chart(chart))]).unwrap();
        let parts = read(&output).unwrap();
        assert_eq!(
            parts["xl/worksheets/sheet1.xml"],
            original.parts["xl/worksheets/sheet1.xml"]
        );
        assert_eq!(
            parts["xl/worksheets/sheet2.xml"],
            original.parts["xl/worksheets/sheet2.xml"]
        );
        for (other, _) in original
            .parts
            .iter()
            .filter(|(name, _)| name.starts_with("xl/charts/") && **name != target.region.part)
        {
            assert_eq!(parts[other], original.parts[other]);
        }
        let chart = xml(&parts[&target.region.part]).unwrap();
        let formulas: Vec<_> = chart
            .descendants()
            .filter(|n| n.has_tag_name((C, "f")))
            .filter_map(|n| n.text())
            .collect();
        assert!(!formulas.is_empty());
        assert!(formulas.iter().all(|f| f.starts_with("'Forge")));
        let workbook = xml(&parts[&original.main]).unwrap();
        assert_eq!(
            workbook
                .descendants()
                .filter(|n| n.has_tag_name((S, "sheet")))
                .count(),
            3
        );
        assert_eq!(
            import(&output)
                .unwrap()
                .targets
                .iter()
                .filter(|t| t.kind == TargetKind::Chart)
                .count(),
            3
        );
    }
}

#[test]
fn foreign_chart_elements_and_attributes_remain_opaque() {
    for fragment in [
        "<mc:AlternateContent xmlns:mc=\"http://schemas.openxmlformats.org/markup-compatibility/2006\"><mc:Fallback/></mc:AlternateContent>",
        "<custom xmlns=\"urn:vendor:chart\"/>",
        "<title xmlns:v=\"urn:vendor:chart\" v:flag=\"keep\"/>",
    ] {
        let mut parts = read(include_bytes!("fixtures/external.xlsx")).unwrap();
        let path = "xl/charts/chart1.xml";
        let chart = String::from_utf8(parts[path].clone()).unwrap();
        parts.insert(path.into(), chart.replace("</chartSpace>", &format!("{fragment}</chartSpace>")).into_bytes());
        let original = import(&forge_package::write(&parts).unwrap()).unwrap();
        let target = original.targets.iter().find(|t| t.region.part == path).unwrap();
        assert_eq!(target.kind, TargetKind::Opaque, "{fragment}");
        assert_eq!(original.targets.iter().filter(|t| t.kind == TargetKind::Chart).count(), 2);
        let chart = Chart { kind: ChartKind::Bar, title: None, categories: vec!["A".into()],
            series: vec![Series { name: "Values".into(), values: vec![1.0] }], legend: true, labels: false };
        assert_eq!(replace(&original, &[(target.id, EditValue::Chart(chart))]).unwrap_err().code,
            forge_tree_doc::ErrorCode::UnsupportedEdit);
        assert_eq!(read(&replace(&original, &[]).unwrap()).unwrap()[path], parts[path]);
    }
}
