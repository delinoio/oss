# React Forge

React Forge uses React components and persistent sessions to author PowerPoint presentations, Word documents, Excel workbooks, and independently generated tagged PDFs. It can also create and edit Figma Design files through explicit remote publication. The public npm package is `@delino/react-forge`; its command is `react-forge`.

Local document work runs on Node.js 24 on macOS, Windows, and glibc Linux, on x64 and arm64. Figma publication additionally needs an existing official Figma MCP connection; live authentication currently uses macOS Keychain and has been validated on macOS arm64. There is no hosted React Forge service.

## Choose a workflow

- [Install React Forge](/react-forge/installation) and [create a first presentation](/react-forge/getting-started).
- Use [sessions](/react-forge/sessions) for repeated React updates, inspection, assets, fonts, measurement, and explicit exports.
- Author [PPTX](/react-forge/pptx), [DOCX](/react-forge/docx), [XLSX](/react-forge/xlsx), or [PDF](/react-forge/pdf) with each format's own components.
- [Edit an existing Office file](/react-forge/office-editing) by selecting a supported region while preserving unrelated content.
- [Create or edit Figma Design files](/react-forge/figma) with explicit publication and a receipt that records remote outcomes.
- Run one-shot tasks with the [CLI](/react-forge/cli), or retain sessions through the [local stdio MCP server](/react-forge/mcp).

Exports are explicit. Sessions exist in memory until disposed; starting a new process does not restore them. React Forge does not fetch arbitrary asset URLs or require Office, LibreOffice, Python, or a conversion service at runtime. See [limits and troubleshooting](/react-forge/limits-and-troubleshooting) and [release and validation status](/react-forge/releases) before adopting a workflow.

React Forge is Apache-2.0 licensed. Report problems through [Delino OSS issues](https://github.com/delinoio/oss/issues).
