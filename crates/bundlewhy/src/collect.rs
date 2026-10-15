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
    /// Names the plugin output hook that turned the selected minifier off,
    /// when one did. Collections written before the field existed, and engines
    /// that do not report it, attribute nothing to a hook.
    pub minify_override: MinifyOverride,
    /// Effective output name-mangling setting, read from the finalized output
    /// options after plugin hooks ran. Collections written before the field
    /// existed, and engines that do not report it (Rspack), assume mangling.
    pub mangle: ManglingSetting,
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
    /// Export-to-export usage edges that the engine reported. Absent for
    /// engines and adapter versions that do not collect them.
    pub export_usage: Option<CollectedExportUsage>,
}

/// Which plugin output hook turned the selected Oxc minifier off.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum MinifyOverride {
    /// `build.minify` or the engine configuration decided, or minification
    /// runs.
    #[default]
    None,
    /// A hook set the final output `minify` option to `false`.
    OutputHookDisabled,
    /// A hook set the final output `minify` option to `"dce-only"`, which
    /// keeps dead-code elimination but skips compression and name mangling.
    OutputHookDceOnly,
}

/// Whether the minifier mangles names for the analyzed output.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ManglingSetting {
    /// Mangling runs, or the collection does not say otherwise.
    #[default]
    Enabled,
    /// The build's final output options disable mangling, so names stay
    /// readable regardless of the source.
    Disabled,
}

/// Export usage graph collected from the engine (Rspack's `RsdoctorPlugin`).
#[derive(Debug, Clone, Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CollectedExportUsage {
    /// False when the engine API, hook or graph could not be used.
    pub available: bool,
    /// Why the graph is unavailable: a stable code, never adapter or engine
    /// error text.
    pub reason: Option<ExportUsageReason>,
    /// Engine edges that were dropped because a module could not be mapped or
    /// an export reference could not be read.
    pub skipped_edges: u32,
    /// Dropped edges per collected provider export: each entry says that an
    /// edge into that export (or into an unreadable export of that module)
    /// exists but cannot be shown, so a chain that ends at a matching export
    /// has not reached a confirmed start.
    pub skipped_providers: Vec<CollectedSkippedProvider>,
    /// Dropped edges whose provider module could not be determined at all.
    /// Such an edge may be an earlier edge of any chain.
    pub unattributed_edges: u32,
    pub edges: Vec<CollectedExportEdge>,
}

/// Edges dropped by the adapter that target one collected module and one
/// export (or an export that could not be read).
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CollectedSkippedProvider {
    pub module: String,
    /// What the adapter could tell about the provider export. A missing or
    /// unrecognized value is `Unknown`, the conservative reading.
    pub scope: SkippedProviderScope,
    /// Export path segments; meaningful only when `scope` is `Named`.
    pub export: Vec<String>,
    pub edges: u32,
}

/// Which export of the provider module a dropped edge targeted.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum SkippedProviderScope {
    /// The dropped edge named one export, given by `export`.
    Named,
    /// The dropped edge targeted module or namespace scope.
    Module,
    /// The export could not be read: the edge may have targeted any export.
    #[default]
    #[serde(other)]
    Unknown,
}

/// Stable codes for export usage that the adapter could not collect. The
/// analysis shows fixed text for each code; an unrecognized code (for example
/// from a newer adapter) maps to `Unknown`, so no free text is ever displayed.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ExportUsageReason {
    PluginApiMissing,
    PluginAlreadyConfigured,
    PluginsNotArray,
    PluginConstructionFailed,
    PluginApplyFailed,
    HookRegistrationFailed,
    BuildFailedWithObserver,
    GraphNotDelivered,
    ModulesPayloadInvalid,
    EdgesPayloadInvalid,
    EdgeLimitExceeded,
    #[serde(other)]
    Unknown,
}

/// One engine-reported edge: `consumer` uses `provider`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CollectedExportEdge {
    pub consumer: CollectedExportRef,
    pub provider: CollectedExportRef,
    pub dependency_id: Option<String>,
    pub location: Option<String>,
}

/// A module plus an export path inside it. A `None` export is module or
/// namespace scope: the engine did not name one export.
#[derive(Debug, Clone, Default, PartialEq, Eq, Hash, PartialOrd, Ord, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct CollectedExportRef {
    pub module: String,
    pub export: Option<Vec<String>>,
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
    /// The `moduleSideEffects` value that a JavaScript resolve, load or
    /// transform hook set (`getModuleInfo()`); values from native plugins such
    /// as Vite's resolver are not visible here.
    pub module_side_effects: Option<String>,
    /// What the configured `treeshake.moduleSideEffects` decides for the
    /// module: `true`, `false`, `null` (no verdict) or a value summary.
    pub treeshake_side_effects: Option<String>,
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
