//! Collection file written by the engine adapters (`collectVersion: 1`).

use std::collections::BTreeMap;

use serde::Deserialize;

pub const COLLECT_VERSION: u32 = 1;

#[derive(Debug, Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct Collection {
    pub collect_version: u32,
    pub bundler: String,
    pub bundler_package: String,
    pub bundler_version: String,
    pub minifier: String,
    pub minifier_version: String,
    pub default_minifier: bool,
    pub minify_enabled: bool,
    pub adapter_id: String,
    pub adapter_version: String,
    pub overrides: Vec<String>,
    pub config_file: Option<String>,
    pub context: Option<String>,
    pub config_summary: BTreeMap<String, String>,
    pub library_build: bool,
    pub ssr_build: bool,
    pub build_dir: String,
    pub warnings: Vec<String>,
    pub errors: Vec<String>,
    pub logs: Vec<EngineLog>,
    pub modules: Vec<CollectedModule>,
    pub chunks: Vec<CollectedChunk>,
    pub assets: Vec<String>,
}

#[derive(Debug, Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct EngineLog {
    pub level: String,
    pub code: Option<String>,
    pub message: String,
    pub id: Option<String>,
    pub line: Option<u32>,
    pub column: Option<u32>,
}

#[derive(Debug, Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CollectedModule {
    pub id: String,
    pub name: Option<String>,
    pub path: Option<String>,
    #[serde(rename = "virtual")]
    pub is_virtual: bool,
    pub is_entry: bool,
    pub importers: Vec<String>,
    pub dynamic_importers: Vec<String>,
    pub imported_ids: Vec<String>,
    pub dynamic_imported_ids: Vec<String>,
    pub exports: Option<Vec<String>>,
    pub used_exports: Option<Vec<String>>,
    pub module_side_effects: Option<String>,
    pub input_format: Option<String>,
    pub module_type: Option<String>,
    pub rendered_length: u64,
    pub included: bool,
    pub chunks: Vec<String>,
    pub bailouts: Vec<String>,
    pub reasons: Vec<CollectedReason>,
    pub depth: Option<u32>,
    pub concatenated_into: Option<String>,
    pub package_name: Option<String>,
    pub package_version: Option<String>,
    pub package_dir: Option<String>,
    pub package_side_effects: Option<String>,
}

#[derive(Debug, Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CollectedReason {
    #[serde(rename = "type")]
    pub kind: Option<String>,
    pub from: Option<String>,
    pub loc: Option<String>,
    pub explanation: Option<String>,
    pub active: bool,
}

#[derive(Debug, Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CollectedChunk {
    pub file: String,
    pub is_entry: bool,
    pub is_dynamic_entry: bool,
    pub facade_module_id: Option<String>,
    pub modules: Vec<String>,
    pub map: Option<String>,
}
