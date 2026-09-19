use std::{
    path::Path,
    sync::atomic::{AtomicBool, Ordering},
};

use regex::Regex;

use crate::{
    config::Redaction,
    error::{Error, Result},
};

pub struct Redactor {
    root: String,
    home: Option<String>,
    temporary: Vec<String>,
    rules: Vec<Regex>,
    secret_values: Vec<String>,
    sensitive_flag: Regex,
    token: Regex,
    indices: Vec<usize>,
    pub path_redacted: AtomicBool,
}
impl Redactor {
    pub fn new(root: &Path, temporary: &[&Path], config: &Redaction) -> Result<Self> {
        let sensitive_flag = Regex::new(r"(?i)(token|password|passwd|secret|credential|authorization|api[-_]?key|private[-_]?key)").expect("static expression");
        let secret_values = std::env::vars()
            .filter(|(key, _)| {
                sensitive_flag.is_match(key) || config.environment_names.contains(key)
            })
            .map(|(_, value)| value)
            .filter(|v| !v.is_empty())
            .collect();
        Ok(Self { root: normalized(root), home: std::env::var_os(if cfg!(windows) {"USERPROFILE"} else {"HOME"}).map(|p| normalized(Path::new(&p))), temporary: temporary.iter().map(|p| normalized(p)).collect(), rules: config.patterns.iter().map(|p| Regex::new(p).map_err(|_| Error::input("invalid redaction pattern"))).collect::<Result<_>>()?, secret_values, sensitive_flag, token: Regex::new(r"(?i)(?:gh[pousr]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|AKIA[A-Z0-9]{16}|Bearer\s+[^\s]+|[a-z][a-z0-9+.-]*://[^\s/@]+:[^\s/@]+@)").expect("static expression"), indices: config.argument_indices.clone(), path_redacted: AtomicBool::new(false) })
    }

    pub fn text(&self, value: &str) -> String {
        let mut value = value.to_owned();
        for secret in &self.secret_values {
            value = value.replace(secret, "[redacted]");
        }
        value = self.token.replace_all(&value, "[redacted]").into_owned();
        for rule in &self.rules {
            value = rule.replace_all(&value, "[redacted]").into_owned();
        }
        for root in &self.temporary {
            value = value.replace(root, "${temporary}");
        }
        value = value.replace(&self.root, "${workspace}");
        if let Some(home) = &self.home {
            if home != "/" && !home.is_empty() {
                value = value.replace(home, "${home}");
            }
        }
        // Control characters must never control the user's terminal.
        value
            .chars()
            .flat_map(|c| {
                if c.is_control() {
                    c.escape_default().collect::<Vec<_>>()
                } else {
                    vec![c]
                }
            })
            .collect()
    }

    pub fn path(&self, path: &Path) -> String {
        let value = normalized(path);
        let redacted = self.text(&value);
        if redacted.contains("[redacted]") || path.to_str().is_none() {
            self.path_redacted.store(true, Ordering::Relaxed);
        }
        redacted
    }

    pub fn argv(&self, argv: &[String]) -> Vec<String> {
        let mut hide_next = false;
        argv.iter()
            .enumerate()
            .map(|(index, arg)| {
                let hide = std::mem::take(&mut hide_next) || self.indices.contains(&index);
                if hide {
                    return "[redacted]".into();
                }
                if arg.starts_with('-') && self.sensitive_flag.is_match(arg) {
                    if let Some((key, _)) = arg.split_once('=') {
                        return format!("{}=[redacted]", self.text(key));
                    }
                    hide_next = true;
                }
                self.text(arg)
            })
            .collect()
    }
}
pub fn normalized(path: &Path) -> String {
    path.to_string_lossy().replace('\\', "/")
}
