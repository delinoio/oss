use tracing_subscriber::EnvFilter;

pub fn init_logging() {
    let env_filter =
        EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("cargo_mono=info"));
    let _ = tracing_subscriber::fmt()
        .with_env_filter(env_filter)
        .with_ansi(false)
        .with_target(false)
        .with_level(true)
        .without_time()
        .try_init();
}
