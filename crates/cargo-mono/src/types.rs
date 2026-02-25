use clap::ValueEnum;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ValueEnum)]
#[serde(rename_all = "kebab-case")]
pub enum OutputFormat {
    Human,
    Json,
}

impl OutputFormat {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Human => "human",
            Self::Json => "json",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum CargoMonoCommand {
    List,
    Changed,
    Bump,
    Publish,
}

impl CargoMonoCommand {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Changed => "changed",
            Self::Bump => "bump",
            Self::Publish => "publish",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, ValueEnum)]
#[serde(rename_all = "kebab-case")]
pub enum BumpLevel {
    Major,
    Minor,
    Patch,
    Prerelease,
}

impl BumpLevel {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Major => "major",
            Self::Minor => "minor",
            Self::Patch => "patch",
            Self::Prerelease => "prerelease",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum TargetSelector {
    All,
    Changed,
    Package,
}

impl TargetSelector {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::All => "all",
            Self::Changed => "changed",
            Self::Package => "package",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum PublishSkipReason {
    NonPublishable,
    AlreadyPublished,
}

impl PublishSkipReason {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::NonPublishable => "non-publishable",
            Self::AlreadyPublished => "already-published",
        }
    }
}
