//! Rules shared by both engines: duplicate packages and minifier blockers.

use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
};

use regex::Regex;

use super::{Context, DiagnosticDraft, RuleId};
use crate::{
    collect::CollectedModule,
    model::{Condition, ConditionOutcome, Evidence, EvidenceStatus},
};

pub fn run(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    duplicate_packages(context, drafts);
    unresolved_env_conditions(context, drafts);
    minification_disabled(context, drafts);
    direct_eval_mangling(context, drafts);
}

fn duplicate_packages(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    // package name -> package directory -> included modules
    let mut packages: BTreeMap<&str, BTreeMap<&str, Vec<&CollectedModule>>> = BTreeMap::new();
    for module in &collection.modules {
        if !module.included {
            continue;
        }
        if let (Some(name), Some(dir)) = (
            module.package_name.as_deref(),
            module.package_dir.as_deref(),
        ) {
            packages
                .entry(name)
                .or_default()
                .entry(dir)
                .or_default()
                .push(module);
        }
    }
    for (name, copies) in packages {
        if copies.len() < 2 {
            continue;
        }
        let descriptions: Vec<String> = copies
            .iter()
            .map(|(dir, modules)| {
                let version = modules[0].package_version.as_deref().unwrap_or("unknown");
                let display = context.display_path(Path::new(dir));
                format!(
                    "{version} at {} ({} module(s))",
                    if display.is_empty() {
                        ".".to_owned()
                    } else {
                        display
                    },
                    modules.len()
                )
            })
            .collect();
        let first_modules: Vec<&CollectedModule> =
            copies.values().map(|modules| modules[0]).collect();
        let second = first_modules[1];
        let located = context.module_location(second, 0, 0);
        let mut dependency_paths = Vec::new();
        for module in &first_modules {
            dependency_paths.extend(context.dependency_paths(&module.id).into_iter().take(1));
        }
        let display = context.module_display(second);
        drafts.push(DiagnosticDraft {
            rule: RuleId::DuplicatePackage,
            status: EvidenceStatus::Confirmed,
            title: format!("{name} is bundled {} times", copies.len()),
            summary: format!(
                "Modules from {} different copies of {name} are included in the build. Each copy \
                 is shipped and evaluated separately.",
                copies.len()
            ),
            conditions: vec![Condition::new(
                "One copy of the package is bundled",
                ConditionOutcome::Unmet,
                descriptions.join("; "),
            )],
            located,
            module: display,
            dependency_paths,
            evidence: descriptions
                .iter()
                .map(|description| {
                    Evidence::new(
                        "module paths and nearest package.json",
                        EvidenceStatus::Confirmed,
                        description.clone(),
                    )
                })
                .collect(),
            direction: format!(
                "Align the version ranges that depend on {name} and deduplicate the lockfile (for \
                 example pnpm dedupe, npm dedupe, or overrides/resolutions), or alias the package \
                 to a single copy."
            ),
            safety: "Only merge copies whose versions are API compatible; run the application's \
                     tests after deduplicating."
                .to_owned(),
            dependency: true,
        });
    }
}

/// Byte ranges of string literals, template literal text, comments and regular
/// expression literals in JavaScript. Text there is never a property read.
///
/// This is a lexical scan, not a parser. Regular expression literals are
/// recognized by the preceding token (an operator, punctuation or keyword such
/// as `return`), which matches minified bundler output.
fn non_code_ranges(code: &str) -> Vec<(usize, usize)> {
    let bytes = code.as_bytes();
    let mut ranges = Vec::new();
    // Open `${` substitutions; each entry is the brace depth inside it.
    let mut templates: Vec<usize> = Vec::new();
    let mut previous_significant: Option<u8> = None;
    let mut index = 0usize;
    let regex_allowed = |previous: Option<u8>, start: usize| -> bool {
        match previous {
            None => true,
            Some(byte) if b"(,=:[!&|?{};+-*%<>~^".contains(&byte) => true,
            Some(byte) if byte.is_ascii_alphabetic() => {
                let head = &code[..start];
                let trimmed = head.trim_end();
                [
                    "return", "typeof", "case", "do", "else", "in", "of", "void", "yield",
                    "delete", "throw", "new",
                ]
                .iter()
                .any(|keyword| {
                    trimmed.ends_with(keyword)
                        && !trimmed[..trimmed.len() - keyword.len()].ends_with(|character: char| {
                            character.is_alphanumeric() || character == '_' || character == '$'
                        })
                })
            }
            _ => false,
        }
    };
    // Scans a quoted string or template text starting after the opener.
    let scan_until = |mut position: usize, terminator: u8, template: bool| -> (usize, bool) {
        while position < bytes.len() {
            match bytes[position] {
                b'\\' => position += 2,
                byte if byte == terminator => return (position + 1, false),
                b'$' if template && bytes.get(position + 1) == Some(&b'{') => {
                    return (position + 2, true);
                }
                b'\n' if !template => return (position, false),
                _ => position += 1,
            }
        }
        (bytes.len(), false)
    };
    while index < bytes.len() {
        let byte = bytes[index];
        match byte {
            b'\'' | b'"' => {
                let (end, _) = scan_until(index + 1, byte, false);
                ranges.push((index, end));
                index = end;
                previous_significant = Some(b'"');
            }
            b'`' => {
                let (end, opened) = scan_until(index + 1, b'`', true);
                ranges.push((index, end));
                if opened {
                    templates.push(0);
                }
                index = end;
                previous_significant = Some(b'"');
            }
            b'}' if templates.last() == Some(&0) => {
                templates.pop();
                let (end, opened) = scan_until(index + 1, b'`', true);
                ranges.push((index, end));
                if opened {
                    templates.push(0);
                }
                index = end;
                previous_significant = Some(b'"');
            }
            b'/' if bytes.get(index + 1) == Some(&b'/') => {
                let end = code[index..]
                    .find('\n')
                    .map_or(bytes.len(), |offset| index + offset);
                ranges.push((index, end));
                index = end;
            }
            b'/' if bytes.get(index + 1) == Some(&b'*') => {
                let end = code[index + 2..]
                    .find("*/")
                    .map_or(bytes.len(), |offset| index + 2 + offset + 2);
                ranges.push((index, end));
                index = end;
            }
            b'/' if regex_allowed(previous_significant, index) => {
                let mut position = index + 1;
                let mut in_class = false;
                while position < bytes.len() {
                    match bytes[position] {
                        b'\\' => position += 1,
                        b'[' => in_class = true,
                        b']' => in_class = false,
                        b'/' if !in_class => break,
                        b'\n' => break,
                        _ => {}
                    }
                    position += 1;
                }
                let end = (position + 1).min(bytes.len());
                ranges.push((index, end));
                index = end;
                previous_significant = Some(b'"');
            }
            _ => {
                if let Some(depth) = templates.last_mut() {
                    if byte == b'{' {
                        *depth += 1;
                    } else if byte == b'}' {
                        *depth -= 1;
                    }
                }
                if !byte.is_ascii_whitespace() {
                    previous_significant = Some(byte);
                }
                index += 1;
            }
        }
    }
    ranges
}

fn in_ranges(ranges: &[(usize, usize)], offset: usize) -> bool {
    let index = ranges.partition_point(|(start, _)| *start <= offset);
    index > 0 && offset < ranges[index - 1].1
}

fn unresolved_env_conditions(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let vite = context.collection.bundler == "vite";
    // Vite replaces `process.env` with `{}` in client builds; Rspack leaves
    // unknown `process.env.X` reads untouched.
    let pattern = if vite {
        Regex::new(r"\(\{\}\)\.([A-Za-z_$][\w$]*)")
    } else {
        Regex::new(r"\bprocess\.env\.([A-Za-z_$][\w$]*)")
    }
    .expect("static regex");
    let mut seen: BTreeSet<(String, String)> = BTreeSet::new();
    for index in 0..context.chunks.len() {
        let ignored = non_code_ranges(&context.chunks[index].code);
        let matches: Vec<(usize, String)> = pattern
            .captures_iter(&context.chunks[index].code)
            .filter_map(|captures| {
                let whole = captures.get(0)?;
                // Text inside strings, templates, comments or regular
                // expressions is not a runtime read.
                if in_ranges(&ignored, whole.start()) {
                    return None;
                }
                // Vite's `(` belongs to the preceding mapping segment; the
                // `{}` replacement maps to the original `process.env` read.
                let offset = if vite {
                    whole.start() + 1
                } else {
                    whole.start()
                };
                Some((offset, captures[1].to_owned()))
            })
            .collect();
        for (offset, name) in matches {
            let located = context.chunk_location(index, offset);
            let mapped = located.generated.is_some();
            tracing::debug!(
                chunk = %context.chunks[index].file,
                offset,
                name = %name,
                mapped,
                location = ?located.location.as_ref().map(|location| (&location.path, location.line)),
                "unresolved env candidate"
            );
            if vite {
                // `({}).X` is only attributable to process.env when the
                // original source at the mapped line reads process.env.X.
                let Some(snippet) = located.snippet.as_ref() else {
                    continue;
                };
                if !mapped || !snippet.text.contains(&format!("process.env.{name}")) {
                    continue;
                }
            }
            let path = located
                .location
                .as_ref()
                .map(|location| location.path.clone())
                .unwrap_or_default();
            if !seen.insert((path.clone(), name.clone())) {
                continue;
            }
            let chunk_file = context.chunks[index].file.clone();
            let dependency = path_is_dependency(&path);
            let generated_text = if vite {
                format!("({{}}).{name}")
            } else {
                format!("process.env.{name}")
            };
            drafts.push(DiagnosticDraft {
                rule: RuleId::UnresolvedEnvCondition,
                status: EvidenceStatus::Inferred,
                title: format!("process.env.{name} is not a compile-time constant"),
                summary: format!(
                    "The generated code still reads {generated_text} at runtime, so the minifier \
                     cannot evaluate conditions that depend on it and cannot remove the branches \
                     they guard."
                ),
                conditions: vec![
                    Condition::new(
                        &format!("process.env.{name} is replaced with a constant at build time"),
                        ConditionOutcome::Unmet,
                        format!("{generated_text} remains in {chunk_file}"),
                    ),
                    Condition::new(
                        "The guarded branch can be evaluated and removed",
                        ConditionOutcome::Unknown,
                        "Depends on whether the value is constant for the deployed build",
                    ),
                ],
                located,
                module: path,
                dependency_paths: Vec::new(),
                evidence: vec![
                    Evidence::new(
                        "generated chunk text",
                        EvidenceStatus::Confirmed,
                        format!("{generated_text} found in {chunk_file}"),
                    ),
                    Evidence::new(
                        "bundlewhy analysis",
                        EvidenceStatus::Inferred,
                        if mapped {
                            "Mapped to the original source through the chunk source map. The \
                             engine does not report why the condition was kept; bundlewhy infers \
                             that the unresolved read blocks dead-code elimination."
                        } else {
                            "No source mapping is available for this position. The engine does not \
                             report why the condition was kept; bundlewhy infers that the \
                             unresolved read blocks dead-code elimination."
                        },
                    ),
                ],
                direction: if vite {
                    format!(
                        "Provide the value at build time with Vite `define` (for example define: \
                         {{ 'process.env.{name}': JSON.stringify(value) }}) or read an \
                         import.meta.env variable instead."
                    )
                } else {
                    format!(
                        "Provide the value at build time with DefinePlugin (for example new \
                         rspack.DefinePlugin({{ 'process.env.{name}': JSON.stringify(value) }}))."
                    )
                },
                safety: "Only define values that are constant for the deployed build. Defined \
                         values are inlined into the client bundle, so never define secrets."
                    .to_owned(),
                dependency,
            });
        }
    }
}

fn path_is_dependency(path: &str) -> bool {
    path.starts_with("node_modules/") || path.starts_with("../")
}

fn minification_disabled(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    if context.collection.minify_enabled {
        return;
    }
    let vite = context.collection.bundler == "vite";
    let option = if vite {
        "build.minify"
    } else {
        "optimization.minimize"
    };
    let value = context
        .collection
        .config_summary
        .get(option)
        .cloned()
        .unwrap_or_else(|| "false".to_owned());
    let config_file = context.collection.config_file.clone();
    let located = match &config_file {
        Some(file) => context.original_location(Path::new(file), 0, 0),
        None => super::Located {
            location: None,
            generated: None,
            snippet: None,
        },
    };
    drafts.push(DiagnosticDraft {
        rule: RuleId::MinificationDisabled,
        status: EvidenceStatus::Confirmed,
        title: "JavaScript minification is disabled".to_owned(),
        summary: format!(
            "{option} is {value}, so dead-code elimination, name mangling and other minifier \
             optimizations do not run."
        ),
        conditions: vec![Condition::new(
            "Minification is enabled",
            ConditionOutcome::Unmet,
            format!("{option}: {value}"),
        )],
        located,
        module: config_file
            .as_deref()
            .map(|file| context.display_path(Path::new(file)))
            .unwrap_or_default(),
        dependency_paths: Vec::new(),
        evidence: vec![Evidence::new(
            "resolved bundler configuration",
            EvidenceStatus::Confirmed,
            format!("{option}: {value}"),
        )],
        direction: format!("Enable {option} for production builds."),
        safety: "Minified output changes function and class names; code that depends on \
                 Function.prototype.name needs keep-names options."
            .to_owned(),
        dependency: false,
    });
}

/// Every binding name of at least four characters introduced by `const`,
/// `let` or `var` declarations, including comma-separated declarators and
/// destructuring patterns. Function declaration names are excluded: bundlers
/// may keep top-level function names for reasons unrelated to eval. Text in
/// strings, templates, comments and regular expressions is masked first.
fn declared_names(source: &str) -> Vec<String> {
    let mut masked = source.as_bytes().to_vec();
    for (start, end) in non_code_ranges(source) {
        for byte in &mut masked[start..end.min(source.len())] {
            if *byte != b'\n' {
                *byte = b' ';
            }
        }
    }
    let code = String::from_utf8_lossy(&masked).into_owned();
    let keyword = Regex::new(r"\b(?:const|let|var)\s+").expect("static regex");
    let mut names = Vec::new();
    for found in keyword.find_iter(&code) {
        let declaration = declaration_text(&code[found.end()..]);
        for declarator in split_top_level(declaration, b',') {
            let target = binding_target(declarator);
            collect_binding_names(target, &mut names);
        }
    }
    names.retain(|name| name.len() >= 4);
    names.sort();
    names.dedup();
    names
}

/// Text of one declaration: up to a top-level `;`, or a top-level line break
/// that does not continue the declaration.
fn declaration_text(rest: &str) -> &str {
    let bytes = rest.as_bytes();
    let mut depth = 0i32;
    let mut last_significant = 0u8;
    for (index, byte) in bytes.iter().enumerate() {
        match byte {
            b'(' | b'[' | b'{' => depth += 1,
            b')' | b']' | b'}' => {
                depth -= 1;
                if depth < 0 {
                    return &rest[..index];
                }
            }
            b';' if depth == 0 => return &rest[..index],
            b'\n' if depth == 0 && !matches!(last_significant, b',' | b'=' | 0) => {
                return &rest[..index];
            }
            _ => {}
        }
        if !byte.is_ascii_whitespace() {
            last_significant = *byte;
        }
    }
    rest
}

fn split_top_level(text: &str, separator: u8) -> Vec<&str> {
    let mut parts = Vec::new();
    let mut depth = 0i32;
    let mut start = 0usize;
    for (index, byte) in text.bytes().enumerate() {
        match byte {
            b'(' | b'[' | b'{' => depth += 1,
            b')' | b']' | b'}' => depth -= 1,
            _ if byte == separator && depth == 0 => {
                parts.push(&text[start..index]);
                start = index + 1;
            }
            _ => {}
        }
    }
    parts.push(&text[start..]);
    parts
}

/// The binding part of a declarator (before its top-level `=` initializer).
fn binding_target(declarator: &str) -> &str {
    let mut depth = 0i32;
    let bytes = declarator.as_bytes();
    for (index, byte) in bytes.iter().enumerate() {
        match byte {
            b'(' | b'[' | b'{' => depth += 1,
            b')' | b']' | b'}' => depth -= 1,
            b'=' if depth == 0
                && bytes.get(index + 1) != Some(&b'=')
                && bytes.get(index + 1) != Some(&b'>') =>
            {
                return &declarator[..index];
            }
            _ => {}
        }
    }
    declarator
}

/// Collects identifiers bound by a pattern: plain names, array elements,
/// object shorthand and `key: binding` values, rest elements and nested
/// patterns. Defaults after `=` and object keys are not bindings.
fn collect_binding_names(pattern: &str, names: &mut Vec<String>) {
    let pattern = pattern.trim();
    let inner = if (pattern.starts_with('{') && pattern.ends_with('}'))
        || (pattern.starts_with('[') && pattern.ends_with(']'))
    {
        Some(&pattern[1..pattern.len() - 1])
    } else {
        None
    };
    let Some(inner) = inner else {
        let name = pattern.trim_start_matches("...").trim();
        if !name.is_empty()
            && name
                .chars()
                .next()
                .is_some_and(|first| first.is_alphabetic() || first == '_' || first == '$')
            && name.chars().all(|character| {
                character.is_alphanumeric() || character == '_' || character == '$'
            })
        {
            names.push(name.to_owned());
        }
        return;
    };
    let object = pattern.starts_with('{');
    for element in split_top_level(inner, b',') {
        let element = binding_target(element);
        let value = if object {
            split_top_level(element, b':')
                .last()
                .copied()
                .unwrap_or(element)
        } else {
            element
        };
        collect_binding_names(value, names);
    }
}

fn direct_eval_line(source: &str) -> Option<(u32, u32)> {
    let pattern = Regex::new(r"(^|[^.\w$])eval\s*\(").expect("static regex");
    // `eval(` inside strings, templates, comments or regular expressions is
    // not a call.
    let ignored = non_code_ranges(source);
    let start = pattern.find_iter(source).find_map(|found| {
        let start = found.start() + found.as_str().find("eval")?;
        (!in_ranges(&ignored, start)).then_some(start)
    })?;
    let line = source[..start].matches('\n').count();
    let column = start - source[..start].rfind('\n').map_or(0, |index| index + 1);
    Some((u32::try_from(line).ok()?, u32::try_from(column).ok()?))
}

fn direct_eval_mangling(context: &mut Context<'_>, drafts: &mut Vec<DiagnosticDraft>) {
    let collection = context.collection;
    if !collection.minify_enabled || !collection.default_minifier {
        return;
    }
    for module in &collection.modules {
        if !module.included || module.path.is_none() {
            continue;
        }
        let rolldown_log = collection.logs.iter().find(|log| {
            log.code.as_deref() == Some("EVAL") && log.id.as_deref() == Some(module.id.as_str())
        });
        let rspack_bailout = module
            .bailouts
            .iter()
            .find(|entry| entry.contains("Module uses eval()"));
        let Some(source) = context.module_source(module) else {
            continue;
        };
        let source_eval = direct_eval_line(&source);
        if rolldown_log.is_none() && rspack_bailout.is_none() && source_eval.is_none() {
            continue;
        }
        let names = declared_names(&source);
        let mut retained: BTreeSet<String> = BTreeSet::new();
        let mut chunk_files = Vec::new();
        for file in &module.chunks {
            let Some(index) = context.chunk_index(file) else {
                continue;
            };
            chunk_files.push(file.clone());
            for name in &names {
                let pattern = Regex::new(&format!(r"(^|[^\w$]){}([^\w$]|$)", regex::escape(name)))
                    .expect("escaped regex");
                if pattern.is_match(&context.chunks[index].code) {
                    retained.insert(name.clone());
                }
            }
        }
        if retained.is_empty() {
            // Names were mangled despite eval, so nothing is blocked.
            continue;
        }
        let engine_reported = rolldown_log.is_some() || rspack_bailout.is_some();
        let (line, column) = rolldown_log
            .and_then(|log| Some((log.line?.saturating_sub(1), log.column.unwrap_or(0))))
            .or(source_eval)
            .unwrap_or((0, 0));
        let display = context.module_display(module);
        let located = context.module_location(module, line, column);
        let retained_list: Vec<String> = retained.into_iter().take(12).collect();
        let mut evidence = Vec::new();
        if let Some(log) = rolldown_log {
            evidence.push(Evidence::new(
                "rolldown log EVAL",
                EvidenceStatus::Confirmed,
                log.message.lines().next().unwrap_or_default().to_owned(),
            ));
        }
        if let Some(bailout) = rspack_bailout {
            evidence.push(Evidence::new(
                "rspack stats (optimizationBailout)",
                EvidenceStatus::Confirmed,
                bailout.clone(),
            ));
        }
        if !engine_reported {
            evidence.push(Evidence::new(
                "bundlewhy source scan",
                EvidenceStatus::Inferred,
                "Direct eval found in the source; the engine did not report it.",
            ));
        }
        evidence.push(Evidence::new(
            "generated chunk text",
            EvidenceStatus::Confirmed,
            format!(
                "Declared names still present in {}: {}",
                chunk_files.join(", "),
                retained_list.join(", ")
            ),
        ));
        drafts.push(DiagnosticDraft {
            rule: RuleId::DirectEvalBlocksMangling,
            status: if engine_reported {
                EvidenceStatus::Confirmed
            } else {
                EvidenceStatus::Inferred
            },
            title: format!("Direct eval in {display} keeps names unmangled"),
            summary: "Direct eval can read any variable in scope by name, so the minifier keeps \
                      the names of every binding the eval call can reach."
                .to_owned(),
            conditions: vec![
                Condition::new(
                    "No direct eval can observe local names",
                    ConditionOutcome::Unmet,
                    if engine_reported {
                        "Direct eval reported by the engine"
                    } else {
                        "Direct eval found in the source"
                    },
                ),
                Condition::new(
                    "Declared names are shortened in the output",
                    ConditionOutcome::Unmet,
                    format!("Unmangled: {}", retained_list.join(", ")),
                ),
            ],
            located,
            module: display,
            dependency_paths: context.dependency_paths(&module.id),
            evidence,
            direction: "Replace direct eval with indirect eval ((0, eval)(code)) or new \
                        Function(...), which run in global scope and let the minifier mangle \
                        local names."
                .to_owned(),
            safety: "Indirect eval and new Function cannot read local variables; pass required \
                     values explicitly. Evaluating untrusted strings remains unsafe either way."
                .to_owned(),
            dependency: context.is_dependency(module),
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn non_code_ranges_cover_strings_comments_templates_and_regexes() {
        let code = r#"a("process.env.A");/* process.env.B */x=`process.env.C ${process.env.D} t`;// process.env.E
y=/process.env.F/.test(z),process.env.G"#;
        let ranges = non_code_ranges(code);
        let ignored =
            |name: &str| in_ranges(&ranges, code.find(&format!("process.env.{name}")).unwrap());
        for name in ["A", "B", "C", "E", "F"] {
            assert!(ignored(name), "{name} should be ignored");
        }
        for name in ["D", "G"] {
            assert!(!ignored(name), "{name} is code");
        }
        // Division is not a regular expression.
        let division = "a=b/2,process.env.H/3";
        assert!(!in_ranges(
            &non_code_ranges(division),
            division.find("process").unwrap()
        ));
    }

    #[test]
    fn finds_direct_eval_but_not_member_eval() {
        assert_eq!(
            direct_eval_line("const a = 1;\n  return eval(x);"),
            Some((1, 9))
        );
        assert_eq!(direct_eval_line("obj.eval(x); evaluate(y);"), None);
        assert_eq!(
            direct_eval_line(
                "const a = \"eval(x)\"; // eval(y)\n/* eval(z) */ b = `eval(t)`; c = /eval(r)/;"
            ),
            None
        );
    }

    #[test]
    fn collects_declared_names_of_four_or_more_characters() {
        let names =
            declared_names("const ab = 1; let local = 2; var other = 3; function helper() {}");
        assert_eq!(names, vec!["local".to_owned(), "other".to_owned()]);
        let names = declared_names(
            "const real = 1; const text = \"const retainedLiteralName\"; // let commented",
        );
        assert_eq!(names, vec!["real".to_owned(), "text".to_owned()]);
        let names = declared_names(
            "const x = f(1, 2), descriptiveName = 1;\nlet { alpha, beta: renamedBeta = 3, \
             ...restOfIt } = o, [first, , second] = a;",
        );
        assert_eq!(
            names,
            [
                "alpha",
                "descriptiveName",
                "first",
                "renamedBeta",
                "restOfIt",
                "second"
            ]
            .map(str::to_owned)
            .to_vec()
        );
    }
}
