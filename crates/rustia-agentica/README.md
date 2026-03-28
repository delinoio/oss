# rustia-agentica

`rustia-agentica` provides a `MicroAgentica`-style agent loop for Rust using:

- `aisdk` non-streaming `generate_text` loop (`with_tool` + `stop_when`)
- class tools (`aisdk::core::tools::Tool`), including `rustia-llm::tool(...)`
- MCP tools bridged from `rmcp` (`tools/list`, `tools/call`)

## Status

- v1 scope: `MicroAgentica` only
- Protocols: class + mcp
- Streaming: not included in v1

## Notes

- Tool name collisions are treated as build errors.
- MCP tool execution is bridged through a synchronous callback workaround using
  `tokio::task::block_in_place` + `Handle::block_on`.
