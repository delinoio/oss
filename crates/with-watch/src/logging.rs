use tracing_subscriber::EnvFilter;

const WITH_WATCH_LOG_COLOR_ENV: &str = "WITH_WATCH_LOG_COLOR";
const NO_COLOR_ENV: &str = "NO_COLOR";

pub fn init_logging() {
    let env_filter =
        EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("with_watch=info"));
    let _ = tracing_subscriber::fmt()
        .with_env_filter(env_filter)
        .with_ansi(log_color_enabled())
        .with_target(false)
        .with_level(true)
        .without_time()
        .try_init();
}

fn log_color_enabled() -> bool {
    resolve_log_color_enabled(
        std::env::var(WITH_WATCH_LOG_COLOR_ENV).ok().as_deref(),
        std::env::var(NO_COLOR_ENV).ok().as_deref(),
    )
}

fn resolve_log_color_enabled(with_watch_log_color: Option<&str>, no_color: Option<&str>) -> bool {
    match with_watch_log_color.and_then(parse_log_color_mode) {
        Some(LogColorMode::Always) => return true,
        Some(LogColorMode::Never) => return false,
        Some(LogColorMode::Auto) | None => {}
    }

    if no_color.is_some() {
        return false;
    }

    true
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
    use super::resolve_log_color_enabled;

    #[test]
    fn default_enables_colored_logs() {
        assert!(resolve_log_color_enabled(None, None));
    }

    #[test]
    fn override_can_disable_logs() {
        assert!(!resolve_log_color_enabled(Some("never"), None));
    }

    #[test]
    fn no_color_disables_logs_without_override() {
        assert!(!resolve_log_color_enabled(None, Some("1")));
    }
}
