//! Collection file written by the engine adapters (`collectVersion: 1`).

use std::collections::BTreeMap;

use clap::ValueEnum;
use serde::Deserialize;

use crate::engine::BundlerChoice;

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
    /// Compression or name mangling runs, or the build names another
    /// minifier. Rolldown's dead-code-elimination-only mode is not
    /// minification, so it leaves this `false`.
    pub minify_enabled: bool,
    /// Whether dead-code elimination runs. Absent in collections from
    /// adapters that do not report it, which means it runs exactly when
    /// `minify_enabled` is set.
    pub dead_code_elimination: Option<Stage>,
    /// Whether code compression runs. Absent follows `minify_enabled`, like
    /// `dead_code_elimination`.
    pub compress: Option<Stage>,
    /// Effective output name-mangling setting. Absent in older collections,
    /// which keeps the default (`Enabled`).
    pub mangle: Mangle,
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

/// Exact module ids of the helpers that the Vite build itself injects (Vite's
/// `\0vite/preload-helper.js`, Rolldown's `\0rolldown/runtime.js`). Plugin
/// virtual module ids are user-controlled, so a namespace prefix such as
/// `\0vite/` or `\0rolldown/` proves nothing: only these verified ids are
/// bundler-owned. Every other virtual module, including `\0virtual:*` and
/// look-alike ids in these namespaces, keeps its source-map requirement.
const VITE_OWNED_VIRTUAL_IDS: [&str; 2] = ["\0vite/preload-helper.js", "\0rolldown/runtime.js"];

impl Collection {
    /// Whether the minifier's dead-code elimination runs for the analyzed
    /// output. Vite's `build.minify: false` still runs it (Rolldown's
    /// `'dce-only'` mode), so it can run while `minify_enabled` is `false`.
    pub fn dead_code_elimination_runs(&self) -> bool {
        self.dead_code_elimination
            .map_or(self.minify_enabled, |stage| stage == Stage::Enabled)
    }

    /// Whether code compression runs for the analyzed output.
    pub fn compress_runs(&self) -> bool {
        self.compress
            .map_or(self.minify_enabled, |stage| stage == Stage::Enabled)
    }

    /// Decodes the `bundler` field into the supported bundler. An unknown or
    /// empty value yields `None` and is treated as unsupported.
    pub fn bundler_choice(&self) -> Option<BundlerChoice> {
        BundlerChoice::from_str(&self.bundler, false).ok()
    }

    /// True when this is a Vite collection and the emitted chunk `file` lists
    /// at least one module and every listed module is a Vite-owned virtual
    /// helper with no source file. Such a chunk has no original source a map
    /// could attribute, so a missing map cannot hide a source-backed
    /// candidate. Only Vite collections qualify: Rspack and unsupported
    /// bundlers never treat a `\0vite/preload-helper.js` or
    /// `\0rolldown/runtime.js` id as bundler-owned and keep the map
    /// requirement for every chunk. Unknown ids, ids outside the
    /// verified helper table, modules with a file path and chunks with no
    /// listed modules are all treated as source-backed.
    pub fn is_virtual_only_chunk(&self, file: &str) -> bool {
        if self.bundler_choice() != Some(BundlerChoice::Vite) {
            return false;
        }
        let Some(chunk) = self.chunks.iter().find(|chunk| chunk.file == file) else {
            return false;
        };
        !chunk.modules.is_empty()
            && chunk.modules.iter().all(|id| {
                VITE_OWNED_VIRTUAL_IDS.contains(&id.as_str())
                    && self.modules.iter().any(|module| {
                        module.id == *id && module.is_virtual && module.path.is_none()
                    })
            })
    }
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

/// Whether one minifier stage (dead-code elimination, compression or name
/// mangling) runs for the analyzed output. An unrecognized value (for example
/// from a newer adapter) is treated as `Enabled`, which keeps every
/// stage-dependent detector active.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Stage {
    /// The configuration turns the stage off, so a missing optimization
    /// proves nothing about its cause.
    Disabled,
    #[default]
    #[serde(other)]
    Enabled,
}

/// Whether the minifier mangles local names for the analyzed output. When
/// `Disabled`, the configuration keeps every name, so retained names prove
/// nothing about their cause.
pub type Mangle = Stage;

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
    /// Chunk files this chunk imports statically (`OutputChunk.imports`).
    /// `None` when the adapter did not collect them (adapter version 1), which
    /// is different from a chunk that imports nothing (`Some` of an empty
    /// list).
    pub imports: Option<Vec<String>>,
    /// The entries of `imports` that name an external module. `imports` mixes
    /// emitted chunk files with external specifiers, and an external can share
    /// its name with an emitted chunk, so an entry listed here is not a trusted
    /// chunk edge. `None` when the adapter did not collect it.
    pub external_imports: Option<Vec<String>>,
    /// Chunk files this chunk requests with a dynamic `import()`
    /// (`OutputChunk.dynamicImports`). `None` when the adapter did not collect
    /// them.
    pub dynamic_imports: Option<Vec<String>>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn dead_code_elimination_and_compress_follow_minify_enabled_when_absent() {
        // Collections from adapters that do not report the stages (Rspack,
        // older Vite adapters) keep the previous meaning of `minifyEnabled`.
        let off: Collection = serde_json::from_str(r#"{"minifyEnabled":false}"#).unwrap();
        assert!(!off.dead_code_elimination_runs());
        assert!(!off.compress_runs());
        let on: Collection = serde_json::from_str(r#"{"minifyEnabled":true}"#).unwrap();
        assert!(on.dead_code_elimination_runs());
        assert!(on.compress_runs());
    }

    #[test]
    fn stages_are_independent_of_minify_enabled_and_of_each_other() {
        // Rolldown's `'dce-only'` mode: dead-code elimination without
        // compression or mangling.
        let dce_only: Collection = serde_json::from_str(
            r#"{"minifyEnabled":false,"deadCodeElimination":"enabled","compress":"disabled","mangle":"disabled"}"#,
        )
        .unwrap();
        assert!(dce_only.dead_code_elimination_runs());
        assert!(!dce_only.compress_runs());
        assert_eq!(dce_only.mangle, Mangle::Disabled);
        // `{ compress: false }`: mangling without compression or dead-code
        // elimination.
        let mangle_only: Collection = serde_json::from_str(
            r#"{"minifyEnabled":true,"deadCodeElimination":"disabled","compress":"disabled","mangle":"enabled"}"#,
        )
        .unwrap();
        assert!(!mangle_only.dead_code_elimination_runs());
        assert!(!mangle_only.compress_runs());
        assert_eq!(mangle_only.mangle, Mangle::Enabled);
        let unknown: Collection =
            serde_json::from_str(r#"{"deadCodeElimination":"partial","compress":"partial"}"#)
                .unwrap();
        assert!(unknown.dead_code_elimination_runs());
        assert!(unknown.compress_runs());
    }

    #[test]
    fn mangle_defaults_to_enabled_and_decodes_known_values() {
        let older: Collection = serde_json::from_str("{}").unwrap();
        assert_eq!(older.mangle, Mangle::Enabled);
        let disabled: Collection = serde_json::from_str(r#"{"mangle":"disabled"}"#).unwrap();
        assert_eq!(disabled.mangle, Mangle::Disabled);
        let enabled: Collection = serde_json::from_str(r#"{"mangle":"enabled"}"#).unwrap();
        assert_eq!(enabled.mangle, Mangle::Enabled);
        let unknown: Collection = serde_json::from_str(r#"{"mangle":"partial"}"#).unwrap();
        assert_eq!(unknown.mangle, Mangle::Enabled);
    }
}
