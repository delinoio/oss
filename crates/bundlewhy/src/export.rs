//! Embedded UI document, Content Security Policy and self-contained HTML
//! export.

use regex::Regex;
use sha2::{Digest, Sha256};

use crate::model::{Analysis, ReportMode, RunStatus, SourceFile};

/// The built web UI (or a fallback page when the UI was not built).
pub const UI_HTML: &str = include_str!(concat!(env!("OUT_DIR"), "/bundlewhy-ui.html"));
pub const REPORT_MARKER: &str = "<!--bundlewhy:report-->";

fn base64(bytes: &[u8]) -> String {
    const TABLE: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(bytes.len().div_ceil(3) * 4);
    for chunk in bytes.chunks(3) {
        let value = (u32::from(chunk[0]) << 16)
            | (u32::from(*chunk.get(1).unwrap_or(&0)) << 8)
            | u32::from(*chunk.get(2).unwrap_or(&0));
        for index in 0..4 {
            if index <= chunk.len() {
                out.push(TABLE[((value >> (18 - index * 6)) & 63) as usize] as char);
            } else {
                out.push('=');
            }
        }
    }
    out
}

fn hashes(html: &str, tag: &str) -> Vec<String> {
    let pattern =
        Regex::new(&format!(r"(?s)<{tag}(\s[^>]*)?>(.*?)</{tag}>")).expect("static regex");
    pattern
        .captures_iter(html)
        .filter(|captures| {
            // JSON data blocks are not executed and need no script hash.
            !captures
                .get(1)
                .is_some_and(|attributes| attributes.as_str().contains("application/json"))
        })
        .map(|captures| {
            let body = captures.get(2).map_or("", |body| body.as_str());
            format!("'sha256-{}'", base64(&Sha256::digest(body.as_bytes())))
        })
        .collect()
}

/// CSP allowing only the document's own inline scripts and styles.
/// `connect_self` permits Connect requests to the loopback origin.
pub fn content_security_policy(html: &str, connect_self: bool) -> String {
    let scripts = hashes(html, "script");
    let styles = hashes(html, "style");
    format!(
        "default-src 'none'; script-src {}; style-src {}; img-src data:; font-src data:; \
         connect-src {}; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
        if scripts.is_empty() {
            "'none'".to_owned()
        } else {
            scripts.join(" ")
        },
        if styles.is_empty() {
            "'none'".to_owned()
        } else {
            styles.join(" ")
        },
        if connect_self { "'self'" } else { "'none'" },
    )
}

/// Escapes JSON for a `<script type="application/json">` element.
pub fn escape_script_json(json: &str) -> String {
    let mut out = String::with_capacity(json.len());
    for character in json.chars() {
        match character {
            '<' => out.push_str("\\u003c"),
            '>' => out.push_str("\\u003e"),
            '&' => out.push_str("\\u0026"),
            '\u{2028}' => out.push_str("\\u2028"),
            '\u{2029}' => out.push_str("\\u2029"),
            other => out.push(other),
        }
    }
    out
}

/// Renders a self-contained interactive report that loads no external
/// resources.
pub fn render(
    ui_html: &str,
    analysis: &Analysis,
    sources: &[SourceFile],
    include_sources: bool,
) -> String {
    let mut analysis = analysis.clone();
    analysis.mode = ReportMode::Exported;
    if analysis.status == RunStatus::Running {
        analysis.status = RunStatus::Partial;
    }
    let sources: Vec<SourceFile> = sources
        .iter()
        .cloned()
        .map(|mut source| {
            if !include_sources {
                source.content.clear();
                source.content_included = false;
            }
            source.stale = false;
            source
        })
        .collect();
    for summary in &mut analysis.sources {
        summary.content_included = include_sources && summary.content_included;
        summary.stale = false;
    }
    let payload = serde_json::json!({ "analysis": analysis, "sources": sources });
    let data = format!(
        "<script type=\"application/json\" id=\"bundlewhy-report\">{}</script>",
        escape_script_json(&payload.to_string())
    );
    // Compute the CSP from the UI document before the data block is inserted.
    let csp = content_security_policy(ui_html, false);
    let head = format!("<meta http-equiv=\"Content-Security-Policy\" content=\"{csp}\">{data}");
    if ui_html.contains(REPORT_MARKER) {
        ui_html.replacen(REPORT_MARKER, &head, 1)
    } else {
        ui_html.replacen("<head>", &format!("<head>{head}"), 1)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::Diagnostic;

    #[test]
    fn base64_matches_known_vectors() {
        assert_eq!(base64(b""), "");
        assert_eq!(base64(b"f"), "Zg==");
        assert_eq!(base64(b"fo"), "Zm8=");
        assert_eq!(base64(b"foo"), "Zm9v");
    }

    #[test]
    fn script_json_cannot_break_out_of_its_element() {
        let escaped = escape_script_json(r#"{"title":"</script><script>alert(1)</script>&"}"#);
        assert!(!escaped.contains("</script"));
        assert!(!escaped.contains('<') && !escaped.contains('>') && !escaped.contains('&'));
        let value: serde_json::Value = serde_json::from_str(&escaped).unwrap();
        assert_eq!(value["title"], "</script><script>alert(1)</script>&");
    }

    #[test]
    fn export_embeds_data_and_blocks_network_access() {
        let ui = "<html><head><!--bundlewhy:report--><style>b{}</style></\
                  head><body><script>run()</script></body></html>";
        let analysis = Analysis {
            schema_version: 1,
            status: RunStatus::Complete,
            diagnostics: vec![Diagnostic {
                title: "<img src=x onerror=alert(1)>".to_owned(),
                ..Diagnostic::default()
            }],
            ..Analysis::default()
        };
        let sources = vec![SourceFile {
            id: "src-0".to_owned(),
            content: "secret".to_owned(),
            content_included: true,
            ..SourceFile::default()
        }];
        let html = render(ui, &analysis, &sources, false);
        assert!(!html.contains(REPORT_MARKER));
        assert!(!html.contains("<img"));
        assert!(!html.contains("secret"));
        assert!(html.contains("REPORT_MODE_EXPORTED"));
        assert!(html.contains("connect-src 'none'"));
        assert!(html.contains("default-src 'none'"));
        let script_hash = format!("'sha256-{}'", base64(&Sha256::digest(b"run()")));
        assert!(html.contains(&script_hash));
        let with_sources = render(ui, &analysis, &sources, true);
        assert!(with_sources.contains("secret"));
    }
}
