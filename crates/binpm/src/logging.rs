use tracing_subscriber::EnvFilter;

const BINPM_LOG_ENV: &str = "BINPM_LOG";
const BINPM_LOG_COLOR_ENV: &str = "BINPM_LOG_COLOR";
const NO_COLOR_ENV: &str = "NO_COLOR";
const DEFAULT_LOG_FILTER: &str = "binpm=warn";

pub fn init_logging() {
    let env_filter = resolve_env_filter_from_environment();
    let _ = tracing_subscriber::fmt()
        .with_env_filter(env_filter)
        .with_writer(std::io::stderr)
        .with_ansi(log_color_enabled())
        .with_target(false)
        .with_level(true)
        .without_time()
        .try_init();
}

fn resolve_env_filter_from_environment() -> EnvFilter {
    resolve_env_filter(std::env::var(BINPM_LOG_ENV).ok().as_deref())
}

fn resolve_env_filter(binpm_log: Option<&str>) -> EnvFilter {
    let filter = binpm_log
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .unwrap_or(DEFAULT_LOG_FILTER);

    EnvFilter::try_new(filter).unwrap_or_else(|_| EnvFilter::new(DEFAULT_LOG_FILTER))
}

fn log_color_enabled() -> bool {
    resolve_log_color_enabled(
        std::env::var(BINPM_LOG_COLOR_ENV).ok().as_deref(),
        std::env::var(NO_COLOR_ENV).ok().as_deref(),
    )
}

fn resolve_log_color_enabled(binpm_log_color: Option<&str>, no_color: Option<&str>) -> bool {
    match binpm_log_color.and_then(parse_log_color_mode) {
        Some(LogColorMode::Always) => return true,
        Some(LogColorMode::Never) => return false,
        Some(LogColorMode::Auto) | None => {}
    }

    no_color.is_none()
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum LogColorMode {
    Auto,
    Always,
    Never,
}

fn parse_log_color_mode(raw: &str) -> Option<LogColorMode> {
    match raw.trim().to_ascii_lowercase().as_str() {
        "always" | "on" | "true" | "1" | "yes" => Some(LogColorMode::Always),
        "never" | "off" | "false" | "0" | "no" => Some(LogColorMode::Never),
        "auto" => Some(LogColorMode::Auto),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::{resolve_env_filter, resolve_log_color_enabled, DEFAULT_LOG_FILTER};

    #[test]
    fn default_filter_enables_warning_level_binpm_logs() {
        assert_eq!(resolve_env_filter(None).to_string(), DEFAULT_LOG_FILTER);
    }

    #[test]
    fn blank_filter_uses_default() {
        assert_eq!(
            resolve_env_filter(Some("  ")).to_string(),
            DEFAULT_LOG_FILTER
        );
    }

    #[test]
    fn color_can_be_disabled_by_no_color() {
        assert!(!resolve_log_color_enabled(None, Some("1")));
    }

    #[test]
    fn explicit_color_setting_overrides_no_color() {
        assert!(resolve_log_color_enabled(Some("always"), Some("1")));
        assert!(!resolve_log_color_enabled(Some("never"), None));
    }
}
