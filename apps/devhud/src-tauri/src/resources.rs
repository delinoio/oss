use std::path::{Path, PathBuf};

#[derive(Debug)]
pub struct ResourceLayout {
    root: PathBuf,
    required: Vec<PathBuf>,
}

impl ResourceLayout {
    pub fn for_executable(executable: &Path) -> Result<Self, String> {
        let binary_dir = executable
            .parent()
            .ok_or_else(|| "the executable has no parent directory".to_string())?;

        #[cfg(target_os = "macos")]
        let (root, required) = {
            let contents = binary_dir
                .parent()
                .ok_or_else(|| "the macOS executable is not inside Contents/MacOS".to_string())?;
            let framework = PathBuf::from("Frameworks/Chromium Embedded Framework.framework");
            let executable_name = executable
                .file_name()
                .and_then(|name| name.to_str())
                .ok_or_else(|| "the executable name is not UTF-8".to_string())?;
            let mut required = vec![
                framework.join("Chromium Embedded Framework"),
                framework.join("Resources/icudtl.dat"),
                framework.join("Resources/resources.pak"),
                framework.join("Libraries/libcef_sandbox.dylib"),
            ];
            for suffix in [
                " Helper (GPU)",
                " Helper (Renderer)",
                " Helper (Plugin)",
                " Helper (Alerts)",
                " Helper",
            ] {
                let helper = format!("{executable_name}{suffix}");
                required.push(
                    PathBuf::from("Frameworks")
                        .join(format!("{helper}.app/Contents/MacOS/{helper}")),
                );
            }
            (contents.to_path_buf(), required)
        };

        #[cfg(target_os = "windows")]
        let (root, required) = (
            binary_dir.to_path_buf(),
            [
                "libcef.dll",
                "chrome_elf.dll",
                "icudtl.dat",
                "resources.pak",
                "v8_context_snapshot.bin",
                "locales/en-US.pak",
                "bootstrap.exe",
                "bootstrapc.exe",
            ]
            .into_iter()
            .map(PathBuf::from)
            .collect(),
        );

        #[cfg(target_os = "linux")]
        let (root, required) = {
            let package_prefix = binary_dir.parent().ok_or_else(|| {
                "the Linux executable is not inside a package bin directory".to_string()
            })?;
            (
                package_prefix.join("share/DevHUD"),
                [
                    "libcef.so",
                    "icudtl.dat",
                    "resources.pak",
                    "v8_context_snapshot.bin",
                    "locales/en-US.pak",
                    "chrome-sandbox",
                ]
                .into_iter()
                .map(PathBuf::from)
                .collect(),
            )
        };

        Ok(Self { root, required })
    }

    pub fn missing(&self) -> Vec<String> {
        self.required
            .iter()
            .filter(|relative| !self.root.join(relative).exists())
            .map(|relative| relative.to_string_lossy().replace('\\', "/"))
            .collect()
    }

    pub fn required_relative_paths(&self) -> impl Iterator<Item = &Path> {
        self.required.iter().map(PathBuf::as_path)
    }
}

#[cfg(test)]
mod tests {
    #[cfg(target_os = "linux")]
    use std::path::Path;

    use super::ResourceLayout;

    #[test]
    fn every_platform_requires_cef_resources() {
        let executable = std::env::current_exe().expect("test executable path");
        let layout = ResourceLayout::for_executable(&executable).expect("resource layout");
        let required: Vec<_> = layout.required_relative_paths().collect();
        assert!(required.len() >= 6);
        assert!(required.iter().any(|path| path.ends_with("icudtl.dat")));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_uses_package_resource_directory() {
        let executable = Path::new("/tmp/devhud-smoke-root/usr/bin/devhud");
        let layout = ResourceLayout::for_executable(executable).expect("resource layout");

        assert_eq!(
            layout.root,
            Path::new("/tmp/devhud-smoke-root/usr/share/DevHUD")
        );
    }
}
