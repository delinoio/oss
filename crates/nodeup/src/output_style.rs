use std::io::IsTerminal;

use crate::cli::OutputColorMode;

pub const NODEUP_COLOR_ENV: &str = "NODEUP_COLOR";
pub const NO_COLOR_ENV: &str = "NO_COLOR";

const ANSI_RESET: &str = "\u{1b}[0m";
const ANSI_BOLD: &str = "\u{1b}[1m";
const ANSI_BOLD_CYAN: &str = "\u{1b}[1;36m";
const ANSI_BOLD_RED: &str = "\u{1b}[1;31m";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum OutputStream {
    Stdout,
    Stderr,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OutputColorDecision {
    pub stream: &'static str,
    pub enabled: bool,
    pub mode: OutputColorMode,
    pub source: &'static str,
    pub is_terminal: bool,
    pub no_color_present: bool,
    pub ignored_nodeup_color: Option<String>,
}

pub fn style_human_stdout(human_line: &str, color_flag: Option<OutputColorMode>) -> String {
    style_human_stdout_with_terminal_detection(
        human_line,
        color_flag,
        std::io::stdout().is_terminal(),
    )
}

pub fn style_human_error(message: &str, color_flag: Option<OutputColorMode>) -> String {
    style_human_error_with_terminal_detection(message, color_flag, std::io::stderr().is_terminal())
}

pub fn parse_output_color_mode(raw: &str) -> Option<OutputColorMode> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "auto" => Some(OutputColorMode::Auto),
        "always" => Some(OutputColorMode::Always),
        "never" => Some(OutputColorMode::Never),
        _ => None,
    }
}

fn style_human_stdout_with_terminal_detection(
    human_line: &str,
    color_flag: Option<OutputColorMode>,
    stdout_is_terminal: bool,
) -> String {
    if human_line.is_empty() {
        return String::new();
    }

    if !resolve_output_color_enabled_for_stream(
        OutputStream::Stdout,
        color_flag,
        stdout_is_terminal,
        false,
    ) {
        return human_line.to_string();
    }

    format!("{ANSI_BOLD_CYAN}{human_line}{ANSI_RESET}")
}

fn style_human_error_with_terminal_detection(
    message: &str,
    color_flag: Option<OutputColorMode>,
    stderr_is_terminal: bool,
) -> String {
    if !resolve_output_color_enabled_for_stream(
        OutputStream::Stderr,
        color_flag,
        false,
        stderr_is_terminal,
    ) {
        return format!("nodeup error: {message}");
    }

    format!("{ANSI_BOLD_RED}nodeup error:{ANSI_RESET} {ANSI_BOLD}{message}{ANSI_RESET}")
}

fn resolve_output_color_enabled_for_stream(
    stream: OutputStream,
    color_flag: Option<OutputColorMode>,
    stdout_is_terminal: bool,
    stderr_is_terminal: bool,
) -> bool {
    let env_mode = std::env::var(NODEUP_COLOR_ENV)
        .ok()
        .as_deref()
        .and_then(parse_output_color_mode);
    let no_color = std::env::var_os(NO_COLOR_ENV).is_some();
    let is_terminal = match stream {
        OutputStream::Stdout => stdout_is_terminal,
        OutputStream::Stderr => stderr_is_terminal,
    };

    resolve_output_color_enabled(color_flag, env_mode, no_color, is_terminal)
}

pub fn stdout_color_decision(color_flag: Option<OutputColorMode>) -> OutputColorDecision {
    output_color_decision_for_stream(
        OutputStream::Stdout,
        color_flag,
        std::io::stdout().is_terminal(),
        false,
    )
}

pub fn stderr_color_decision(color_flag: Option<OutputColorMode>) -> OutputColorDecision {
    output_color_decision_for_stream(
        OutputStream::Stderr,
        color_flag,
        false,
        std::io::stderr().is_terminal(),
    )
}

fn output_color_decision_for_stream(
    stream: OutputStream,
    color_flag: Option<OutputColorMode>,
    stdout_is_terminal: bool,
    stderr_is_terminal: bool,
) -> OutputColorDecision {
    let raw_env_mode = std::env::var(NODEUP_COLOR_ENV).ok();
    let env_mode = raw_env_mode.as_deref().and_then(parse_output_color_mode);
    let ignored_nodeup_color =
        raw_env_mode.filter(|value| parse_output_color_mode(value).is_none());
    let no_color_present = std::env::var_os(NO_COLOR_ENV).is_some();
    let is_terminal = match stream {
        OutputStream::Stdout => stdout_is_terminal,
        OutputStream::Stderr => stderr_is_terminal,
    };
    let (mode, source) = resolve_output_color_mode(color_flag, env_mode, no_color_present);
    let enabled = match mode {
        OutputColorMode::Always => true,
        OutputColorMode::Never => false,
        OutputColorMode::Auto => is_terminal,
    };

    OutputColorDecision {
        stream: match stream {
            OutputStream::Stdout => "human-stdout",
            OutputStream::Stderr => "human-stderr",
        },
        enabled,
        mode,
        source,
        is_terminal,
        no_color_present,
        ignored_nodeup_color,
    }
}

fn resolve_output_color_mode(
    color_flag: Option<OutputColorMode>,
    env_mode: Option<OutputColorMode>,
    no_color: bool,
) -> (OutputColorMode, &'static str) {
    if let Some(mode) = color_flag {
        return (mode, "--color");
    }

    if let Some(mode) = env_mode {
        return (mode, NODEUP_COLOR_ENV);
    }

    if no_color {
        return (OutputColorMode::Never, NO_COLOR_ENV);
    }

    (OutputColorMode::Auto, "auto")
}

fn resolve_output_color_enabled(
    color_flag: Option<OutputColorMode>,
    env_mode: Option<OutputColorMode>,
    no_color: bool,
    is_terminal: bool,
) -> bool {
    match color_flag.or(env_mode).unwrap_or(OutputColorMode::Auto) {
        OutputColorMode::Always => true,
        OutputColorMode::Never => false,
        OutputColorMode::Auto => {
            if no_color {
                return false;
            }

            is_terminal
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{
        parse_output_color_mode, resolve_output_color_enabled,
        style_human_error_with_terminal_detection, style_human_stdout_with_terminal_detection,
    };
    use crate::cli::OutputColorMode;

    #[test]
    fn parse_output_color_mode_accepts_stable_values() {
        assert_eq!(parse_output_color_mode("auto"), Some(OutputColorMode::Auto));
        assert_eq!(
            parse_output_color_mode("always"),
            Some(OutputColorMode::Always)
        );
        assert_eq!(
            parse_output_color_mode("never"),
            Some(OutputColorMode::Never)
        );
    }

    #[test]
    fn parse_output_color_mode_rejects_invalid_values() {
        assert_eq!(parse_output_color_mode("on"), None);
        assert_eq!(parse_output_color_mode("off"), None);
    }

    #[test]
    fn flag_precedence_overrides_env() {
        assert!(!resolve_output_color_enabled(
            Some(OutputColorMode::Never),
            Some(OutputColorMode::Always),
            false,
            true,
        ));
        assert!(resolve_output_color_enabled(
            Some(OutputColorMode::Always),
            Some(OutputColorMode::Never),
            false,
            false,
        ));
    }

    #[test]
    fn env_precedence_overrides_no_color_for_always() {
        assert!(resolve_output_color_enabled(
            None,
            Some(OutputColorMode::Always),
            true,
            false,
        ));
    }

    #[test]
    fn auto_mode_respects_no_color_and_terminal_detection() {
        assert!(!resolve_output_color_enabled(
            None,
            Some(OutputColorMode::Auto),
            true,
            true,
        ));
        assert!(!resolve_output_color_enabled(
            None,
            Some(OutputColorMode::Auto),
            false,
            false,
        ));
        assert!(resolve_output_color_enabled(
            None,
            Some(OutputColorMode::Auto),
            false,
            true,
        ));
    }

    #[test]
    fn stdout_style_uses_ansi_when_enabled() {
        let styled = style_human_stdout_with_terminal_detection(
            "Active runtime: v22.1.0",
            Some(OutputColorMode::Always),
            false,
        );
        assert!(styled.starts_with("\u{1b}[1;36m"));
        assert!(styled.ends_with("\u{1b}[0m"));
    }

    #[test]
    fn stdout_style_keeps_plain_text_when_disabled() {
        let plain = style_human_stdout_with_terminal_detection(
            "Active runtime: v22.1.0",
            Some(OutputColorMode::Never),
            true,
        );
        assert_eq!(plain, "Active runtime: v22.1.0");
    }

    #[test]
    fn stderr_style_formats_error_label_when_enabled() {
        let styled = style_human_error_with_terminal_detection(
            "No runtime selector resolved. Hint: set default.",
            Some(OutputColorMode::Always),
            false,
        );
        assert!(styled.contains("\u{1b}[1;31mnodeup error:\u{1b}[0m"));
        assert!(
            styled.contains("\u{1b}[1mNo runtime selector resolved. Hint: set default.\u{1b}[0m")
        );
    }

    #[test]
    fn stderr_style_keeps_plain_error_when_disabled() {
        let plain = style_human_error_with_terminal_detection(
            "No runtime selector resolved. Hint: set default.",
            Some(OutputColorMode::Never),
            true,
        );
        assert_eq!(
            plain,
            "nodeup error: No runtime selector resolved. Hint: set default."
        );
    }
}
