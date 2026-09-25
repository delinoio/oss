use std::{
    fs,
    io::Write,
    path::Path,
    process::{Command, Output, Stdio},
};

fn command(dir: &Path) -> Command {
    let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
    command
        .current_dir(dir)
        .env_remove("RUST_LOG")
        .env("NO_COLOR", "1");
    command
}
fn run(dir: &Path, args: &[&str], input: &[u8]) -> Output {
    let mut child = command(dir)
        .args(args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    // Rejected CLI arguments can close the pipe before the fixture is written.
    let _ = child.stdin.take().unwrap().write_all(input);
    child.wait_with_output().unwrap()
}
fn good(args: &[&str], input: &str, expected: &str) {
    let dir = tempfile::tempdir().unwrap();
    let result = run(dir.path(), args, input.as_bytes());
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, expected.as_bytes());
    assert!(result.stderr.is_empty());
}
fn yaml(input: &str, expected: &str) {
    good(&["yaml", "normalize"], input, expected);
    good(&["yaml", "normalize", "--input", "-"], expected, expected);
}
fn no_temps(dir: &Path) {
    assert!(!fs::read_dir(dir).unwrap().any(|entry| entry
        .unwrap()
        .file_name()
        .to_string_lossy()
        .starts_with(".clibox-")));
}

#[test]
fn dotenv_tokens_order_overrides_and_defaults() {
    let dir = tempfile::tempdir().unwrap();
    fs::write(
        dir.path().join(".env"),
        "z=secret\nA=first\na=case\nA=last\n",
    )
    .unwrap();
    let result = run(dir.path(), &["dotenv", "list"], b"ignored invalid stdin");
    assert_eq!(result.stdout, b"A\na\nz\n");
    assert!(result.status.success());
    fs::write(
        dir.path().join("base"),
        "\u{feff}# comment\r\n export Z = old\r\nA = \"literal ${HOME} $(touch no) \
         `cmd`\\n\r\nnext\" # comment\r\nB=base\r\n",
    )
    .unwrap();
    fs::write(
        dir.path().join("local"),
        "Z=\nB=' last # value '\nA=\"literal ${HOME} $(touch no) `cmd`\\n\r\nnext\"\n",
    )
    .unwrap();
    let expected = b"A=\"literal ${HOME} $(touch no) `cmd`\\n\r\nnext\"\nB=' last # value '\nZ=\n";
    let result = run(
        dir.path(),
        &["dotenv", "merge", "base", "-", "local"],
        b"B=stdin\nZ=stdin",
    );
    assert!(result.status.success(), "{:?}", result.stderr);
    assert_eq!(result.stdout, expected);
    assert!(!dir.path().join("no").exists());
    good(
        &["dotenv", "merge", "-"],
        "export=ok\nexport A=1\n_0=2\na=a\nA=3 #trim\n",
        "A=3\n_0=2\na=a\nexport=ok\n",
    );
    good(
        &["dotenv", "list", "--input", "-"],
        "KEY=\"secret\nmore secret\"",
        "KEY\n",
    );
}

#[test]
fn dotenv_export_key_is_distinct_from_the_optional_prefix() {
    for (input, key, token) in [
        ("export =enabled\n", "export", "enabled"),
        (
            " export\t = 'literal ${HOME}' # comment\r\n",
            "export",
            "'literal ${HOME}'",
        ),
        ("export \t=", "export", ""),
        ("export\t=enabled\n", "export", "enabled"),
        ("export export =nested\n", "export", "nested"),
        ("export \tKEY =enabled\n", "KEY", "enabled"),
        ("\tKEY\t=\tenabled\n", "KEY", "enabled"),
        ("export_KEY =enabled\n", "export_KEY", "enabled"),
    ] {
        good(
            &["dotenv", "list", "--input", "-"],
            input,
            &format!("{key}\n"),
        );
        good(
            &["dotenv", "merge", "-"],
            input,
            &format!("{key}={token}\n"),
        );
    }
    good(
        &["dotenv", "merge", "-"],
        "export =first\nexport export=second\nexport\t=\n",
        "export=\n",
    );
}

#[test]
fn dotenv_export_prefix_requires_an_ascii_space() {
    let dir = tempfile::tempdir().unwrap();
    for separator in ["\t", "\t ", "\t\t"] {
        let input = format!("SAFE=before\nexport{separator}SECRET_KEY=secret\nSECRET_KEY=after\n");
        for args in [
            vec!["dotenv", "list", "--input", "-"],
            vec!["dotenv", "merge", "-"],
        ] {
            let result = run(dir.path(), &args, input.as_bytes());
            assert_eq!(result.status.code(), Some(1), "{separator:?} {args:?}");
            assert!(result.stdout.is_empty());
            let stderr = String::from_utf8(result.stderr).unwrap();
            assert!(stderr.contains("DotenvSyntax"));
            assert!(stderr.contains("line=2"));
            assert!(!stderr.contains("SECRET_KEY"));
            assert!(!stderr.contains("secret"));
        }
    }
}

#[test]
fn explicit_files_do_not_consume_stdin_or_discover_parents() {
    let dir = tempfile::tempdir().unwrap();
    fs::write(dir.path().join(".env"), "A=1").unwrap();
    fs::write(dir.path().join("config"), "z: 1\na: 2").unwrap();
    fs::create_dir(dir.path().join("child")).unwrap();
    assert_eq!(
        run(&dir.path().join("child"), &["dotenv", "list"], b"")
            .status
            .code(),
        Some(1)
    );
    for args in [
        vec!["dotenv", "list"],
        vec!["yaml", "normalize", "--input", "config"],
    ] {
        let mut child = command(dir.path())
            .args(args)
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        // Keep the stdin writer open. Completion must not wait for EOF.
        wait(&mut child);
        assert!(child.wait().unwrap().success());
    }
}

#[test]
fn cli_conflicts_are_redacted_usage_errors() {
    let dir = tempfile::tempdir().unwrap();
    for args in [
        vec!["dotenv", "merge"],
        vec!["dotenv", "merge", "-", "-"],
        vec!["dotenv", "list", "--force"],
        vec!["dotenv", "merge", "-", "--force"],
        vec!["yaml", "normalize", "--force"],
        vec!["yaml", "normalize", "--in-place"],
        vec!["yaml", "normalize", "--input", "-", "--in-place"],
        vec![
            "yaml",
            "normalize",
            "--input",
            "secret-marker",
            "--output",
            "secret-marker",
            "--in-place",
        ],
        vec!["dotenv", "list", "--input"],
        vec!["secret-marker"],
        vec!["yaml", "normalize", "--secret-marker"],
    ] {
        let result = run(dir.path(), &args, b"");
        assert_eq!(result.status.code(), Some(2), "{args:?}");
        assert!(result.stdout.is_empty());
        let stderr = String::from_utf8(result.stderr).unwrap();
        assert!(stderr.contains("Arguments"));
        assert!(!stderr.contains("secret-marker"));
    }
}

#[test]
fn encoding_empty_streams_and_record_boundaries() {
    for args in [
        vec!["dotenv", "list", "--input", "-"],
        vec!["dotenv", "merge", "-"],
        vec!["yaml", "normalize"],
    ] {
        for empty in ["", "\u{feff}", "# comment\r\n  # comment\n", " \n\t\r\n"] {
            good(&args, empty, "");
        }
        let dir = tempfile::tempdir().unwrap();
        for input in [b"A=\xff".as_slice(), b"A=\0"] {
            let result = run(dir.path(), &args, input);
            assert_eq!(result.status.code(), Some(1));
            assert!(result.stdout.is_empty());
            assert!(String::from_utf8_lossy(&result.stderr).contains("Encoding"));
        }
    }
    good(
        &["dotenv", "merge", "-"],
        "\u{feff}A=한글\r\nB='x\r\ny'",
        "A=한글\nB='x\r\ny'\n",
    );
    yaml("\u{feff}key: 한글\r\n", "\"key\": \"한글\"\n");
}

#[test]
fn dotenv_invalid_records_never_emit_even_when_overwritten() {
    let dir = tempfile::tempdir().unwrap();
    for input in [
        "BAD-KEY=secret",
        "9KEY=secret",
        "한글=secret",
        "KEY",
        "export KEY",
        "export # missing assignment",
        "export export KEY=secret",
        "export BAD-KEY=secret",
        "A='secret",
        "A=\"secret",
        "A='secret' trailing",
        "A='secret' OTHER=ok",
        "A=good\nA='bad' junk\nA=good",
        "=value",
    ] {
        fs::write(dir.path().join("bad"), input).unwrap();
        fs::write(dir.path().join("later"), "A=valid\n").unwrap();
        let result = run(dir.path(), &["dotenv", "merge", "bad", "later"], b"");
        assert_eq!(result.status.code(), Some(1), "{input}");
        assert!(result.stdout.is_empty());
        let stderr = String::from_utf8(result.stderr).unwrap();
        assert!(
            stderr.contains("DotenvSyntax")
                && stderr.contains("line=")
                && stderr.contains("input=1")
        );
        assert!(!stderr.contains("secret"));
    }
}

#[test]
fn dotenv_diagnostic_columns_count_unicode_scalars() {
    let dir = tempfile::tempdir().unwrap();
    for (input, line, column) in [
        ("A='é' trailing", 1, 7),
        ("A='🙂漢e\u{301}' trailing", 1, 10),
        ("#🙂 comment\r\nA='é\r\n漢🙂' trailing", 3, 5),
        ("A='🙂", 1, 5),
    ] {
        for args in [
            vec!["dotenv", "list", "--input", "-"],
            vec!["dotenv", "merge", "-"],
        ] {
            let result = run(dir.path(), &args, input.as_bytes());
            assert_eq!(result.status.code(), Some(1));
            assert!(result.stdout.is_empty());
            let stderr = String::from_utf8(result.stderr).unwrap();
            assert!(stderr.contains("DotenvSyntax"));
            assert!(stderr.contains(&format!("line={line}")), "{stderr}");
            assert!(stderr.contains(&format!("column={column}")), "{stderr}");
        }
    }
}

#[test]
fn yaml_merges_are_shallow_ordered_and_explicit_values_win() {
    yaml(
        "base: &base {z: 1, nested: {old: 1}, a: old}\nsecond: &second {z: 2, b: 3}\nresult: {<<: \
         [*base, *second], a: new, nested: {new: 2}}\n",
        "\"base\":\n  \"a\": \"old\"\n  \"nested\":\n    \"old\": 1\n  \"z\": 1\n\"result\":\n  \
         \"a\": \"new\"\n  \"b\": 3\n  \"nested\":\n    \"new\": 2\n  \"z\": 1\n\"second\":\n  \
         \"b\": 3\n  \"z\": 2\n",
    );
    yaml(
        "plain: {<<: {a: 1}, b: 2}\nquoted: {'<<': x}\ntagged: {!!str <<: y}\n",
        "\"plain\":\n  \"a\": 1\n  \"b\": 2\n\"quoted\":\n  \"<<\": \"x\"\n\"tagged\":\n  \"<<\": \
         \"y\"\n",
    );
    yaml(
        "a: &a {x: [1, 2]}\nb: &b {<<: *a}\nc: *b\n",
        "\"a\":\n  \"x\":\n    - 1\n    - 2\n\"b\":\n  \"x\":\n    - 1\n    - 2\n\"c\":\n  \
         \"x\":\n    - 1\n    - 2\n",
    );
}

#[test]
fn yaml_scalar_precision_and_core_types_are_preserved() {
    yaml(
        "[null, ~, NULL, true, FALSE, yes, 'true', '123', 000123, 0xFFFF, 0o77, \
         123456789012345678901234567890, 1.234567890123456789012345678901, \
         1e999999999999999999999, .inf, -.Inf, .NaN, !!float 1, !!str 42]",
        "- null\n- null\n- null\n- true\n- false\n- \"yes\"\n- \"true\"\n- \"123\"\n- 000123\n- \
         0xFFFF\n- 0o77\n- 123456789012345678901234567890\n- !!float \
         1.234567890123456789012345678901\n- !!float 1e999999999999999999999\n- !!float .inf\n- \
         !!float -.Inf\n- !!float .NaN\n- !!float 1\n- \"42\"\n",
    );
    yaml(
        "text: |\n  first\n  second\ncontrol: \"a\\u0085b\\u2028c\\u2029d\\0e\"\n",
        "\"control\": \"a\\u0085b\\u2028c\\u2029d\\u0000e\"\n\"text\": \"first\\nsecond\\n\"\n",
    );
    yaml(
        "[!!null '', !!bool True, !!int '123', !!seq [], !!map {}, ! 123]",
        "- null\n- true\n- 123\n- []\n- {}\n- \"123\"\n",
    );
}

#[test]
fn yaml_document_boundaries_unicode_sorting_and_anchor_scope() {
    yaml(
        "---\n# empty\n...\n---\nz: 2\na: 1\n---\n",
        "---\nnull\n---\n\"a\": 1\n\"z\": 2\n---\nnull\n",
    );
    yaml("---\n", "null\n");
    yaml("--- &a [1]\n--- &a [2]\n", "---\n- 1\n---\n- 2\n");
    yaml(
        "é: 1\né: 2\na: 3\nZ: 4\n😀: 5\n",
        "\"Z\": 4\n\"a\": 3\n\"é\": 2\n\"é\": 1\n\"😀\": 5\n",
    );
    yaml(
        "%YAML 1.2\n---\na: |\n  %YAML 1.1\n",
        "\"a\": \"%YAML 1.1\\n\"\n",
    );
}

#[test]
fn yaml_invalid_graphs_and_schema_never_publish_partial_results() {
    let dir = tempfile::tempdir().unwrap();
    for input in [
        "a: 1\na: 2",
        "{a: 1, 'a': 2}",
        "{1: value}",
        "{null: value}",
        "{[a]: value}",
        "a: !secret-marker v",
        "!!binary abc",
        "!!int nope",
        "!!bool yes",
        "!!seq {}",
        "{<<: 1}",
        "{<<: [{a: 1}, 2]}",
        "*undefined",
        "&a [*a]",
        "&a {<<: *a}",
        "--- &a 1\n--- *a",
        "%YAML 1.1\n---\na: yes",
        "%YAML 1.3\n---\na: yes",
        "a: [",
    ] {
        fs::write(dir.path().join("out"), "original").unwrap();
        let result = run(
            dir.path(),
            &["yaml", "normalize", "--output", "out", "--force"],
            input.as_bytes(),
        );
        assert_eq!(result.status.code(), Some(1), "{input}");
        assert!(result.stdout.is_empty());
        assert_eq!(fs::read(dir.path().join("out")).unwrap(), b"original");
        no_temps(dir.path());
    }
    let result = run(
        dir.path(),
        &["yaml", "normalize"],
        b"---\na: valid\n---\na: [",
    );
    assert_eq!(result.status.code(), Some(1));
    assert!(result.stdout.is_empty());
}

#[test]
fn private_file_output_force_in_place_and_input_overlap() {
    let dir = tempfile::tempdir().unwrap();
    let output = dir.path().join("out");
    let result = run(
        dir.path(),
        &["dotenv", "merge", "-", "--output", "out"],
        b"Z=1\nA=2",
    );
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(result.stdout.is_empty());
    assert_eq!(fs::read(&output).unwrap(), b"A=2\nZ=1\n");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(
            fs::metadata(&output).unwrap().permissions().mode() & 0o777,
            0o600
        );
        fs::set_permissions(&output, fs::Permissions::from_mode(0o640)).unwrap();
    }
    let result = run(
        dir.path(),
        &["dotenv", "merge", "-", "--output", "out"],
        b"A=3",
    );
    assert_eq!(result.status.code(), Some(1));
    assert_eq!(fs::read(&output).unwrap(), b"A=2\nZ=1\n");
    let result = run(
        dir.path(),
        &["dotenv", "merge", "out", "-", "--output", "out", "--force"],
        b"A=3",
    );
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(fs::read(&output).unwrap(), b"A=3\nZ=1\n");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(
            fs::metadata(&output).unwrap().permissions().mode() & 0o777,
            0o640
        );
    }
    fs::write(&output, "z: 2\na: 1").unwrap();
    let result = run(
        dir.path(),
        &["yaml", "normalize", "--input", "out", "--in-place"],
        b"",
    );
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(result.stdout.is_empty());
    assert_eq!(fs::read(&output).unwrap(), b"\"a\": 1\n\"z\": 2\n");
    let result = run(
        dir.path(),
        &["dotenv", "list", "--input", "-", "--output", "./-"],
        b"A=value",
    );
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(fs::read(dir.path().join("-")).unwrap(), b"A\n");
    no_temps(dir.path());
}

#[test]
fn in_place_rejects_hard_linked_input_before_decoding() {
    let dir = tempfile::tempdir().unwrap();
    let source = dir.path().join("source");
    let bytes = b"\xffprivate-input-marker";
    fs::write(&source, bytes).unwrap();
    fs::hard_link(&source, dir.path().join("linked")).unwrap();
    let result = run(
        dir.path(),
        &["yaml", "normalize", "--input", "linked", "--in-place"],
        b"",
    );
    assert_eq!(result.status.code(), Some(1));
    assert!(result.stdout.is_empty());
    let stderr = String::from_utf8(result.stderr).unwrap();
    assert!(stderr.contains("UnsafeDestination"), "{stderr}");
    assert!(!stderr.contains("Encoding"));
    assert!(!stderr.contains("private-input-marker"));
    assert_eq!(fs::read(&source).unwrap(), bytes);
    assert_eq!(fs::read(dir.path().join("linked")).unwrap(), bytes);
    no_temps(dir.path());
}

#[test]
fn publication_failures_links_and_directory_destinations() {
    let dir = tempfile::tempdir().unwrap();
    fs::write(dir.path().join("original"), "original").unwrap();
    fs::hard_link(dir.path().join("original"), dir.path().join("hard")).unwrap();
    fs::create_dir(dir.path().join("directory")).unwrap();
    for output in ["hard", "directory", "missing/output"] {
        let result = run(
            dir.path(),
            &["dotenv", "merge", "-", "--output", output, "--force"],
            b"A=1",
        );
        assert_eq!(result.status.code(), Some(1));
        assert!(result.stdout.is_empty());
        no_temps(dir.path());
    }
    assert_eq!(fs::read(dir.path().join("original")).unwrap(), b"original");
    #[cfg(unix)]
    {
        std::os::unix::fs::symlink("original", dir.path().join("sym")).unwrap();
        let result = run(
            dir.path(),
            &["dotenv", "merge", "-", "--output", "sym", "--force"],
            b"A=1",
        );
        assert_eq!(result.status.code(), Some(1));
        fs::write(dir.path().join("original"), "A=1").unwrap();
        assert_eq!(
            run(dir.path(), &["dotenv", "list", "--input", "sym"], b"").stdout,
            b"A\n"
        );
        assert_eq!(
            run(
                dir.path(),
                &["yaml", "normalize", "--input", "sym", "--in-place"],
                b""
            )
            .status
            .code(),
            Some(1)
        );
    }
    no_temps(dir.path());
}

#[cfg(unix)]
#[test]
fn replacement_preserves_read_only_and_write_only_permissions() {
    use std::os::unix::fs::PermissionsExt;

    let dir = tempfile::tempdir().unwrap();
    let output = dir.path().join("out");
    for mode in [0o400, 0o200] {
        fs::write(&output, "original").unwrap();
        fs::set_permissions(&output, fs::Permissions::from_mode(mode)).unwrap();
        let result = run(
            dir.path(),
            &["dotenv", "merge", "-", "--output", "out", "--force"],
            b"A=replaced",
        );
        assert!(
            result.status.success(),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert!(result.stdout.is_empty());
        assert_eq!(
            fs::metadata(&output).unwrap().permissions().mode() & 0o777,
            mode
        );
        // Restore read access only after verifying the published permissions.
        fs::set_permissions(&output, fs::Permissions::from_mode(0o600)).unwrap();
        assert_eq!(fs::read(&output).unwrap(), b"A=replaced\n");
        no_temps(dir.path());
    }
}

#[test]
fn debug_diagnostics_never_disclose_input_keys_values_paths_or_argv() {
    let dir = tempfile::tempdir().unwrap();
    for (args, input) in [
        (
            vec!["dotenv", "list", "--input", "-"],
            "SECRET_MARKER='SECRET_MARKER' trailing",
        ),
        (
            vec!["yaml", "normalize"],
            "SECRET_MARKER: !SECRET_MARKER SECRET_MARKER",
        ),
        (vec!["yaml", "normalize"], "SECRET_MARKER: *SECRET_MARKER"),
        (vec!["yaml", "normalize"], "SECRET_MARKER: [SECRET_MARKER"),
        (vec!["dotenv", "list", "--input", "SECRET_MARKER"], ""),
        (
            vec![
                "dotenv",
                "list",
                "--input",
                "-",
                "--output",
                "SECRET_MARKER/out",
            ],
            "SECRET_MARKER=SECRET_MARKER",
        ),
        (vec!["yaml", "normalize", "--SECRET_MARKER"], ""),
    ] {
        let mut child = command(dir.path())
            .env("RUST_LOG", "trace")
            .args(args)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let _ = child.stdin.take().unwrap().write_all(input.as_bytes());
        let result = child.wait_with_output().unwrap();
        assert!(!result.status.success());
        assert!(result.stdout.is_empty());
        let stderr = String::from_utf8(result.stderr).unwrap();
        assert!(!stderr.contains("SECRET_MARKER"), "{stderr}");
        assert!(!stderr.contains('\u{1b}'));
        assert!(!stderr.contains(&dir.path().display().to_string()));
    }
}

#[test]
fn nesting_and_compact_exponential_alias_expansion_are_bounded() {
    let accepted = format!("{}0{}", "[".repeat(128), "]".repeat(128));
    let dir = tempfile::tempdir().unwrap();
    assert!(run(dir.path(), &["yaml", "normalize"], accepted.as_bytes())
        .status
        .success());
    let rejected = format!("{}0{}", "[".repeat(129), "]".repeat(129));
    let result = run(dir.path(), &["yaml", "normalize"], rejected.as_bytes());
    assert_eq!(result.status.code(), Some(1));
    assert!(result.stdout.is_empty());
    let mut bomb = "a0: &a0 [x, x]\n".to_owned();
    for index in 1..64 {
        bomb.push_str(&format!(
            "a{index}: &a{index} [*a{}, *a{}]\n",
            index - 1,
            index - 1
        ));
    }
    let result = run(dir.path(), &["yaml", "normalize"], bomb.as_bytes());
    assert_eq!(result.status.code(), Some(1));
    assert!(result.stdout.is_empty());
    assert!(String::from_utf8_lossy(&result.stderr).contains("OutputLimit"));
}

#[test]
fn yaml_output_limit_ignores_shadowed_escaped_merge_values() {
    const LIMIT: usize = 64 * 1024 * 1024;
    // Each two-byte input escape becomes six output bytes. The raw document is
    // within its budget, but encoding this value would exceed the output limit.
    let escaped = "\\0".repeat(LIMIT / 6 + 1);
    let dir = tempfile::tempdir().unwrap();
    for source in [
        format!("<<: {{value: \"{escaped}\"}}\nvalue: small\n"),
        format!("<<: [{{value: small}}, {{value: \"{escaped}\"}}]\n"),
    ] {
        assert!(source.len() < LIMIT);
        let result = run(dir.path(), &["yaml", "normalize"], source.as_bytes());
        assert!(
            result.status.success(),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(result.stdout, b"\"value\": \"small\"\n");
        assert!(result.stderr.is_empty());
    }
    // Keeping the same value reachable still fails before stdout publication.
    let source = format!("value: \"{escaped}\"\n");
    let result = run(dir.path(), &["yaml", "normalize"], source.as_bytes());
    assert_eq!(result.status.code(), Some(1));
    assert!(result.stdout.is_empty());
    assert!(String::from_utf8_lossy(&result.stderr).contains("OutputLimit"));
}

#[test]
fn yaml_shares_long_shadowed_merge_chains_with_bounded_memory() {
    let dir = tempfile::tempdir().unwrap();
    let mut source = "<<:\n  payload:\n    - &a0 {k0: 0}\n".to_owned();
    for i in 1usize..5000 {
        // Repeated and nearly identical operands must retain structural sharing.
        source.push_str(&format!(
            "    - &a{i} {{<<: [*a{}, *a{}], k{i}: 0}}\n",
            i - 1,
            i.saturating_sub(2)
        ));
    }
    source.push_str("payload: kept\n");
    assert!(source.len() < 1024 * 1024);
    fs::write(dir.path().join("chain.yaml"), source).unwrap();
    let mut cmd = command(dir.path());
    cmd.args(["yaml", "normalize", "--input", "chain.yaml"])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    #[cfg(target_os = "linux")]
    {
        use std::os::unix::process::CommandExt;
        // Bound only this test-owned child: materializing every intermediate
        // map would exceed this budget even though the final result is tiny.
        unsafe {
            cmd.pre_exec(|| {
                let limit = libc::rlimit {
                    rlim_cur: 256 * 1024 * 1024,
                    rlim_max: 256 * 1024 * 1024,
                };
                if libc::setrlimit(libc::RLIMIT_AS, &limit) != 0 {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
    }
    let mut child = cmd.spawn().unwrap();
    wait(&mut child);
    let result = child.wait_with_output().unwrap();
    assert!(result.status.success(), "{:?}", result.stderr);
    assert_eq!(result.stdout, b"\"payload\": \"kept\"\n");
    assert!(result.stderr.is_empty());
}

#[test]
fn yaml_mapping_statistics_recover_after_shadowing_saturated_values() {
    let mut source = "<<:\n  payload:\n    - &a0 x\n".to_owned();
    for i in 1..125 {
        source.push_str(&format!("    - &a{i} [*a{}, *a{}]\n", i - 1, i - 1));
    }
    // The inherited size exceeds usize, but its removal must restore the exact
    // small size and depth instead of subtracting from a saturated total.
    source.push_str("payload: kept\n");
    yaml(&source, "\"payload\": \"kept\"\n");
}

fn wait(child: &mut std::process::Child) {
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(15);
    while child.try_wait().unwrap().is_none() {
        if std::time::Instant::now() >= deadline {
            let _ = child.kill();
            panic!("child did not terminate");
        }
        std::thread::sleep(std::time::Duration::from_millis(10));
    }
}

#[test]
fn broken_stdout_is_a_redacted_runtime_failure() {
    let dir = tempfile::tempdir().unwrap();
    fs::write(
        dir.path().join("input"),
        format!("A={}\n", "x".repeat(1024 * 1024)),
    )
    .unwrap();
    let mut child = command(dir.path())
        .args(["dotenv", "merge", "input"])
        .env("RUST_LOG", "off")
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    drop(child.stdout.take());
    wait(&mut child);
    let result = child.wait_with_output().unwrap();
    assert_eq!(result.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&result.stderr).contains("Write"));
}

#[test]
fn filtered_configuration_errors_remain_actionable_and_redacted() {
    let dir = tempfile::tempdir().unwrap();
    fs::write(dir.path().join("out"), "original").unwrap();
    for filter in [
        "off",
        "clibox=off",
        "clibox::config_runtime=off",
        "yaml_rust2=trace",
    ] {
        for (args, input, code, classification) in [
            (
                vec!["dotenv", "merge", "-", "--output", "out", "--force"],
                "SECRET_MARKER invalid",
                1,
                "DotenvSyntax",
            ),
            (
                vec!["yaml", "normalize", "--output", "out", "--force"],
                "SECRET_MARKER: [",
                1,
                "YamlSyntax",
            ),
            (
                vec!["dotenv", "list", "--input", "SECRET_MARKER"],
                "",
                1,
                "Read",
            ),
            (
                vec!["dotenv", "merge", "-", "--output", "SECRET_MARKER/out"],
                "A=SECRET_MARKER",
                1,
                "Publish",
            ),
            (
                vec!["dotenv", "merge", "-", "--output", "out"],
                "A=SECRET_MARKER",
                1,
                "DestinationExists",
            ),
            (
                vec!["dotenv", "merge", "-", "--force"],
                "SECRET_MARKER",
                2,
                "Arguments",
            ),
        ] {
            let mut child = command(dir.path())
                .args(args)
                .env("RUST_LOG", filter)
                .stdin(Stdio::piped())
                .stdout(Stdio::piped())
                .stderr(Stdio::piped())
                .spawn()
                .unwrap();
            let _ = child.stdin.take().unwrap().write_all(input.as_bytes());
            let output = child.wait_with_output().unwrap();
            assert_eq!(output.status.code(), Some(code));
            assert!(output.stdout.is_empty());
            let stderr = String::from_utf8(output.stderr).unwrap();
            assert!(stderr.contains("error:"), "{filter}: {stderr}");
            assert!(stderr.contains(classification), "{filter}: {stderr}");
            assert_eq!(stderr.lines().count(), 1);
            assert!(!stderr.contains("SECRET_MARKER"));
            assert!(!stderr.contains(&dir.path().display().to_string()));
            assert!(!stderr.contains('\u{1b}'));
            assert_eq!(fs::read(dir.path().join("out")).unwrap(), b"original");
            no_temps(dir.path());
        }
    }
}

#[test]
fn closed_stderr_preserves_success_and_runtime_failure_statuses() {
    use std::io::{BufRead, BufReader};

    for (filter, input, code, expected) in [
        ("clibox=debug", "A=1", 0, "A=1\n"),
        ("clibox=debug", "invalid", 1, ""),
        ("off", "A=1", 0, "A=1\n"),
        ("off", "invalid", 1, ""),
    ] {
        let dir = tempfile::tempdir().unwrap();
        let mut child = command(dir.path())
            .args(["dotenv", "merge", "-"])
            .env("RUST_LOG", filter)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let mut stderr = BufReader::new(child.stderr.take().unwrap());
        let mut ready = String::new();
        if filter != "off" {
            stderr.read_line(&mut ready).unwrap();
            assert!(ready.contains("operation_started"));
        }
        // Close the diagnostic consumer before allowing processing to finish.
        // Both completion and content-error diagnostics must tolerate this.
        drop(stderr);
        child
            .stdin
            .take()
            .unwrap()
            .write_all(input.as_bytes())
            .unwrap();
        wait(&mut child);
        let result = child.wait_with_output().unwrap();
        assert_eq!(result.status.code(), Some(code));
        assert_eq!(result.stdout, expected.as_bytes());
    }
}

#[cfg(unix)]
#[test]
fn signals_interrupt_blocked_stdin_and_stdout_with_expected_codes() {
    use std::io::{BufRead, BufReader};
    for (signal, code) in [(libc::SIGINT, 130), (libc::SIGTERM, 143)] {
        for blocked_output in [false, true] {
            let dir = tempfile::tempdir().unwrap();
            fs::write(
                dir.path().join("input"),
                format!("A={}\n", "x".repeat(1024 * 1024)),
            )
            .unwrap();
            fs::write(dir.path().join("out"), "original").unwrap();
            let args = if blocked_output {
                vec!["dotenv", "merge", "input"]
            } else {
                vec!["dotenv", "merge", "-", "--output", "out", "--force"]
            };
            let mut child = command(dir.path())
                .env("RUST_LOG", "clibox=debug")
                .args(args)
                .stdin(Stdio::piped())
                .stdout(Stdio::piped())
                .stderr(Stdio::piped())
                .spawn()
                .unwrap();
            let mut stderr = BufReader::new(child.stderr.take().unwrap());
            let mut ready = String::new();
            stderr.read_line(&mut ready).unwrap();
            assert!(ready.contains("operation_started"));
            if blocked_output {
                let mut chunk = [0; 1];
                std::io::Read::read_exact(child.stdout.as_mut().unwrap(), &mut chunk).unwrap();
            }
            // Cancellation diagnostics must tolerate a closed stderr consumer.
            drop(stderr);
            assert_eq!(unsafe { libc::kill(child.id() as i32, signal) }, 0);
            wait(&mut child);
            assert_eq!(child.wait().unwrap().code(), Some(code));
            assert_eq!(fs::read(dir.path().join("out")).unwrap(), b"original");
            no_temps(dir.path());
        }
    }
}

#[test]
fn aggregate_raw_input_boundaries_and_serialized_growth() {
    const LIMIT: usize = 64 * 1024 * 1024;
    let dir = tempfile::tempdir().unwrap();
    // Comments count toward raw input even though they produce no result.
    let mut input = vec![b'#'];
    input.resize(LIMIT / 2, b'x');
    fs::write(dir.path().join("first"), &input).unwrap();
    fs::write(dir.path().join("second"), &input).unwrap();
    let result = run(dir.path(), &["dotenv", "merge", "first", "second"], b"");
    assert!(result.status.success());
    assert!(result.stdout.is_empty());
    input.push(b'x');
    fs::write(dir.path().join("second"), &input).unwrap();
    let result = run(dir.path(), &["dotenv", "merge", "first", "second"], b"");
    assert_eq!(result.status.code(), Some(1));
    assert!(result.stdout.is_empty());
    assert!(String::from_utf8_lossy(&result.stderr).contains("InputLimit"));
    input.clear();
    input.extend_from_slice(b"A=");
    input.resize(LIMIT, b'x');
    fs::write(dir.path().join("first"), &input).unwrap();
    let result = run(dir.path(), &["dotenv", "merge", "first"], b"");
    assert_eq!(result.status.code(), Some(1));
    assert!(result.stdout.is_empty());
    assert!(String::from_utf8_lossy(&result.stderr).contains("OutputLimit"));
}

#[test]
fn concurrent_authorized_replacements_produce_one_complete_result() {
    for round in 0..8 {
        let dir = tempfile::tempdir().unwrap();
        fs::write(dir.path().join("out"), "original").unwrap();
        let payloads = [
            format!("A={}\n", "x".repeat(65536)),
            format!("B={}\n", "y".repeat(65536)),
        ];
        let mut children = Vec::new();
        for (i, payload) in payloads.iter().enumerate() {
            fs::write(dir.path().join(format!("in{i}")), payload).unwrap();
            children.push(
                command(dir.path())
                    .env("RUST_LOG", "clibox=debug")
                    .args([
                        "dotenv",
                        "merge",
                        &format!("in{i}"),
                        "--output",
                        "out",
                        "--force",
                    ])
                    .stdout(Stdio::null())
                    .stderr(Stdio::piped())
                    .spawn()
                    .unwrap(),
            );
        }
        let mut successful_payloads = Vec::new();
        let mut outcomes = Vec::new();
        for (mut child, payload) in children.into_iter().zip(&payloads) {
            wait(&mut child);
            let output = child.wait_with_output().unwrap();
            outcomes.push((
                output.status.code(),
                String::from_utf8_lossy(&output.stderr).into_owned(),
            ));
            if output.status.success() {
                successful_payloads.push(payload);
            } else {
                let diagnostic = String::from_utf8_lossy(&output.stderr);
                // Windows sharing can reject concurrent inspection/publication.
                // Unix inspection can observe a zero-link destination handle
                // after the other writer replaces it and must fail closed.
                // A failed writer must not overwrite a successful writer's result;
                // no locks or automatic retries are promised.
                assert_eq!(output.status.code(), Some(1), "{diagnostic}");
                #[cfg(unix)]
                assert!(
                    diagnostic.contains("classification=UnsafeDestination"),
                    "{diagnostic}"
                );
                #[cfg(windows)]
                {
                    assert!(
                        diagnostic.contains("classification=Publish")
                            || diagnostic.contains("classification=Permissions"),
                        "{diagnostic}"
                    );
                }
            }
        }
        assert!(
            !successful_payloads.is_empty(),
            "round {round}: {outcomes:?}"
        );
        let result = fs::read_to_string(dir.path().join("out")).unwrap();
        assert!(
            successful_payloads.contains(&&result),
            "round {round}: final length={}, matching writer={:?}, outcomes={outcomes:?}",
            result.len(),
            payloads.iter().position(|payload| payload == &result)
        );
        no_temps(dir.path());
        // A replacement started after both have finished deterministically wins.
        assert!(run(
            dir.path(),
            &["dotenv", "merge", "-", "--output", "out", "--force"],
            b"C=last"
        )
        .status
        .success());
        assert_eq!(fs::read(dir.path().join("out")).unwrap(), b"C=last\n");
        no_temps(dir.path());
    }
}

#[cfg(windows)]
#[test]
fn windows_sharing_conflict_preserves_destination_and_cleans_staging() {
    use std::os::windows::fs::OpenOptionsExt;

    use windows_sys::Win32::Storage::FileSystem::{FILE_SHARE_READ, FILE_SHARE_WRITE};

    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("out");
    fs::write(&path, "original").unwrap();
    // Permit ordinary access but deny deletion/rename while this test-owned
    // handle is live. This deterministically exercises Windows sharing failure.
    let blocker = fs::OpenOptions::new()
        .read(true)
        .share_mode(FILE_SHARE_READ | FILE_SHARE_WRITE)
        .open(&path)
        .unwrap();
    let args = ["dotenv", "merge", "-", "--output", "out", "--force"];
    let failed = run(dir.path(), &args, b"A=new");
    assert_eq!(failed.status.code(), Some(1));
    assert!(failed.stdout.is_empty());
    assert!(
        String::from_utf8_lossy(&failed.stderr).contains("classification=Publish"),
        "{}",
        String::from_utf8_lossy(&failed.stderr)
    );
    assert_eq!(fs::read(&path).unwrap(), b"original");
    no_temps(dir.path());

    drop(blocker);
    let success = run(dir.path(), &args, b"A=new");
    assert!(
        success.status.success(),
        "{}",
        String::from_utf8_lossy(&success.stderr)
    );
    assert!(success.stdout.is_empty());
    assert_eq!(fs::read(&path).unwrap(), b"A=new\n");
    no_temps(dir.path());
}

#[cfg(windows)]
#[test]
fn windows_console_interrupt_cleans_up_and_returns_130() {
    use std::{
        io::{BufRead, BufReader},
        os::windows::process::CommandExt,
    };

    use windows_sys::Win32::System::{Console::*, Threading::CREATE_NEW_PROCESS_GROUP};
    // CI may have no attached console. Allocate one for this process test and
    // target only the child's process group, never the test runner's group.
    let allocated = unsafe { AllocConsole() } != 0;
    let dir = tempfile::tempdir().unwrap();
    let mut child = command(dir.path())
        .creation_flags(CREATE_NEW_PROCESS_GROUP)
        .env("RUST_LOG", "clibox=debug")
        .args(["dotenv", "merge", "-", "--output", "out"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut ready = String::new();
    let mut stderr = BufReader::new(child.stderr.take().unwrap());
    stderr.read_line(&mut ready).unwrap();
    assert!(ready.contains("operation_started"));
    // Also exercise cancellation after the diagnostic consumer has gone away.
    drop(stderr);
    // CTRL_BREAK is the targeted Windows console interrupt; CTRL_C cannot be
    // scoped to a process group. Both use the same installed ctrlc callback.
    assert_ne!(
        unsafe { GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, child.id()) },
        0
    );
    wait(&mut child);
    assert_eq!(child.wait().unwrap().code(), Some(130));
    assert!(!dir.path().join("out").exists());
    no_temps(dir.path());
    if allocated {
        unsafe {
            FreeConsole();
        }
    }
}

#[cfg(windows)]
#[test]
fn windows_replacement_preserves_explicit_dacl() {
    use std::os::windows::ffi::OsStrExt;

    use windows_sys::Win32::{
        Foundation::LocalFree,
        Security::{Authorization::*, *},
    };
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("out");
    fs::write(&path, "original").unwrap();
    let wide: Vec<u16> = path.as_os_str().encode_wide().chain([0]).collect();
    let sddl: Vec<u16> = "D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;OW)"
        .encode_utf16()
        .chain([0])
        .collect();
    let mut descriptor = std::ptr::null_mut();
    assert_ne!(
        unsafe {
            ConvertStringSecurityDescriptorToSecurityDescriptorW(
                sddl.as_ptr(),
                1,
                &mut descriptor,
                std::ptr::null_mut(),
            )
        },
        0
    );
    assert_ne!(
        unsafe {
            SetFileSecurityW(
                wide.as_ptr(),
                DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
                descriptor,
            )
        },
        0
    );
    unsafe {
        LocalFree(descriptor);
    }
    let security = || {
        let mut needed = 0;
        unsafe {
            GetFileSecurityW(
                wide.as_ptr(),
                DACL_SECURITY_INFORMATION,
                std::ptr::null_mut(),
                0,
                &mut needed,
            );
        }
        assert!(needed > 0);
        let mut data = vec![0u8; needed as usize];
        assert_ne!(
            unsafe {
                GetFileSecurityW(
                    wide.as_ptr(),
                    DACL_SECURITY_INFORMATION,
                    data.as_mut_ptr().cast(),
                    needed,
                    &mut needed,
                )
            },
            0
        );
        let mut control = 0;
        let mut revision = 0;
        let mut present = 0;
        let mut defaulted = 0;
        let mut dacl = std::ptr::null_mut();
        // Compare the actual ACL and its protection/control semantics, not the
        // self-relative descriptor's storage layout. Windows may set
        // SE_DACL_AUTO_INHERITED while preserving every ACE and protection from
        // parent inheritance. That bookkeeping bit grants no additional access.
        // SAFETY: data holds a valid OS-produced descriptor throughout these
        // queries and the ACL copy; no returned pointer outlives the buffer.
        unsafe {
            assert_ne!(
                GetSecurityDescriptorControl(data.as_mut_ptr().cast(), &mut control, &mut revision),
                0
            );
            assert_ne!(
                GetSecurityDescriptorDacl(
                    data.as_mut_ptr().cast(),
                    &mut present,
                    &mut dacl,
                    &mut defaulted,
                ),
                0
            );
            assert_ne!(present, 0);
            assert!(!dacl.is_null());
            assert_ne!(IsValidAcl(dacl), 0);
            assert_ne!(control & SE_DACL_PROTECTED, 0);
            (
                control & !SE_DACL_AUTO_INHERITED,
                revision,
                defaulted,
                std::slice::from_raw_parts(dacl.cast::<u8>(), usize::from((*dacl).AclSize))
                    .to_vec(),
            )
        }
    };
    let before = security();
    let result = run(
        dir.path(),
        &["dotenv", "merge", "-", "--output", "out", "--force"],
        b"A=1",
    );
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(security(), before);
    assert_eq!(fs::read(&path).unwrap(), b"A=1\n");
}

#[test]
fn core_tag_handles_verbatim_tags_and_nonspecific_tags_preserve_semantics() {
    yaml(
        "%TAG !a! tag:yaml.org,2002:\n%TAG !b! tag:yaml.org,2002:\n---\né: !a!str 1\nx: !b!float \
         123\n",
        "\"x\": !!float 123\n\"é\": \"1\"\n",
    );
    yaml(
        "%TAG !! tag:example.com,2026:\n---\na: !<tag:yaml.org,2002:str> 1\n",
        "\"a\": \"1\"\n",
    );
    yaml(
        "[<<, ! [], ! {}, !!str <<, !<tag:yaml.org,2002:int> 12]",
        "- \"<<\"\n- []\n- {}\n- \"<<\"\n- 12\n",
    );
    let dir = tempfile::tempdir().unwrap();
    for input in [
        "%TAG !a! tag:yaml.org,2002:\n%TAG !a! tag:yaml.org,2002:\n---\n1",
        "%TAG !a! tag:yaml.org,2002:\na: 1",
        "%TAG !! tag:example.com,2026:\n---\na: !!str x",
        "!s 1",
        "%TAG !a! tag:yaml.org,2002:\n---\n!a!str x\n---\n!a!str y",
    ] {
        let result = run(dir.path(), &["yaml", "normalize"], input.as_bytes());
        assert_eq!(result.status.code(), Some(1), "{input}");
        assert!(result.stdout.is_empty());
    }
}

#[test]
fn yaml_rejects_nonprintable_source_and_incomplete_directives() {
    let dir = tempfile::tempdir().unwrap();
    for input in [
        "x: \"a\u{1}b\"",
        "x: \"a\u{7f}b\"",
        "x: \"a\u{ffff}b\"",
        "%TAG !a! tag:yaml.org,2002:\n",
        "%TAG !a! tag:yaml.org,2002:\n...\n",
        "a: 1\n%TAG !a! tag:yaml.org,2002:\n---\nx: 2",
    ] {
        let result = run(dir.path(), &["yaml", "normalize"], input.as_bytes());
        assert_eq!(result.status.code(), Some(1), "{input:?}");
        assert!(result.stdout.is_empty());
    }
}

#[test]
fn yaml_source_positions_count_cr_lf_and_crlf_once() {
    let dir = tempfile::tempdir().unwrap();
    for (separator, line) in [("\r", 2), ("\n", 2), ("\r\n", 2), ("\r\r\n", 3)] {
        for filter in ["debug", "off"] {
            let source = format!("header: ok{separator}한글: \u{1}SECRET-CR");
            fs::write(dir.path().join("input.yaml"), source).unwrap();
            let result = command(dir.path())
                .args(["yaml", "normalize", "--input", "input.yaml"])
                .env("RUST_LOG", filter)
                .output()
                .unwrap();
            assert_eq!(result.status.code(), Some(1));
            assert!(result.stdout.is_empty());
            let stderr = String::from_utf8(result.stderr).unwrap();
            assert!(stderr.contains("YamlSyntax"), "{stderr}");
            let position = if filter == "off" {
                format!("line=Some({line}) column=Some(5)")
            } else {
                format!("line={line} column=5")
            };
            assert!(stderr.contains(&position), "{separator:?}: {stderr}");
            assert!(!stderr.contains("SECRET-CR"));
        }
    }
    yaml("b: 2\ra: 1\r", "\"a\": 1\n\"b\": 2\n");
}

#[test]
fn yaml_escaped_bmp_noncharacters_remain_valid_and_idempotent() {
    yaml(
        "\"\\uFFFE\": &value \"\\uFFFF\"\ncopy: *value\nlist: [\"\\uFFFE\", \"\\U0000FFFF\"]\n",
        "\"copy\": \"\\uffff\"\n\"list\":\n  - \"\\ufffe\"\n  - \"\\uffff\"\n\"\\ufffe\": \
         \"\\uffff\"\n",
    );
}

#[test]
fn yaml_version_directives_are_unique_per_document() {
    let dir = tempfile::tempdir().unwrap();
    for input in [
        "%YAML 1.2\n%YAML 1.2\n---\na: 1",
        "%YAML 1.2\n%TAG !s! tag:yaml.org,2002:\n%YAML 1.2\n---\na: 1",
        "---\na: 1\n...\n%YAML 1.2\n%YAML 1.2\n---\nb: 2",
    ] {
        fs::write(dir.path().join("out"), "original").unwrap();
        let result = run(
            dir.path(),
            &["yaml", "normalize", "--output", "out", "--force"],
            input.as_bytes(),
        );
        assert_eq!(result.status.code(), Some(1));
        assert!(result.stdout.is_empty());
        assert!(String::from_utf8_lossy(&result.stderr).contains("YamlSyntax"));
        assert_eq!(fs::read(dir.path().join("out")).unwrap(), b"original");
        no_temps(dir.path());
    }
    yaml(
        "%YAML 1.2\n---\na: 1\n...\n%YAML 1.2\n---\nb: 2",
        "---\n\"a\": 1\n---\n\"b\": 2\n",
    );
}

#[cfg(unix)]
#[test]
fn restrictive_umask_keeps_new_outputs_readable_and_preserves_replacements() {
    use std::os::unix::{fs::PermissionsExt, process::CommandExt};
    for existing in [false, true] {
        let dir = tempfile::tempdir().unwrap();
        fs::write(dir.path().join("input"), "A=1").unwrap();
        let path = dir.path().join("out");
        if existing {
            fs::write(&path, "original").unwrap();
            fs::set_permissions(&path, fs::Permissions::from_mode(0o640)).unwrap();
        }
        let mut cmd = command(dir.path());
        cmd.args(["dotenv", "merge", "input", "--output", "out", "--force"]);
        // Change only the child process's mask, never the parallel test runner.
        unsafe {
            cmd.pre_exec(|| {
                libc::umask(0o777);
                Ok(())
            });
        }
        let result = cmd.output().unwrap();
        assert!(
            result.status.success(),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            if existing { 0o640 } else { 0o600 }
        );
        assert_eq!(fs::read(&path).unwrap(), b"A=1\n");
        no_temps(dir.path());
    }
}
