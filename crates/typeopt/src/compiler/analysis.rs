// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! Shared whole-program facts for pass planners. Everything here is computed
//! from the current program text; nothing survives a round, so accepted
//! changes always invalidate and recompute every fact.
use anyhow::Result;
use ts_goport::prelude::*;

use super::{collect, is_js};

pub(crate) struct SourceInfo {
    pub node: Node,
    pub name: String,
    pub text: String,
    pub js: bool,
    pub entry: bool,
    /// Every node of the file in preorder.
    pub nodes: Vec<Node>,
}

pub(crate) struct Ctx<'c> {
    pub checker: &'c mut Checker,
    /// Application modules (TypeScript and pass-through JavaScript) in
    /// deterministic path order.
    pub files: Vec<SourceInfo>,
}

impl<'c> Ctx<'c> {
    pub fn new(checker: &'c mut Checker, files: Vec<Node>, entries: &[String]) -> Result<Self> {
        let mut infos: Vec<SourceInfo> = files
            .into_iter()
            .map(|node| {
                let name = source_file_info(node).file_name.clone();
                let mut nodes = Vec::new();
                collect(node, &mut nodes);
                SourceInfo {
                    node,
                    entry: entries.contains(&name),
                    js: is_js(node),
                    text: source_file_text(node).to_string(),
                    name,
                    nodes,
                }
            })
            .collect();
        infos.sort_by(|a, b| a.name.cmp(&b.name));
        Ok(Self {
            checker,
            files: infos,
        })
    }
}
