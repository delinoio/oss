use tracing_subscriber::EnvFilter;

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
        .with_ansi(false)
        .with_target(false)
        .with_level(true)
        .without_time()
        .try_init();
}
