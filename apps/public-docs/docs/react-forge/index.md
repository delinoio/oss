# React Forge

Build PowerPoint presentations, Word documents, Excel workbooks, and independently generated tagged PDFs with React components. React Forge keeps your work in an in-memory session until you export it. Install the public `@delino/react-forge` npm package; its command is `react-forge`.

## Start with a presentation

1. [Install React Forge](/react-forge/installation) with optional dependencies enabled.
2. Follow [Getting started](/react-forge/getting-started) to create a `hello.tsx` task.
3. Run the task to export your first `hello.pptx`, then choose a format or workflow below.

## Where it runs

Local document work runs on Node.js 24 on macOS, Windows, and glibc Linux, on x64 and arm64. React Forge can also create and edit Figma Design files through explicit remote publication. Figma publication needs an existing official Figma MCP connection; live authentication currently uses macOS Keychain and has been validated on macOS arm64.

Exports are explicit. Sessions exist in memory until disposed; starting a new process does not restore them. There is no hosted React Forge service. Local document work does not fetch arbitrary asset URLs or require Office, LibreOffice, Python, or a conversion service at runtime.

## Choose a workflow

- **Create a local document:** Choose [PPTX](/react-forge/formats/pptx/), [DOCX](/react-forge/formats/docx/), [XLSX](/react-forge/formats/xlsx/), or [PDF](/react-forge/formats/pdf/). Each format has its own components and model.
- **Keep working in one process:** Use [sessions](/react-forge/sessions) for React updates, inspection, assets, fonts, measurement, and explicit exports.
- **Edit an Office file:** [Mount a supported region](/react-forge/office-editing) of an imported PPTX, DOCX, or XLSX while preserving unrelated content.
- **Work in Figma:** [Create or edit Figma Design files](/react-forge/formats/figma/) with explicit publication and a receipt that records remote outcomes.
- **Choose an entry point:** Run one-shot tasks with the [CLI](/react-forge/cli), or retain sessions through the [local stdio MCP server](/react-forge/mcp).
- **Preview upcoming formats:** Read about [procedural game SFX](/react-forge/formats/sfx/) and [pixel sprites](/react-forge/formats/sprite/). Both are unreleased and absent from npm `0.1.1`.

Before adopting a workflow, check its [limits and troubleshooting](/react-forge/limits-and-troubleshooting) and [release and validation status](/react-forge/releases).

React Forge is Apache-2.0 licensed. Report problems through [Delino OSS issues](https://github.com/delinoio/oss/issues).
