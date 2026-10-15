Runmoor for macOS 14+ arm64, Ubuntu 22.04+ amd64/arm64 and Windows 11 x64.
Live GitHub repository/organization and App/PAT compatibility and real Tart execution have not been certified.
Verify SHA256SUMS and the accompanying Sigstore bundles before installation.
Updates are manual. Docker/Tart are operator-installed; macOS/Xcode images are not included.

Actual macOS host execution, unsigned Xcode builds and live host jobs have not been validated. Host execution requires explicit selection on macOS 14+ arm64 and trusted same-account workflows; separate directories do not provide a security boundary.

Windows supports trusted same-account native runners and local Windows Docker runners in plain mode. Windows Docker requires Windows 11 Pro/Enterprise, Hyper-V, Docker Desktop in Windows containers mode and Server Core LTSC 2022 x64 images. Windows Server, ARM64, Linux containers on Windows and Windows DinD are unsupported. Login-triggered background execution uses the current user's scheduled task and stores no password; continued execution after logout is unsupported. Verify the Windows ZIP and complete checksum inventory with the accompanying exact-source Sigstore bundles. Docker Windows execution and live GitHub jobs require separate acceptance and have not been established by fixtures or packaging.
