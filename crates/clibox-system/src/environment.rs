use std::{
    collections::BTreeMap,
    ffi::{OsStr, OsString},
    process::{Command, ExitStatus},
    sync::LazyLock,
};

use regex::{Captures, Regex};

use crate::{
    error::{Code, Failure, Result},
    runtime,
};

// Compatibility reference: cross-env v10.1.0, commit
// 152ae6a85b5725ac3c725a8a3e471aee79acc712 (MIT). Keep the unusual parent-only
// assignment expansion and platform-specific command conversion covered by
// fixtures.
static ASSIGNMENT: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r#"([A-Za-z0-9_]+)=('(.*)'|"(.*)"|(.*))"#).unwrap());
static VARIABLE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"(\\*)(\$([A-Za-z0-9_]+)|\$\{([A-Za-z0-9_]+)\})").unwrap());
static LIST: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"(\\*):").unwrap());
static SIMPLE: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"\$([A-Za-z0-9_]+)|\$\{([A-Za-z0-9_]+)\}").unwrap());
static DEFAULT: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"\$\{([A-Za-z0-9_]+):-([^}]+)\}").unwrap());

type Environment = BTreeMap<OsString, OsString>;

fn lookup<'a>(env: &'a Environment, key: &str, windows: bool) -> Option<&'a OsStr> {
    if windows {
        env.iter()
            .find(|(name, _)| name.to_string_lossy().eq_ignore_ascii_case(key))
            .map(|(_, v)| v.as_os_str())
    } else {
        env.get(OsStr::new(key)).map(OsString::as_os_str)
    }
}

fn value(value: &str, name: &str, parent: &Environment, windows: bool) -> String {
    let list = if matches!(name, "PATH" | "NODE_PATH") {
        LIST.replace_all(value, |c: &Captures<'_>| {
            if c[1].len() % 2 == 1 {
                c[0][1..].to_owned()
            } else {
                format!("{}{}", &c[1], if windows { ';' } else { ':' })
            }
        })
        .into_owned()
    } else {
        value.to_owned()
    };
    VARIABLE
        .replace_all(&list, |c: &Captures<'_>| {
            if c[1].len() % 2 == 1 {
                c[2].to_owned()
            } else {
                let key = c.get(3).or_else(|| c.get(4)).unwrap().as_str();
                format!(
                    "{}{}",
                    "\\".repeat(c[1].len() / 2),
                    lookup(parent, key, windows)
                        .unwrap_or_default()
                        .to_string_lossy()
                )
            }
        })
        .into_owned()
}

fn command_convert(arg: OsString, env: &Environment, windows: bool) -> OsString {
    if !windows {
        return arg;
    }
    let Some(text) = arg.to_str() else {
        return arg;
    };
    let defaults = DEFAULT.replace_all(text, |c: &Captures<'_>| {
        lookup(env, &c[1], true)
            .filter(|v| !v.is_empty())
            .map_or_else(|| c[2].to_owned(), |v| v.to_string_lossy().into_owned())
    });
    SIMPLE
        .replace_all(&defaults, |c: &Captures<'_>| {
            let key = c.get(1).or_else(|| c.get(2)).unwrap().as_str();
            if lookup(env, key, true).is_some_and(|v| !v.is_empty()) {
                format!("%{key}%")
            } else {
                String::new()
            }
        })
        .into_owned()
        .into()
}

#[derive(Clone)]
pub(crate) struct Plan {
    command: OsString,
    args: Vec<OsString>,
    env: Environment,
}

fn plan(args: Vec<OsString>, parent: Environment, windows: bool) -> Result<Plan> {
    let mut setters = Vec::new();
    let mut first = 0;
    for arg in &args {
        if arg == "--" {
            first += 1;
            break;
        }
        if let Some(c) = arg.to_str().and_then(|s| ASSIGNMENT.captures(s)) {
            let raw = c
                .get(3)
                .or_else(|| c.get(4))
                .or_else(|| c.get(5))
                .unwrap()
                .as_str();
            setters.push((c[1].to_owned(), raw.to_owned()));
            first += 1;
        } else {
            break;
        }
    }
    let mut env = parent.clone();
    for (key, raw) in setters {
        if windows {
            env.retain(|k, _| !k.to_string_lossy().eq_ignore_ascii_case(&key));
        }
        env.insert(
            key.clone().into(),
            value(&raw, &key, &parent, windows).into(),
        );
    }
    // The caller already tokenized argv. Assignment escaping must not strip
    // literal child quotes or backslashes (including a Windows UNC prefix).
    let mut converted = args
        .into_iter()
        .skip(first)
        .map(|a| command_convert(a, &env, windows));
    let command = converted.next().filter(|s| !s.is_empty()).ok_or_else(|| {
        Failure::new(
            Code::InvalidInput,
            "A child command is required after environment assignments; use clibox run env --help.",
        )
    })?;
    Ok(Plan {
        command,
        args: converted.collect(),
        env,
    })
}

/// Prepare one literal child invocation using the same compatibility rules as
/// `run env`. Run wrappers use this rather than reparsing environment
/// assignments so nesting does not change command or PATH behavior.
pub(crate) fn prepare(args: Vec<OsString>) -> Result<Plan> {
    plan(args, std::env::vars_os().collect(), cfg!(windows))
}

pub(crate) fn command(plan: &Plan) -> Result<Command> {
    // Wrapper admission must complete before Windows resolves the executable:
    // `with-lock --on-locked skip` and rate-limit token consumption are defined
    // independently of whether a later workload spawn can succeed.
    #[cfg(windows)]
    let plan = windows_plan(plan.clone())?;
    #[cfg(not(windows))]
    let plan = plan.clone();
    let mut command = Command::new(&plan.command);
    command
        .args(plan.args.clone())
        .env_clear()
        .envs(plan.env.clone());
    Ok(command)
}

pub fn execute(args: Vec<OsString>) -> Result<ExitStatus> {
    let plan = prepare(args)?;
    runtime::delegated(command(&plan)?)
}

#[cfg(windows)]
fn windows_plan(mut plan: Plan) -> Result<Plan> {
    use std::path::{Path, PathBuf};
    // Cross-env normalizes only the executable, never the child arguments.
    let raw = plan.command.to_string_lossy().replace('/', "\\");
    let requested = Path::new(&raw);
    let extensions =
        lookup(&plan.env, "PATHEXT", true).unwrap_or(OsStr::new(".COM;.EXE;.BAT;.CMD"));
    let mut directories = vec![std::env::current_dir().map_err(|e| Failure::io(&e))?];
    directories.extend(std::env::split_paths(
        lookup(&plan.env, "PATH", true).unwrap_or_default(),
    ));
    let candidates: Vec<PathBuf> = if requested.components().count() > 1 || requested.is_absolute()
    {
        vec![requested.to_owned()]
    } else {
        directories.into_iter().map(|p| p.join(requested)).collect()
    };
    let mut found = None;
    'search: for candidate in candidates {
        if candidate.extension().is_some() && candidate.is_file() {
            found = Some(candidate);
            break;
        }
        for extension in extensions
            .to_string_lossy()
            .split(';')
            .filter(|s| !s.is_empty())
        {
            let path = PathBuf::from(format!("{}{extension}", candidate.display()));
            if path.is_file() {
                found = Some(path);
                break 'search;
            }
        }
    }
    let path = found.ok_or_else(|| {
        Failure::new(
            Code::SpawnFailed,
            "Executable was not found in the child PATH; check the installation and PATH \
             assignments.",
        )
    })?;
    if path
        .extension()
        .is_some_and(|e| e.eq_ignore_ascii_case("cmd") || e.eq_ignore_ascii_case("bat"))
    {
        // Expand only the simple environment-reference form used by cross-env before
        // std escapes the batch argv. Never concatenate a cmd.exe expression or use
        // raw_arg; expansion results themselves must remain literal argument data.
        let refs = Regex::new(r"%([A-Za-z0-9_]+)%").unwrap();
        for arg in &mut plan.args {
            *arg = refs
                .replace_all(&arg.to_string_lossy(), |c: &Captures<'_>| {
                    lookup(&plan.env, &c[1], true)
                        .map_or_else(|| c[0].to_owned(), |v| v.to_string_lossy().into_owned())
                })
                .into_owned()
                .into();
        }
    }
    plan.command = path.into_os_string();
    Ok(plan)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn args(values: &[&str]) -> Vec<OsString> {
        values.iter().map(OsString::from).collect()
    }
    fn parent() -> Environment {
        [
            ("BASE".into(), "original".into()),
            ("EMPTY".into(), "".into()),
        ]
        .into()
    }

    #[test]
    fn assignments_use_parent_not_earlier_setters_and_last_duplicate_wins() {
        let p = plan(
            args(&[
                "BASE=child",
                "OTHER=$BASE",
                "DUP=a",
                "DUP=",
                "cmd",
                "",
                "a b",
                "&&",
            ]),
            parent(),
            false,
        )
        .unwrap();
        assert_eq!(p.env[OsStr::new("BASE")], "child");
        assert_eq!(p.env[OsStr::new("OTHER")], "original");
        assert_eq!(p.env[OsStr::new("DUP")], "");
        assert_eq!(p.args, args(&["", "a b", "&&"]));
    }
    #[test]
    fn pinned_cross_env_value_fixtures() {
        for windows in [false, true] {
            for (raw, expected) in [
                ("$BASE/${MISSING}", "original/"),
                (r"\$BASE", "$BASE"),
                (r"\\$BASE", r"\original"),
                (r"\\\$BASE", "$BASE"),
                ("🦀 $EMPTY", "🦀 "),
            ] {
                assert_eq!(value(raw, "VAR", &parent(), windows), expected);
            }
            assert_eq!(
                value(r"a:b\:c", "PATH", &parent(), windows),
                if windows { "a;b:c" } else { "a:b:c" }
            );
            assert_eq!(value("a:b", "OTHER", &parent(), windows), "a:b");
        }
    }
    #[test]
    fn command_conversion_preserves_tokenized_arguments() {
        let p = plan(
            args(&[
                "FOO=bar",
                "./cmd",
                "$FOO",
                "${EMPTY:-fallback}",
                "$MISSING",
                "$1",
                r"\'quoted\'",
                r"a\\b",
            ]),
            parent(),
            true,
        )
        .unwrap();
        assert_eq!(
            p.args,
            args(&["%FOO%", "fallback", "", "", r"\'quoted\'", r"a\\b"])
        );
        let p = plan(
            args(&["cmd", "$BASE", "${EMPTY:-fallback}", "$1"]),
            parent(),
            false,
        )
        .unwrap();
        assert_eq!(p.args, args(&["$BASE", "${EMPTY:-fallback}", "$1"]));
        for windows in [false, true] {
            let literals = [
                "O'Reilly",
                r"a\\b",
                r"\\server\share\tool.exe",
                r#"\"quoted\""#,
            ];
            for command in ["O'Reilly", r"\\server\share\tool.exe"] {
                let mut tokens = vec![command];
                tokens.extend(literals);
                let p = plan(args(&tokens), parent(), windows).unwrap();
                assert_eq!(p.command, command);
                assert_eq!(p.args, args(&literals));
            }
        }
    }
    #[test]
    fn separator_and_missing_command() {
        assert!(plan(args(&["FOO=bar"]), parent(), false).is_err());
        assert!(plan(args(&["FOO=bar", ""]), parent(), false).is_err());
        let p = plan(args(&["--", "NAME=command", "x"]), parent(), false).unwrap();
        assert_eq!(p.command, "NAME=command");
    }

    #[cfg(windows)]
    #[test]
    fn prepare_defers_windows_executable_lookup() {
        let missing = format!(".\\clibox-missing-workload-{}", std::process::id());
        let prepared = prepare(args(&[&missing])).unwrap();

        assert_eq!(prepared.command, OsString::from(missing));
        assert!(command(&prepared).is_err());
    }
}
