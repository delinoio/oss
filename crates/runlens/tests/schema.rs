use std::path::Path;

#[test]
fn public_schemas_reproduce_without_drift() {
    for (name, schema) in [
        (
            "config-v1.json",
            schemars::schema_for!(runlens::config::Config),
        ),
        (
            "report-v1.json",
            schemars::schema_for!(runlens::model::Report),
        ),
    ] {
        let path = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("schema")
            .join(name);
        let mut schema = serde_json::to_value(schema).unwrap();
        schema["$id"] = format!("https://runlens.delino.io/schema/{name}").into();
        schema["properties"]["schema_version"]["const"] = 1.into();
        let contents = serde_json::to_string_pretty(&schema).unwrap() + "\n";
        if std::env::var_os("RUNLENS_UPDATE_SCHEMAS").is_some() {
            std::fs::create_dir_all(path.parent().unwrap()).unwrap();
            std::fs::write(&path, &contents).unwrap();
        }
        assert_eq!(std::fs::read_to_string(path).unwrap(), contents);
    }
}
