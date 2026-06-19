fn main() {
    if let Ok(target) = std::env::var("TARGET") {
        println!("cargo:rustc-env=BINPM_TARGET_TRIPLE={target}");
    }
}
