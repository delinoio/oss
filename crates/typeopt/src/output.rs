use std::{collections::BTreeMap, path::Path};

use anyhow::{Context, Result, ensure};
use serde::Serialize;

use crate::optimizer::{Edit, Mapping};

pub fn apply(source: &str, edits: &[Edit]) -> Result<String> {
    let mut text = String::new();
    let mut previous = 0;
    for edit in edits {
        ensure!(
            edit.start >= previous && edit.end >= edit.start && edit.end <= source.len(),
            "invalid or overlapping property edits"
        );
        text.push_str(&source[previous..edit.start]);
        text.push_str(&edit.replacement);
        previous = edit.end;
    }
    text.push_str(&source[previous..]);
    Ok(text)
}

fn line_starts(text: &str) -> Vec<usize> {
    let mut starts = vec![0];
    let mut chars = text.char_indices().peekable();
    while let Some((offset, ch)) = chars.next() {
        match ch {
            '\r' if chars.peek().is_some_and(|(_, next)| *next == '\n') => {
                let (offset, _) = chars.next().unwrap();
                starts.push(offset + 1);
            }
            '\r' | '\n' | '\u{2028}' | '\u{2029}' => starts.push(offset + ch.len_utf8()),
            _ => {}
        }
    }
    starts
}
fn byte_at_utf16(text: &str, starts: &[usize], line: u32, column: u32) -> usize {
    let start = starts.get(line as usize).copied().unwrap_or(text.len());
    let mut units = 0;
    for (offset, ch) in text[start..].char_indices() {
        if units >= column || matches!(ch, '\r' | '\n' | '\u{2028}' | '\u{2029}') {
            return start + offset;
        }
        units += ch.len_utf16() as u32;
    }
    text.len()
}
fn original_offset(offset: usize, edits: &[Edit]) -> usize {
    let mut delta: isize = 0;
    for edit in edits {
        let start = (edit.start as isize + delta) as usize;
        if offset < start {
            break;
        }
        if offset < start + edit.replacement.len() {
            return edit.start;
        }
        delta += edit.replacement.len() as isize - (edit.end - edit.start) as isize;
    }
    (offset as isize - delta) as usize
}
fn coordinate(text: &str, starts: &[usize], offset: usize) -> (u32, u32) {
    let line = starts.partition_point(|&start| start <= offset) - 1;
    (
        line as u32,
        text[starts[line]..offset].encode_utf16().count() as u32,
    )
}
fn compose(
    raw: &str,
    original: &str,
    name: &str,
    output_name: &str,
    edits: &[Edit],
) -> Result<Vec<u8>> {
    let source_map =
        sourcemap::SourceMap::from_slice(raw.as_bytes()).context("invalid compiler source map")?;
    let changed = apply(original, edits)?;
    let changed_starts = line_starts(&changed);
    let original_starts = line_starts(original);
    ensure!(
        source_map.get_source_count() <= 1,
        "expected a per-file compiler source map"
    );
    let mut builder = sourcemap::SourceMapBuilder::new(Some(output_name));
    let id = builder.add_source(name);
    builder.set_source_contents(id, Some(original));
    for token in source_map.tokens() {
        if token.has_source() {
            let byte = byte_at_utf16(
                &changed,
                &changed_starts,
                token.get_src_line(),
                token.get_src_col(),
            );
            let (line, col) = coordinate(original, &original_starts, original_offset(byte, edits));
            builder.add(
                token.get_dst_line(),
                token.get_dst_col(),
                line,
                col,
                Some(name),
                token.get_name(),
                false,
            );
        } else {
            builder.add(
                token.get_dst_line(),
                token.get_dst_col(),
                0,
                0,
                None,
                None,
                false,
            );
        }
    }
    let mut bytes = Vec::new();
    builder.into_sourcemap().to_writer(&mut bytes)?;
    Ok(bytes)
}

#[derive(Serialize)]
#[serde(rename_all = "snake_case")]
enum ModuleKind {
    Optimized,
    Preserved,
    Passthrough,
}
#[derive(Serialize)]
struct Module {
    source: String,
    /// SHA-256 of the analyzed source text, so consumers can reject output
    /// that no longer matches the current source.
    source_sha256: String,
    output: String,
    source_map: Option<String>,
    kind: ModuleKind,
}
#[derive(Serialize)]
struct Versioned<T> {
    schema_version: u8,
    #[serde(flatten)]
    data: T,
}

pub fn publish(
    destination: &Path,
    emitted: BTreeMap<String, (String, String)>,
    sources: &BTreeMap<String, String>,
    edits: &BTreeMap<String, Vec<Edit>>,
    mappings: &[Mapping],
    report: &mut crate::app::Report,
    started: std::time::Instant,
) -> Result<()> {
    let absolute = std::path::absolute(destination)?;
    let parent = absolute
        .parent()
        .context("output path has no parent")?
        .canonicalize()
        .context("output parent must exist")?;
    let destination = parent.join(
        absolute
            .file_name()
            .context("output path needs a directory name")?,
    );
    ensure!(
        !destination.try_exists()?,
        "output destination already exists"
    );
    let staging = tempfile::Builder::new()
        .prefix(".typeopt-")
        .tempdir_in(&parent)?;
    let mut modules = Vec::new();
    for (index, (name, original)) in sources.iter().enumerate() {
        crate::app::check_cancelled()?;
        let (code, raw_map) = match emitted.get(name) {
            Some(output) => output,
            None => {
                ensure!(
                    name.ends_with(".js")
                        || name.ends_with(".jsx")
                        || name.ends_with(".mjs")
                        || name.ends_with(".cjs"),
                    "compiler omitted a source module"
                );
                modules.push(Module {
                    source: name.clone(),
                    source_sha256: sha256_hex(original),
                    output: name.clone(),
                    source_map: None,
                    kind: ModuleKind::Passthrough,
                });
                continue;
            }
        };
        ensure!(!raw_map.is_empty(), "compiler omitted source map");
        let output = format!("{index}.js");
        let map_name = format!("{output}.map");
        let code = match code.rfind("//# sourceMappingURL=") {
            Some(start) => &code[..start],
            None => code,
        };
        std::fs::write(
            staging.path().join(&output),
            format!("{code}\n//# sourceMappingURL={map_name}\n"),
        )?;
        let map = compose(
            raw_map,
            original,
            name,
            &output,
            edits.get(name).map_or(&[], Vec::as_slice),
        )?;
        std::fs::write(staging.path().join(&map_name), map)?;
        modules.push(Module {
            source: name.clone(),
            source_sha256: sha256_hex(original),
            output,
            source_map: Some(map_name),
            kind: if edits.get(name).is_some_and(|edits| !edits.is_empty()) {
                ModuleKind::Optimized
            } else {
                ModuleKind::Preserved
            },
        });
    }
    write_json(
        &staging.path().join("modules.json"),
        &Versioned {
            schema_version: 0,
            data: serde_json::json!({"modules": modules}),
        },
    )?;
    write_json(
        &staging.path().join("properties.json"),
        &Versioned {
            schema_version: 0,
            data: serde_json::json!({"properties": mappings}),
        },
    )?;
    report.measure(started);
    write_json(&staging.path().join("report.json"), report)?;
    crate::app::check_cancelled()?;
    rename_exclusive(staging.path(), &destination)?;
    Ok(())
}
fn sha256_hex(text: &str) -> String {
    use sha2::{Digest, Sha256};
    Sha256::digest(text.as_bytes())
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
fn write_json(path: &Path, value: &impl Serialize) -> Result<()> {
    std::fs::write(path, serde_json::to_vec_pretty(value)?)?;
    Ok(())
}

fn rename_exclusive(from: &Path, to: &Path) -> Result<()> {
    use std::os::unix::ffi::OsStrExt;
    let from = std::ffi::CString::new(from.as_os_str().as_bytes())?;
    let to = std::ffi::CString::new(to.as_os_str().as_bytes())?;
    // SAFETY: both C strings remain alive and NUL-terminated for this syscall.
    // Exclusive rename closes the check/publish race without replacing any
    // path.
    #[cfg(target_os = "macos")]
    let result = unsafe { libc::renamex_np(from.as_ptr(), to.as_ptr(), libc::RENAME_EXCL) };
    #[cfg(target_os = "linux")]
    let result = unsafe {
        libc::renameat2(
            libc::AT_FDCWD,
            from.as_ptr(),
            libc::AT_FDCWD,
            to.as_ptr(),
            libc::RENAME_NOREPLACE,
        )
    };
    if result != 0 {
        return Err(std::io::Error::last_os_error())
            .context("cannot publish output without replacing destination");
    }
    Ok(())
}

pub fn peak_rss() -> u64 {
    let mut usage = std::mem::MaybeUninit::<libc::rusage>::uninit();
    // SAFETY: getrusage initializes the pointed-to structure on success.
    if unsafe { libc::getrusage(libc::RUSAGE_SELF, usage.as_mut_ptr()) } != 0 {
        return 0;
    }
    let bytes = unsafe { usage.assume_init() }.ru_maxrss as u64;
    #[cfg(target_os = "macos")]
    {
        bytes
    }
    #[cfg(target_os = "linux")]
    {
        bytes * 1024
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn unicode_coordinates_and_edit_boundaries() {
        let s = "😀longName + z\nlongName";
        let edits = vec![Edit {
            start: 4,
            end: 12,
            replacement: "a".into(),
        }];
        let transformed = apply(s, &edits).unwrap();
        assert_eq!(transformed, "😀a + z\nlongName");
        assert_eq!(
            byte_at_utf16(&transformed, &line_starts(&transformed), 0, 2),
            4
        );
        assert_eq!(
            coordinate(s, &line_starts(s), original_offset(8, &edits)),
            (0, 13)
        );
    }
    #[test]
    fn exclusive_publication_preserves_existing_directory() {
        let root = tempfile::tempdir().unwrap();
        let a = root.path().join("a");
        let b = root.path().join("b");
        std::fs::create_dir(&a).unwrap();
        std::fs::create_dir(&b).unwrap();
        assert!(rename_exclusive(&a, &b).is_err());
        assert!(a.is_dir());
        assert!(b.is_dir());
    }
}
