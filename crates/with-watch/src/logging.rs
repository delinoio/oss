use tracing_subscriber::EnvFilter;

const WW_LOG_ENV: &str = "WW_LOG";
const WITH_WATCH_LOG_COLOR_ENV: &str = "WITH_WATCH_LOG_COLOR";
const NO_COLOR_ENV: &str = "NO_COLOR";
const DEFAULT_LOG_FILTER: &str = "with_watch=off";

pub fn init_logging() {
    let env_filter = resolve_env_filter_from_environment();
    let _ = tracing_subscriber::fmt()
        .with_env_filter(env_filter)
        .with_ansi(log_color_enabled())
        .with_target(false)
        .with_level(true)
        .without_time()
        .try_init();
}

fn resolve_env_filter_from_environment() -> EnvFilter {
    resolve_env_filter(std::env::var(WW_LOG_ENV).ok().as_deref())
}

fn resolve_env_filter(ww_log: Option<&str>) -> EnvFilter {
    let filter = ww_log
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .unwrap_or(DEFAULT_LOG_FILTER);

    EnvFilter::try_new(filter).unwrap_or_else(|_| EnvFilter::new(DEFAULT_LOG_FILTER))
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
    use std::{
        io::{self, Write},
        sync::{Arc, Mutex, OnceLock},
    };

    use tracing::{debug, info};

    use super::{
        resolve_env_filter, resolve_env_filter_from_environment, resolve_log_color_enabled,
        EnvFilter, DEFAULT_LOG_FILTER, WW_LOG_ENV,
    };

    const RUST_LOG_ENV: &str = "RUST_LOG";

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

    #[test]
    fn default_filter_disables_info_logs() {
        let output = capture_logs(resolve_env_filter(None), || {
            info!("hidden");
        });

        assert!(output.is_empty());
    }

    #[test]
    fn blank_ww_log_uses_default_filter() {
        let output = capture_logs(resolve_env_filter(Some("   ")), || {
            info!("hidden");
        });

        assert!(
            output.is_empty(),
            "blank WW_LOG should fall back to {DEFAULT_LOG_FILTER}"
        );
    }

    #[test]
    fn ww_log_can_enable_info_logs() {
        let output = capture_logs(resolve_env_filter(Some("with_watch=info")), || {
            info!("visible");
            debug!("still hidden");
        });

        assert!(output.contains("visible"));
        assert!(!output.contains("still hidden"));
    }

    #[test]
    fn ww_log_can_enable_debug_logs() {
        let output = capture_logs(resolve_env_filter(Some("with_watch=debug")), || {
            debug!("visible");
        });

        assert!(output.contains("visible"));
    }

    #[test]
    fn invalid_ww_log_falls_back_to_default_filter() {
        let output = capture_logs(resolve_env_filter(Some("not a valid filter[")), || {
            info!("hidden");
        });

        assert!(output.is_empty());
    }

    #[test]
    fn rust_log_is_ignored_when_resolving_environment_filter() {
        with_logging_environment(None, Some("with_watch=debug"), || {
            let output = capture_logs(resolve_env_filter_from_environment(), || {
                info!("hidden");
            });

            assert!(output.is_empty());
        });
    }

    #[test]
    fn ww_log_from_environment_overrides_default_off() {
        with_logging_environment(Some("with_watch=info"), Some("with_watch=off"), || {
            let output = capture_logs(resolve_env_filter_from_environment(), || {
                info!("visible");
            });

            assert!(output.contains("visible"));
        });
    }

    fn capture_logs(env_filter: EnvFilter, callback: impl FnOnce()) -> String {
        let buffer = Arc::new(Mutex::new(Vec::new()));
        let writer = SharedWriter(buffer.clone());
        let subscriber = tracing_subscriber::fmt()
            .with_env_filter(env_filter)
            .with_ansi(false)
            .with_target(false)
            .with_level(false)
            .without_time()
            .with_writer(move || writer.clone())
            .finish();

        tracing::subscriber::with_default(subscriber, callback);

        let output = buffer.lock().expect("lock log buffer").clone();
        String::from_utf8(output).expect("utf8 log output")
    }

    fn with_logging_environment<T>(
        ww_log: Option<&str>,
        rust_log: Option<&str>,
        callback: impl FnOnce() -> T,
    ) -> T {
        let _guard = environment_lock().lock().expect("lock logging env");

        let original_ww_log = std::env::var_os(WW_LOG_ENV);
        let original_rust_log = std::env::var_os(RUST_LOG_ENV);

        set_optional_env(WW_LOG_ENV, ww_log);
        set_optional_env(RUST_LOG_ENV, rust_log);

        let result = callback();

        restore_optional_env(WW_LOG_ENV, original_ww_log);
        restore_optional_env(RUST_LOG_ENV, original_rust_log);

        result
    }

    fn environment_lock() -> &'static Mutex<()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(()))
    }

    fn set_optional_env(key: &str, value: Option<&str>) {
        match value {
            Some(value) => std::env::set_var(key, value),
            None => std::env::remove_var(key),
        }
    }

    fn restore_optional_env(key: &str, value: Option<std::ffi::OsString>) {
        match value {
            Some(value) => std::env::set_var(key, value),
            None => std::env::remove_var(key),
        }
    }

    #[derive(Clone)]
    struct SharedWriter(Arc<Mutex<Vec<u8>>>);

    impl Write for SharedWriter {
        fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
            self.0
                .lock()
                .expect("lock log buffer")
                .extend_from_slice(buf);
            Ok(buf.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }
}
