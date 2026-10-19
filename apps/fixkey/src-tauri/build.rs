fn main() {
    #[cfg(feature = "desktop")]
    {
        // Native tests/checks do not assemble distributions. Tauri's build hook
        // generates full dependency notices before every supported package
        // build.
        let directory = std::path::Path::new("gen/licenses");
        if std::env::var("PROFILE").as_deref() == Ok("release")
            && !directory.join("inventory.json").is_file()
        {
            panic!(
                "Generate FixKey dependency notices through the Tauri package build before \
                 release compilation"
            );
        }
        std::fs::create_dir_all(directory).expect("FixKey generated resource directory");
        tauri_build::build();
    }
}
