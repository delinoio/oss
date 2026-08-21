use std::{
    fs, io,
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};

use crate::{HOST_NAME, expected_extension_origin};

#[derive(Debug, Deserialize, Serialize)]
pub struct HostManifest {
    pub name: String,
    pub description: String,
    pub path: String,
    #[serde(rename = "type")]
    pub host_type: String,
    pub allowed_origins: Vec<String>,
}

pub fn manifest(binary: &Path) -> io::Result<HostManifest> {
    if !binary.is_absolute() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "host path must be absolute",
        ));
    }
    Ok(HostManifest {
        name: HOST_NAME.to_string(),
        description: "DevHUD browser context broker".to_string(),
        path: binary.to_string_lossy().into_owned(),
        host_type: "stdio".to_string(),
        allowed_origins: vec![expected_extension_origin()],
    })
}

pub fn write_manifest(destination: &Path, binary: &Path) -> io::Result<()> {
    let parent = destination
        .parent()
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "manifest has no parent"))?;
    fs::create_dir_all(parent)?;
    let bytes = serde_json::to_vec_pretty(&manifest(binary)?)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "unable to serialize manifest"))?;
    fs::write(destination, [bytes.as_slice(), b"\n"].concat())
}

pub fn remove_manifest(destination: &Path) -> io::Result<()> {
    match fs::remove_file(destination) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
    }
}

pub fn user_manifest_path() -> io::Result<PathBuf> {
    #[cfg(target_os = "macos")]
    return dirs::home_dir()
        .map(|home| {
            home.join(format!(
                "Library/Application Support/Google/Chrome/NativeMessagingHosts/{HOST_NAME}.json"
            ))
        })
        .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "home directory is required"));
    #[cfg(target_os = "linux")]
    return dirs::home_dir()
        .map(|home| {
            home.join(format!(
                ".config/google-chrome/NativeMessagingHosts/{HOST_NAME}.json"
            ))
        })
        .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "home directory is required"));
    #[cfg(windows)]
    return std::env::var_os("LOCALAPPDATA")
        .map(PathBuf::from)
        .map(|root| root.join(format!("io.delino.devhud/{HOST_NAME}.json")))
        .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "LOCALAPPDATA is required"));
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn registration_is_exact_and_removable() {
        let root =
            std::env::temp_dir().join(format!("devhud-host-registration-{}", std::process::id()));
        let destination = root.join("manifest.json");
        let binary = root.join("devhud-native-messaging-host");
        write_manifest(&destination, &binary).unwrap();
        let parsed: HostManifest =
            serde_json::from_slice(&fs::read(&destination).unwrap()).unwrap();
        assert_eq!(parsed.name, HOST_NAME);
        assert_eq!(parsed.allowed_origins, [expected_extension_origin()]);
        assert_eq!(parsed.path, binary.to_string_lossy());
        remove_manifest(&destination).unwrap();
        assert!(!destination.exists());
        let _ = fs::remove_dir_all(root);
    }
}
