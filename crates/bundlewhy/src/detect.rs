//! Project, bundler and configuration selection.
//!
//! Auto-selection happens only when exactly one candidate exists; otherwise the
//! error lists the candidates and the flag that selects one.

use std::path::{Path, PathBuf};

use crate::{
    engine::{self, BundlerChoice},
    error::{BwError, BwResult, ErrorCode},
};

#[derive(Debug, Clone)]
pub struct Selection {
    pub project_root: PathBuf,
    pub project_name: String,
    pub bundler: BundlerChoice,
    pub config_file: Option<PathBuf>,
}

#[derive(Debug, Default)]
pub struct SelectionRequest {
    pub project: Option<PathBuf>,
    pub config: Option<PathBuf>,
    pub bundler: Option<BundlerChoice>,
}

fn read_manifest(root: &Path) -> BwResult<serde_json::Value> {
    let path = root.join("package.json");
    let text = std::fs::read_to_string(&path).map_err(|error| {
        BwError::new(
            ErrorCode::ProjectNotFound,
            format!("Could not read {}: {error}", path.display()),
            "Run bundlewhy in a project directory that contains package.json, or pass --project \
             <dir>.",
        )
    })?;
    serde_json::from_str(&text).map_err(|error| {
        BwError::new(
            ErrorCode::ProjectNotFound,
            format!("{} is not valid JSON: {error}", path.display()),
            "Fix package.json, then start a new run.",
        )
    })
}

fn declares_dependency(manifest: &serde_json::Value, name: &str) -> bool {
    [
        "dependencies",
        "devDependencies",
        "optionalDependencies",
        "peerDependencies",
    ]
    .iter()
    .any(|field| {
        manifest
            .get(field)
            .and_then(|deps| deps.get(name))
            .is_some()
    })
}

fn config_candidates(root: &Path, choice: BundlerChoice) -> Vec<PathBuf> {
    choice
        .spec()
        .config_files
        .iter()
        .map(|name| root.join(name))
        .filter(|path| path.is_file())
        .collect()
}

pub fn select(request: &SelectionRequest) -> BwResult<Selection> {
    let requested_root = request
        .project
        .clone()
        .unwrap_or_else(|| PathBuf::from("."));
    let project_root = std::fs::canonicalize(&requested_root).map_err(|error| {
        BwError::new(
            ErrorCode::ProjectNotFound,
            format!(
                "Project directory {} is not accessible: {error}",
                requested_root.display()
            ),
            "Pass an existing project directory with --project <dir>.",
        )
    })?;
    let manifest = read_manifest(&project_root)?;
    let project_name = manifest
        .get("name")
        .and_then(serde_json::Value::as_str)
        .map_or_else(
            || {
                project_root.file_name().map_or_else(
                    || "project".to_owned(),
                    |name| name.to_string_lossy().into_owned(),
                )
            },
            ToOwned::to_owned,
        );

    let explicit_config = match &request.config {
        Some(config) => {
            let path = if config.is_absolute() {
                config.clone()
            } else {
                project_root.join(config)
            };
            Some(std::fs::canonicalize(&path).map_err(|error| {
                BwError::new(
                    ErrorCode::ConfigNotFound,
                    format!(
                        "Configuration file {} is not accessible: {error}",
                        path.display()
                    ),
                    "Pass an existing configuration file with --config <file>.",
                )
            })?)
        }
        None => None,
    };

    let bundler = if let Some(choice) = request.bundler {
        choice
    } else if let Some(config) = &explicit_config {
        let name = config
            .file_name()
            .map(|name| name.to_string_lossy().into_owned())
            .unwrap_or_default();
        if name.starts_with("vite.") {
            BundlerChoice::Vite
        } else if name.starts_with("rspack.") {
            BundlerChoice::Rspack
        } else {
            return Err(BwError::new(
                ErrorCode::BundlerNotDetected,
                format!("Cannot infer the bundler from configuration file {name}"),
                "Pass --bundler vite or --bundler rspack.",
            ));
        }
    } else {
        let candidates: Vec<BundlerChoice> = engine::ALL
            .into_iter()
            .filter(|choice| {
                declares_dependency(&manifest, choice.spec().package)
                    || !config_candidates(&project_root, *choice).is_empty()
            })
            .collect();
        match candidates.as_slice() {
            [single] => *single,
            [] => {
                return Err(BwError::new(
                    ErrorCode::BundlerNotDetected,
                    "No supported bundler (vite or @rspack/core) is declared in package.json and \
                     no configuration file was found",
                    "Pass --bundler vite or --bundler rspack together with --config <file>.",
                ));
            }
            many => {
                let names: Vec<&str> = many.iter().map(|choice| choice.as_str()).collect();
                return Err(BwError::new(
                    ErrorCode::BundlerAmbiguous,
                    format!("Several bundlers are candidates: {}", names.join(", ")),
                    "Select one with --bundler <vite|rspack>.",
                ));
            }
        }
    };

    let config_file = if let Some(config) = explicit_config {
        Some(config)
    } else {
        let candidates = config_candidates(&project_root, bundler);
        match candidates.len() {
            0 if bundler == BundlerChoice::Vite => None,
            0 => {
                return Err(BwError::new(
                    ErrorCode::ConfigNotFound,
                    format!(
                        "No {} configuration file found in {}",
                        bundler.as_str(),
                        project_root.display()
                    ),
                    "Pass the configuration file with --config <file>.",
                ));
            }
            1 => candidates.into_iter().next(),
            _ => {
                let names: Vec<String> = candidates
                    .iter()
                    .filter_map(|path| {
                        path.file_name()
                            .map(|name| name.to_string_lossy().into_owned())
                    })
                    .collect();
                return Err(BwError::new(
                    ErrorCode::ConfigAmbiguous,
                    format!(
                        "Several configuration files are candidates: {}",
                        names.join(", ")
                    ),
                    "Select one with --config <file>.",
                ));
            }
        }
    };

    Ok(Selection {
        project_root,
        project_name,
        bundler,
        config_file,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn project(files: &[(&str, &str)]) -> tempfile::TempDir {
        let dir = tempfile::tempdir().unwrap();
        for (name, content) in files {
            std::fs::write(dir.path().join(name), content).unwrap();
        }
        dir
    }

    #[test]
    fn selects_unambiguous_vite_project() {
        let dir = project(&[
            (
                "package.json",
                r#"{"name":"app","devDependencies":{"vite":"8.3.4"}}"#,
            ),
            ("vite.config.ts", "export default {}"),
        ]);
        let selection = select(&SelectionRequest {
            project: Some(dir.path().into()),
            ..Default::default()
        })
        .unwrap();
        assert_eq!(selection.bundler, BundlerChoice::Vite);
        assert_eq!(selection.project_name, "app");
        assert!(selection.config_file.unwrap().ends_with("vite.config.ts"));
    }

    #[test]
    fn rejects_ambiguous_bundlers_with_candidates() {
        let dir = project(&[(
            "package.json",
            r#"{"devDependencies":{"vite":"8.3.4","@rspack/core":"2.2.9"}}"#,
        )]);
        let error = select(&SelectionRequest {
            project: Some(dir.path().into()),
            ..Default::default()
        })
        .unwrap_err();
        assert_eq!(error.code, ErrorCode::BundlerAmbiguous);
        assert!(error.message.contains("vite, rspack"));
        assert!(error.recovery.contains("--bundler"));
    }

    #[test]
    fn rejects_ambiguous_configuration_files() {
        let dir = project(&[
            (
                "package.json",
                r#"{"devDependencies":{"@rspack/core":"2.2.9"}}"#,
            ),
            ("rspack.config.js", ""),
            ("rspack.config.mjs", ""),
        ]);
        let error = select(&SelectionRequest {
            project: Some(dir.path().into()),
            ..Default::default()
        })
        .unwrap_err();
        assert_eq!(error.code, ErrorCode::ConfigAmbiguous);
        assert!(error.message.contains("rspack.config.js"));
    }

    #[test]
    fn explicit_bundler_resolves_ambiguity() {
        let dir = project(&[
            (
                "package.json",
                r#"{"devDependencies":{"vite":"8","@rspack/core":"2"}}"#,
            ),
            ("rspack.config.mjs", ""),
        ]);
        let selection = select(&SelectionRequest {
            project: Some(dir.path().into()),
            bundler: Some(BundlerChoice::Rspack),
            ..Default::default()
        })
        .unwrap();
        assert_eq!(selection.bundler, BundlerChoice::Rspack);
    }

    #[test]
    fn missing_project_manifest_is_reported() {
        let dir = tempfile::tempdir().unwrap();
        let error = select(&SelectionRequest {
            project: Some(dir.path().into()),
            ..Default::default()
        })
        .unwrap_err();
        assert_eq!(error.code, ErrorCode::ProjectNotFound);
    }
}
