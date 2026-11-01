// SPDX-License-Identifier: Apache-2.0
//! Shell activation code and confirmed, atomic profile updates (Go
//! `internal/shell`). Never writes diagnostics to stdout or prompts without
//! the caller's participation.

use anyhow::Result;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    Bash,
    Zsh,
    #[serde(rename = "powershell")]
    PowerShell,
}

impl Kind {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Bash => "bash",
            Self::Zsh => "zsh",
            Self::PowerShell => "powershell",
        }
    }

    /// Parses a shell name; the error matches `unsupported shell "x"; use bash,
    /// zsh, or powershell`.
    pub fn parse(value: &str) -> Result<Self> {
        let _ = value;
        todo!("shell::Kind::parse")
    }
}

impl std::fmt::Display for Kind {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Action {
    Setup,
    Remove,
}

impl Action {
    pub fn parse(value: &str) -> Option<Self> {
        match value {
            "setup" => Some(Self::Setup),
            "remove" => Some(Self::Remove),
            _ => None,
        }
    }

    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Setup => "setup",
            Self::Remove => "remove",
        }
    }
}

/// Returns activation code only.
pub fn init(kind: Kind, shim_dir: &str) -> Result<String> {
    let _ = (kind, shim_dir);
    todo!("shell::init")
}

#[derive(Debug, Clone, Default)]
pub struct Selection {
    /// Explicit `--file` takes precedence.
    pub file: String,
    pub home: String,
    pub zdotdir: String,
    /// `$PROFILE.CurrentUserCurrentHost` from PowerShell 7.
    pub powershell_profile: String,
}

/// Never guesses between existing Bash startup files or PowerShell hosts.
pub fn select_file(kind: Kind, selection: &Selection) -> Result<String> {
    let _ = (kind, selection);
    todo!("shell::select_file")
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum ShellError {
    #[error(
        "shell update requires confirmation; review the proposed target/change and use --yes for \
         noninteractive operation"
    )]
    ConfirmationRequired,
    #[error("shell update cancelled")]
    Cancelled,
}

/// A reviewed profile change.
pub struct Plan {
    pub path: String,
    pub kind: Kind,
    pub action: Action,
    pub before: Vec<u8>,
    pub after: Vec<u8>,
}

pub struct Approval<'a> {
    pub yes: bool,
    pub interactive: bool,
    /// Must show the target and owned-block change before asking.
    pub confirm: Option<&'a mut dyn FnMut(&Plan) -> Result<bool>>,
}

impl Plan {
    pub fn changed(&self) -> bool {
        self.before != self.after
    }

    /// Owned block bytes removed and added, and whether a separator line break
    /// outside the block is added or removed. Never returns other content.
    pub fn owned_change(&self) -> (Vec<u8>, Vec<u8>, bool) {
        todo!("shell::Plan::owned_change")
    }

    /// A notice, not activation code.
    pub fn current_session_instruction(&self) -> String {
        todo!("shell::Plan::current_session_instruction")
    }

    pub fn apply(&self, approval: Approval<'_>) -> Result<()> {
        let _ = approval;
        todo!("shell::Plan::apply")
    }
}

pub fn prepare(kind: Kind, action: Action, path: &str, shim_dir: &str) -> Result<Plan> {
    let _ = (kind, action, path, shim_dir);
    todo!("shell::prepare")
}
