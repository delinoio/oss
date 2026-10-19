//! Desktop-entry identity resolution from explicit X11 application hints.

use std::{
    collections::HashSet,
    path::{Component, Path, PathBuf},
};

pub(super) fn resolve(hints: &[String], roots: &[PathBuf]) -> Option<String> {
    let mut ids = HashSet::new();
    for hint in hints {
        let path = Path::new(hint);
        if path.is_absolute() {
            for root in roots {
                if let Ok(relative) = path.strip_prefix(root)
                    && relative
                        .components()
                        .all(|part| matches!(part, Component::Normal(_)))
                    && path
                        .extension()
                        .is_some_and(|extension| extension == "desktop")
                    && path.is_file()
                {
                    let id = relative
                        .components()
                        .map(|part| part.as_os_str().to_str())
                        .collect::<Option<Vec<_>>>()?
                        .join("-");
                    ids.insert(id);
                }
            }
        } else if !hint.is_empty()
            && hint
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'-' | b'_'))
            && !hint.starts_with('.')
        {
            let id = if hint.ends_with(".desktop") {
                hint.clone()
            } else {
                format!("{hint}.desktop")
            };
            if roots.iter().any(|root| root.join(&id).is_file()) {
                ids.insert(id);
            }
        }
    }
    (ids.len() == 1).then(|| ids.into_iter().next()).flatten()
}

#[cfg(test)]
mod tests {
    use std::fs;

    use super::resolve;

    #[test]
    fn explicit_ids_distinguish_apps_with_the_same_window_class() {
        let directory = tempfile::tempdir().unwrap();
        let roots = vec![directory.path().to_path_buf()];
        for id in ["org.example.One", "org.example.Two"] {
            fs::write(
                directory.path().join(format!("{id}.desktop")),
                "[Desktop Entry]\nType=Application\n",
            )
            .unwrap();
            assert_eq!(resolve(&[id.into()], &roots), Some(format!("{id}.desktop")));
        }
        assert_eq!(resolve(&["missing".into()], &roots), None);
        assert_eq!(
            resolve(
                &["org.example.One".into(), "org.example.Two".into()],
                &roots
            ),
            None
        );
    }

    #[test]
    fn desktop_paths_are_scoped_to_applications_roots_and_normalized_to_ids() {
        let directory = tempfile::tempdir().unwrap();
        let apps = directory.path().join("applications");
        fs::create_dir_all(apps.join("vendor")).unwrap();
        let entry = apps.join("vendor/editor.desktop");
        fs::write(&entry, "[Desktop Entry]\nType=Application\n").unwrap();
        let roots = vec![apps];
        assert_eq!(
            resolve(&[entry.to_str().unwrap().into()], &roots),
            Some("vendor-editor.desktop".into())
        );
        let outside = directory.path().join("outside.desktop");
        fs::write(&outside, "[Desktop Entry]\nType=Application\n").unwrap();
        assert_eq!(
            resolve(
                &[
                    outside.to_str().unwrap().into(),
                    "../outside.desktop".into()
                ],
                &roots
            ),
            None
        );
    }
}
