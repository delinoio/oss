const TEST_EXTENSION_ID: &str = "lmillpebkoiadcjhfimemdbcdhpafhgg";

fn valid_extension_id(value: &str) -> bool {
    value.len() == 32 && value.bytes().all(|byte| (b'a'..=b'p').contains(&byte))
}

fn main() {
    println!("cargo:rerun-if-env-changed=DEVHUD_CHROME_EXTENSION_ID");
    println!("cargo:rerun-if-env-changed=DEVHUD_EXTENSION_TEST_BUILD");
    let profile = std::env::var("PROFILE").unwrap_or_default();
    let test_build = std::env::var_os("DEVHUD_EXTENSION_TEST_BUILD").is_some();
    let extension_id = std::env::var("DEVHUD_CHROME_EXTENSION_ID").unwrap_or_else(|_| {
        assert!(
            profile != "release" || test_build,
            "release builds require DEVHUD_CHROME_EXTENSION_ID"
        );
        TEST_EXTENSION_ID.to_string()
    });
    assert!(
        valid_extension_id(&extension_id),
        "DEVHUD_CHROME_EXTENSION_ID must be 32 lowercase letters a-p"
    );
    println!("cargo:rustc-env=DEVHUD_CHROME_EXTENSION_ID={extension_id}");
}
