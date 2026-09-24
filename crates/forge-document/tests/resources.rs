use forge_document::{Chart, ChartKind, Series};
use forge_tree_doc::ErrorCode;

#[test]
fn chart_cross_product_is_bounded_before_workbook_expansion() {
    let chart = Chart {
        kind: ChartKind::Bar,
        title: None,
        categories: vec!["A".into(); 20_000],
        series: vec![
            Series {
                name: "Series".into(),
                values: vec![1.0; 20_000]
            };
            11
        ],
        legend: true,
        labels: false,
    };
    assert_eq!(
        chart.office_parts().unwrap_err().code,
        ErrorCode::ResourceLimit
    );
}

#[test]
fn image_dimensions_are_bounded_before_pixel_decode() {
    let mut image = include_bytes!("../../forge-pptx/tests/fixtures/sample.png").to_vec();
    image[16..20].copy_from_slice(&8001_u32.to_be_bytes());
    image[20..24].copy_from_slice(&8000_u32.to_be_bytes());
    // Recompute the IHDR checksum so this exercises the pixel budget, rather
    // than merely failing corrupt-input validation.
    let mut crc = !0_u32;
    for value in &image[12..29] {
        crc ^= u32::from(*value);
        for _ in 0..8 {
            crc = (crc >> 1) ^ (0xedb88320 & (0_u32.wrapping_sub(crc & 1)));
        }
    }
    image[29..33].copy_from_slice(&(!crc).to_be_bytes());
    assert_eq!(
        forge_document::image(&image).unwrap_err().code,
        ErrorCode::ResourceLimit
    );
}

#[test]
fn image_byte_budget_rejects_before_decoding() {
    assert_eq!(
        forge_document::image(&vec![0; 64 * 1024 * 1024 + 1])
            .unwrap_err()
            .code,
        ErrorCode::ResourceLimit
    );
}
