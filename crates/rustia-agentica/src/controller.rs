use aisdk::core::tools::Tool;
use rmcp::{Peer, RoleClient};

/// Controller inputs accepted by [`crate::MicroAgentica`].
#[derive(Clone, Debug)]
pub enum MicroAgenticaController {
    Class(ClassController),
    Mcp(McpController),
}

impl From<ClassController> for MicroAgenticaController {
    fn from(value: ClassController) -> Self {
        Self::Class(value)
    }
}

impl From<McpController> for MicroAgenticaController {
    fn from(value: McpController) -> Self {
        Self::Mcp(value)
    }
}

/// Class-based controller carrying pre-built AISDK tools.
#[derive(Clone, Debug)]
pub struct ClassController {
    pub name: String,
    pub tools: Vec<Tool>,
}

impl ClassController {
    pub fn new(tools: Vec<Tool>) -> Self {
        Self {
            name: "class".to_owned(),
            tools,
        }
    }

    pub fn named(name: impl Into<String>, tools: Vec<Tool>) -> Self {
        Self {
            name: name.into(),
            tools,
        }
    }
}

/// MCP controller carrying a connected `rmcp` client peer.
#[derive(Clone, Debug)]
pub struct McpController {
    pub name: String,
    pub peer: Peer<RoleClient>,
}

impl McpController {
    pub fn new(name: impl Into<String>, peer: Peer<RoleClient>) -> Self {
        Self {
            name: name.into(),
            peer,
        }
    }
}
