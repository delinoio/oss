use semver::{Prerelease, Version};

use crate::{
    errors::{CargoMonoError, Result},
    types::BumpLevel,
};

pub fn bump_version(current: &Version, level: BumpLevel, preid: Option<&str>) -> Result<Version> {
    let mut next = current.clone();

    match level {
        BumpLevel::Major => {
            next.major += 1;
            next.minor = 0;
            next.patch = 0;
            next.pre = Prerelease::EMPTY;
            next.build = semver::BuildMetadata::EMPTY;
        }
        BumpLevel::Minor => {
            next.minor += 1;
            next.patch = 0;
            next.pre = Prerelease::EMPTY;
            next.build = semver::BuildMetadata::EMPTY;
        }
        BumpLevel::Patch => {
            next.patch += 1;
            next.pre = Prerelease::EMPTY;
            next.build = semver::BuildMetadata::EMPTY;
        }
        BumpLevel::Prerelease => {
            let preid = preid.ok_or_else(|| {
                CargoMonoError::invalid_input("--preid is required when --level prerelease")
            })?;

            let next_pre = next_prerelease(current, preid)?;
            if current.pre.is_empty() || !current.pre.as_str().starts_with(preid) {
                next.patch += 1;
                next.pre = next_pre;
            } else {
                next.pre = next_pre;
            }
            next.build = semver::BuildMetadata::EMPTY;
        }
    }

    Ok(next)
}

fn next_prerelease(current: &Version, preid: &str) -> Result<Prerelease> {
    if current.pre.is_empty() {
        return Prerelease::new(&format!("{preid}.1")).map_err(Into::into);
    }

    let raw = current.pre.as_str();
    if !raw.starts_with(preid) {
        return Prerelease::new(&format!("{preid}.1")).map_err(Into::into);
    }

    let suffix = raw.strip_prefix(preid).unwrap_or_default();
    if let Some(number_part) = suffix.strip_prefix('.') {
        if let Ok(number) = number_part.parse::<u64>() {
            return Prerelease::new(&format!("{preid}.{}", number + 1)).map_err(Into::into);
        }
    }

    Prerelease::new(&format!("{preid}.1")).map_err(Into::into)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bump_major_resets_minor_and_patch() {
        let current = Version::parse("1.2.3").unwrap();
        let next = bump_version(&current, BumpLevel::Major, None).unwrap();

        assert_eq!(next, Version::parse("2.0.0").unwrap());
    }

    #[test]
    fn bump_minor_resets_patch() {
        let current = Version::parse("1.2.3").unwrap();
        let next = bump_version(&current, BumpLevel::Minor, None).unwrap();

        assert_eq!(next, Version::parse("1.3.0").unwrap());
    }

    #[test]
    fn bump_patch_increments_patch() {
        let current = Version::parse("1.2.3").unwrap();
        let next = bump_version(&current, BumpLevel::Patch, None).unwrap();

        assert_eq!(next, Version::parse("1.2.4").unwrap());
    }

    #[test]
    fn bump_prerelease_requires_preid() {
        let current = Version::parse("1.2.3").unwrap();
        let error = bump_version(&current, BumpLevel::Prerelease, None).unwrap_err();

        assert_eq!(error.kind, crate::errors::ErrorKind::InvalidInput);
    }

    #[test]
    fn bump_prerelease_from_release_increments_patch() {
        let current = Version::parse("1.2.3").unwrap();
        let next = bump_version(&current, BumpLevel::Prerelease, Some("rc")).unwrap();

        assert_eq!(next, Version::parse("1.2.4-rc.1").unwrap());
    }

    #[test]
    fn bump_prerelease_same_identifier_increments_suffix() {
        let current = Version::parse("1.2.3-rc.7").unwrap();
        let next = bump_version(&current, BumpLevel::Prerelease, Some("rc")).unwrap();

        assert_eq!(next, Version::parse("1.2.3-rc.8").unwrap());
    }
}
