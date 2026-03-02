use tracing_subscriber::EnvFilter;

const NODEUP_LOG_COLOR_ENV: &str = "NODEUP_LOG_COLOR";
const NO_COLOR_ENV: &str = "NO_COLOR";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LoggingContext {
    ManagedAlias,
    ManagementHuman,
    ManagementJson,
}

impl LoggingContext {
    fn default_filter(self) -> &'static str {
        match self {
            Self::ManagedAlias => "nodeup=warn",
            Self::ManagementHuman => "nodeup=info",
            Self::ManagementJson => "nodeup=off",
        }
    }
}

pub fn init_logging(context: LoggingContext) {
    let env_filter = EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| EnvFilter::new(context.default_filter()));
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
        std::env::var(NO_COLOR_ENV).ok().as_deref(),
    )
}

fn resolve_log_color_enabled(nodeup_log_color: Option<&str>, no_color: Option<&str>) -> bool {
    match nodeup_log_color.and_then(parse_log_color_mode) {
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
    use super::{resolve_log_color_enabled, LoggingContext};

    #[test]
    fn managed_alias_default_filter_is_warn() {
        assert_eq!(LoggingContext::ManagedAlias.default_filter(), "nodeup=warn");
    }

    #[test]
    fn management_human_default_filter_is_info() {
        assert_eq!(
            LoggingContext::ManagementHuman.default_filter(),
            "nodeup=info"
        );
    }

    #[test]
    fn management_json_default_filter_is_off() {
        assert_eq!(
            LoggingContext::ManagementJson.default_filter(),
            "nodeup=off"
        );
    }

    #[test]
    fn default_enables_colored_logs() {
        assert!(resolve_log_color_enabled(None, None));
    }

    #[test]
    fn nodeup_override_can_disable_logs() {
        assert!(!resolve_log_color_enabled(Some("never"), None));
    }

    #[test]
    fn nodeup_override_can_force_logs() {
        assert!(resolve_log_color_enabled(Some("always"), Some("1")));
    }

    #[test]
    fn auto_override_respects_global_disable() {
        assert!(!resolve_log_color_enabled(Some("auto"), Some("1")));
    }

    #[test]
    fn no_color_disables_logs_without_override() {
        assert!(!resolve_log_color_enabled(None, Some("1")));
    }

    #[test]
    fn invalid_override_falls_back_to_no_color() {
        assert!(!resolve_log_color_enabled(Some("invalid"), Some("1")));
    }
}
