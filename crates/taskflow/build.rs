fn main() {
    println!("cargo:rerun-if-changed=native/supervisor.c");
    let os = std::env::var("CARGO_CFG_TARGET_OS").unwrap();
    if os != "linux" && os != "macos" {
        return;
    }
    let output =
        std::path::PathBuf::from(std::env::var_os("OUT_DIR").unwrap()).join("taskflow-supervisor");
    let mut compiler = cc::Build::new().get_compiler().to_command();
    compiler.args(["-std=c11", "-O2", "-Wall", "-Wextra", "-Werror"]);
    if os == "macos" {
        compiler.arg("-mmacosx-version-min=13.0");
    }
    compiler.arg("native/supervisor.c").arg("-o").arg(output);
    assert!(
        compiler
            .status()
            .expect("start native supervisor compiler")
            .success(),
        "compile native process supervisor"
    );
}
