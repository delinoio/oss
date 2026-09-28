fn main() {
    // Detours' ordinal export syntax is accepted by MSVC link.exe only. The
    // repository ships MSVC Windows targets; GNU cross-checks skip this
    // packaging directive so Rust hook code can still be type-checked.
    if std::env::var_os("CARGO_CFG_TARGET_OS").as_deref() == Some(std::ffi::OsStr::new("windows"))
        && std::env::var_os("CARGO_CFG_TARGET_ENV").as_deref() == Some(std::ffi::OsStr::new("msvc"))
    {
        println!("cargo:rustc-cdylib-link-arg=/EXPORT:DetourFinishHelperProcess,@1,NONAME");
    }
}
