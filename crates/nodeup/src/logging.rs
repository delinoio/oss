use tracing_subscriber::EnvFilter;

const NODEUP_LOG_COLOR_ENV: &str = "NODEUP_LOG_COLOR";
const NO_COLOR_ENV: &str = "NO_COLOR";
const CLICOLOR_ENV: &str = "CLICOLOR";
const CLICOLOR_FORCE_ENV: &str = "CLICOLOR_FORCE";

pub fn init_logging(json_error_output_requested: bool) {
    let default_filter = if json_error_output_requested {
        "nodeup=off"
    } else {
        "nodeup=info"
    };
    let env_filter =
        EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new(default_filter));
    let _ = tracing_subscriber::fmt()
        .pretty()
        .with_env_filter(env_filter)
        .with_ansi(log_color_enabled())
        .with_target(false)
        .with_level(true)
        .without_time()
        .try_init();
}

fn log_color_enabled() -> bool {
    resolve_log_color_enabled(
        std::env::var(NODEUP_LOG_COLOR_ENV).ok().as_deref(),
        std::env::var(CLICOLOR_FORCE_ENV).ok().as_deref(),
        std::env::var(NO_COLOR_ENV).ok().as_deref(),
        std::env::var(CLICOLOR_ENV).ok().as_deref(),
    )
}

fn resolve_log_color_enabled(
    nodeup_log_color: Option<&str>,
    clicolor_force: Option<&str>,
    no_color: Option<&str>,
    clicolor: Option<&str>,
) -> bool {
    match nodeup_log_color.and_then(parse_log_color_mode) {
        Some(LogColorMode::Always) => return true,
        Some(LogColorMode::Never) => return false,
        Some(LogColorMode::Auto) | None => {}
    }

    if clicolor_force.is_some_and(|raw| raw.trim() != "0") {
        return true;
    }

    if no_color.is_some() {
        return false;
    }

    if clicolor.is_some_and(|raw| raw.trim() == "0") {
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
        assert!(resolve_log_color_enabled(None, None, None, None));
    }

    #[test]
    fn nodeup_override_can_disable_logs() {
        assert!(!resolve_log_color_enabled(
            Some("never"),
            Some("1"),
            None,
            None
        ));
    }

    #[test]
    fn nodeup_override_can_force_logs() {
        assert!(resolve_log_color_enabled(
            Some("always"),
            None,
            Some("1"),
            Some("0")
        ));
    }

    #[test]
    fn auto_override_respects_global_disable() {
        assert!(!resolve_log_color_enabled(
            Some("auto"),
            None,
            Some("1"),
            None
        ));
    }

    #[test]
    fn clicolor_force_overrides_no_color() {
        assert!(resolve_log_color_enabled(
            None,
            Some("1"),
            Some("1"),
            Some("0")
        ));
    }

    #[test]
    fn clicolor_zero_disables_logs_without_force() {
        assert!(!resolve_log_color_enabled(None, None, None, Some("0")));
    }

    #[test]
    fn invalid_override_falls_back_to_standard_envs() {
        assert!(!resolve_log_color_enabled(
            Some("invalid"),
            None,
            Some("1"),
            None
        ));
    }
}
