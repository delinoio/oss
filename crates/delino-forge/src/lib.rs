pub mod mcp;
pub mod preview;
pub mod store;
pub fn capabilities() -> serde_json::Value {
    serde_json::json!({"name":"Delino Forge MCP","version":env!("CARGO_PKG_VERSION"),"dsl_versions":[1],"formats":["pptx"],"nodes":["row","column","canvas","text","list","image","shape","table","chart","connector"],"charts":["bar"],"images":["png","jpeg"],"transport":"stdio","internal_llm":false,"preview":{"optional":true,"executables":["soffice","pdftoppm"]},"import":{"dialect":"transitional","unsupported_elements":"preserve","signed_or_encrypted":"reject"},"export":{"tracked_source_overwrite":false,"existing_output_requires_overwrite":true}})
}
