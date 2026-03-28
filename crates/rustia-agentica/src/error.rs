use std::fmt::{Display, Formatter};

/// Tool registration source used by build-time diagnostics.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ToolOrigin {
    Class { controller: String },
    Mcp { controller: String },
}

impl Display for ToolOrigin {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Class { controller } => write!(f, "class:{controller}"),
            Self::Mcp { controller } => write!(f, "mcp:{controller}"),
        }
    }
}

/// Build-time error for [`crate::MicroAgentica`] construction.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MicroAgenticaBuildError {
    DuplicateToolName {
        name: String,
        first_origin: ToolOrigin,
        second_origin: ToolOrigin,
    },
    EmptyToolName {
        origin: ToolOrigin,
    },
    McpListTools {
        controller: String,
        reason: String,
    },
    InvalidMcpSchema {
        controller: String,
        tool_name: String,
        reason: String,
    },
    ToolBuild {
        origin: ToolOrigin,
        tool_name: String,
        reason: String,
    },
}

impl Display for MicroAgenticaBuildError {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::DuplicateToolName {
                name,
                first_origin,
                second_origin,
            } => {
                write!(
                    f,
                    "duplicate tool name '{name}' between {first_origin} and {second_origin}"
                )
            }
            Self::EmptyToolName { origin } => write!(f, "tool name must not be empty ({origin})"),
            Self::McpListTools { controller, reason } => {
                write!(
                    f,
                    "failed to list MCP tools for controller '{controller}': {reason}"
                )
            }
            Self::InvalidMcpSchema {
                controller,
                tool_name,
                reason,
            } => write!(
                f,
                "invalid MCP tool schema for '{tool_name}' in controller '{controller}': {reason}"
            ),
            Self::ToolBuild {
                origin,
                tool_name,
                reason,
            } => write!(
                f,
                "failed to build tool '{tool_name}' from {origin}: {reason}"
            ),
        }
    }
}

impl std::error::Error for MicroAgenticaBuildError {}

/// Runtime error for one conversation turn.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MicroAgenticaConversationError {
    ModelRequest { reason: String },
}

impl Display for MicroAgenticaConversationError {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::ModelRequest { reason } => {
                write!(f, "language model request failed: {reason}")
            }
        }
    }
}

impl std::error::Error for MicroAgenticaConversationError {}
