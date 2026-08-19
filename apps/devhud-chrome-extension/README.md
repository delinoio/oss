# DevHUD Chrome extension

The MV3 extension captures one explicitly selected element after a popup gesture and sends bounded, redacted context through `io.delino.devhud.native_messaging`. It has no page-wide content script and does not collect cookies, storage, console output, or network traffic.

Use `pnpm --filter devhud-chrome-extension test` for the deterministic fixture build. Production builds require the matching `DEVHUD_CHROME_EXTENSION_ID` and base64 SPKI `DEVHUD_CHROME_EXTENSION_PUBLIC_KEY` release configuration.
