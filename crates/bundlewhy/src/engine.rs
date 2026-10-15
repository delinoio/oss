//! Frozen engine compatibility matrix and embedded collector adapters.

use clap::ValueEnum;

use crate::model::Bundler;

#[derive(Debug, Clone, Copy, PartialEq, Eq, ValueEnum)]
pub enum BundlerChoice {
    Vite,
    Rspack,
}

impl BundlerChoice {
    pub const fn bundler(self) -> Bundler {
        match self {
            Self::Vite => Bundler::Vite,
            Self::Rspack => Bundler::Rspack,
        }
    }

    pub const fn spec(self) -> &'static EngineSpec {
        match self {
            Self::Vite => &VITE,
            Self::Rspack => &RSPACK,
        }
    }

    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Vite => "vite",
            Self::Rspack => "rspack",
        }
    }
}

#[derive(Debug)]
pub struct EngineSpec {
    pub package: &'static str,
    pub version: &'static str,
    pub minifier: &'static str,
    pub note: &'static str,
    pub config_files: &'static [&'static str],
    pub adapter_file: &'static str,
}

impl EngineSpec {
    /// Whether an installed engine version is the frozen matrix version. The
    /// comparison is byte-exact, matching the adapter's own check: a version
    /// with surrounding whitespace is never the matrix version. Every
    /// support decision uses this one function.
    pub fn is_matrix_version(&self, version: &str) -> bool {
        version == self.version
    }
}

pub const VITE: EngineSpec = EngineSpec {
    package: "vite",
    version: "8.3.4",
    minifier: "Oxc minifier (Rolldown 1.2.13, build.minify: 'oxc')",
    note: "Client production builds only",
    config_files: &[
        "vite.config.js",
        "vite.config.mjs",
        "vite.config.cjs",
        "vite.config.ts",
        "vite.config.mts",
        "vite.config.cts",
    ],
    adapter_file: "vite.mjs",
};

pub const RSPACK: EngineSpec = EngineSpec {
    package: "@rspack/core",
    version: "2.2.9",
    minifier: "SwcJsMinimizerRspackPlugin (built into @rspack/core)",
    note: "Web-target production builds only",
    config_files: &[
        "rspack.config.js",
        "rspack.config.mjs",
        "rspack.config.cjs",
        "rspack.config.ts",
        "rspack.config.mts",
        "rspack.config.cts",
    ],
    adapter_file: "rspack.mjs",
};

pub const ALL: [BundlerChoice; 2] = [BundlerChoice::Vite, BundlerChoice::Rspack];

/// Adapter sources embedded at build time so the adapter always matches the
/// binary.
pub const ADAPTER_FILES: [(&str, &str); 3] = [
    ("common.mjs", include_str!("../adapters/common.mjs")),
    ("vite.mjs", include_str!("../adapters/vite.mjs")),
    ("rspack.mjs", include_str!("../adapters/rspack.mjs")),
];

pub fn matrix_text() -> String {
    let mut text =
        String::from("bundlewhy engine compatibility matrix (frozen for this release)\n\n");
    for choice in ALL {
        let spec = choice.spec();
        text.push_str(&format!(
            "  {:<7} {} {}\n          minifier: {}\n          scope: {}\n",
            choice.as_str(),
            spec.package,
            spec.version,
            spec.minifier,
            spec.note
        ));
    }
    text.push_str(
        "\nOther versions require --allow-unsupported-engine and are marked unsupported in \
         results.\n",
    );
    text
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn matrix_version_comparison_is_exact() {
        for choice in ALL {
            let spec = choice.spec();
            assert!(spec.is_matrix_version(spec.version));
            for other in [
                format!(" {}", spec.version),
                format!("{} ", spec.version),
                format!("{}\n", spec.version),
                format!("v{}", spec.version),
                String::new(),
                "unknown".to_owned(),
            ] {
                assert!(!spec.is_matrix_version(&other), "{other:?}");
            }
        }
    }
}
