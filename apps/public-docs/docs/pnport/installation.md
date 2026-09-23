# Installation and availability

**No pnport release is available yet.** Do not install an unpublished `0.0.0` development package or assume that the presence of an installer script means that 0.1.0 has passed its platform gates. Check [pnport releases](https://github.com/delinoio/oss/releases?q=pnport) and the [npm package](https://www.npmjs.com/package/@delino/pnport) for an actual published version before using these methods.

Once a version is published, choose an exact version for repeatable installation. npm and Yarn 4 users install `@delino/pnport` with optional dependencies enabled. It selects one matching native package at the same exact version. The launcher needs Node.js 22 or newer; a standalone native archive does not need Node.js merely to start pnport. Packages have no install script, runtime download, or compiler fallback.

After confirming a version is published, replace `<published-version>` in one of these examples:

```sh
npm install --save-dev --ignore-scripts '@delino/pnport@<published-version>'
# or, in a Yarn 4 project:
yarn add --dev '@delino/pnport@<published-version>'
```

For a direct macOS or GNU Linux install, inspect the hosted script and pin the version:

```sh
curl -fsSLo pnport-install.sh https://oss.delino.io/pnport/install.sh
bash pnport-install.sh --version '<published-version>'
```

On Windows PowerShell:

```powershell
Invoke-WebRequest https://oss.delino.io/pnport/install.ps1 -OutFile pnport-install.ps1
./pnport-install.ps1 -Version '<published-version>'
```

After the first release, Homebrew users can run `brew install delinoio/tap/pnport` on supported macOS and GNU Linux hosts. Run `pnport --version` after any installation and use an explicit published version for direct installer rollback.

Direct <a href="/pnport/install.sh">POSIX</a> and <a href="/pnport/install.ps1">PowerShell</a> installers are provided for published GitHub archives. Each archive includes `pnport` and its matched interception library; the installer verifies the archive checksum and activates both files together. Homebrew uses prebuilt macOS or GNU Linux archives. The native library must remain next to the actual executable. Do not copy only the executable.

The target matrix is macOS x64/arm64, Windows x64/arm64 MSVC, and GNU Linux x64/arm64. Linux musl and other architectures are unsupported. See [releases and rollback](/pnport/releases) for verification and explicit version changes. No automatic updates occur.
