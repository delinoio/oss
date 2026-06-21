use tracing_subscriber::EnvFilter;

pub const NODEUP_LOG_COLOR_ENV: &str = "NODEUP_LOG_COLOR";
const NO_COLOR_ENV: &str = crate::output_style::NO_COLOR_ENV;

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
            Self::ManagementHuman => "nodeup=warn",
            Self::ManagementJson => "nodeup=off",
        }
    }
}

pub fn init_logging(context: LoggingContext) {
    let env_filter = EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| EnvFilter::new(context.default_filter()));
    let _ = tracing_subscriber::fmt()
        .pretty()
        .with_writer(std::io::stderr)
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

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LogColorDecision {
    pub enabled: bool,
    pub mode: &'static str,
    pub source: &'static str,
    pub no_color_present: bool,
    pub ignored_nodeup_log_color: Option<String>,
}

pub fn log_color_decision() -> LogColorDecision {
    let raw_log_color = std::env::var(NODEUP_LOG_COLOR_ENV).ok();
    let parsed_log_color = raw_log_color.as_deref().and_then(parse_log_color_mode);
    let ignored_nodeup_log_color =
        raw_log_color.filter(|value| parse_log_color_mode(value).is_none());
    let no_color_present = std::env::var_os(NO_COLOR_ENV).is_some();
    let (mode, source) = resolve_log_color_mode(parsed_log_color, no_color_present);

    LogColorDecision {
        enabled: resolve_log_color_enabled_for_mode(parsed_log_color, no_color_present),
        mode: mode.as_str(),
        source,
        no_color_present,
        ignored_nodeup_log_color,
    }
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

fn resolve_log_color_mode(
    nodeup_log_color: Option<LogColorMode>,
    no_color_present: bool,
) -> (LogColorMode, &'static str) {
    match nodeup_log_color {
        Some(LogColorMode::Always) => (LogColorMode::Always, NODEUP_LOG_COLOR_ENV),
        Some(LogColorMode::Never) => (LogColorMode::Never, NODEUP_LOG_COLOR_ENV),
        Some(LogColorMode::Auto) => (LogColorMode::Auto, NODEUP_LOG_COLOR_ENV),
        None => {
            if no_color_present {
                (LogColorMode::Never, NO_COLOR_ENV)
            } else {
                (LogColorMode::Always, "default")
            }
        }
    }
}

fn resolve_log_color_enabled_for_mode(
    nodeup_log_color: Option<LogColorMode>,
    no_color_present: bool,
) -> bool {
    match nodeup_log_color {
        Some(LogColorMode::Always) => true,
        Some(LogColorMode::Never) => false,
        Some(LogColorMode::Auto) | None => !no_color_present,
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum LogColorMode {
    Auto,
    Always,
    Never,
}

impl LogColorMode {
    fn as_str(self) -> &'static str {
        match self {
            Self::Auto => "auto",
            Self::Always => "always",
            Self::Never => "never",
        }
    }
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
    fn management_human_default_filter_is_warn() {
        assert_eq!(
            LoggingContext::ManagementHuman.default_filter(),
            "nodeup=warn"
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
