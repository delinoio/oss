# Preview testing

Use the published pnport preview to test Turbopack and TypeScript 7 against a Yarn 4 Plug'n'Play project, without creating a physical project `node_modules` directory.

**pnport 0.1.0 is unreleased.** The experimental `0.1.0-next.1` preview is available on macOS and glibc Linux, each on x64 and arm64. Windows, musl hosts such as Alpine Linux, and mixed architectures are unsupported. Full feature, minimum-OS and benchmark acceptance remain unfinished; intermittent native initialization failures with exit status 125 remain under investigation. These workflows are for testing and do not certify compatibility with every project or tool.

## Install the preview

Use Node.js 22 or newer; Node.js 24 is recommended for this walkthrough. Check the exact published version before installing it globally:

```sh
npm view @delino/pnport@0.1.0-next.1 version &&
  npm install --global --ignore-scripts @delino/pnport@0.1.0-next.1
pnport --version
```

Keep optional dependencies enabled. Global installation keeps npm's physical `node_modules` directory out of the selected PnP project. See [installation and availability](/pnport/installation) for the project-local Yarn alternative and its package-age quarantine. With a project-local installation, replace `pnport` below with `yarn pnport`.

## Prepare a PnP project

Use an existing Yarn 4 project. Run the following commands at its root, using a test branch or disposable copy if these settings differ from your project's configuration:

```sh
yarn config set nodeLinker pnp
yarn config set enableGlobalCache false
yarn config set cacheFolder .yarn/cache
yarn install
```

For the Turbopack workflow, keep the Yarn cache inside Turbopack's configured filesystem root. These settings place it inside the project; in a monorepo, ensure the configured root includes the shared cache.

Confirm that `.pnp.cjs` exists, then check readiness from the workspace where the command will run:

```sh
pnport doctor --json
```

The report should contain `"ready": true`. This checks pnport's environment, not compatibility with a particular executable. pnport does not install dependencies or delete a conflicting physical `node_modules` directory. Resolve any reported conflict deliberately before retrying. See the [command reference](/pnport/commands) for project selection and binary resolution.

## Run Turbopack

Use a Next.js project with `next`, `react` and `react-dom` installed. Your Next.js version must support the Turbopack flags shown below; see the [Next.js CLI reference](https://nextjs.org/docs/app/api-reference/cli/next). In a monorepo, run these commands from the Next.js app workspace, where `next` is a direct dependency.

Start the development server:

```sh
pnport run -- next dev --turbopack
```

Open the URL printed by Next.js, load a page, and edit its source to check that the change appears in the browser. Stop the development server with Ctrl+C before testing the production build:

```sh
pnport run -- next build --turbopack
```

After a successful build, start the production server and check the page again:

```sh
pnport run -- next start
```

Stop it with Ctrl+C. Run the same build command again to check an unchanged rebuild, and record the Next.js version with `pnport run -- next --version`.

## Run TypeScript 7

TypeScript 7 provides the native compiler through the `typescript` package and the `tsc` command. See the [official TypeScript installation guide](https://www.typescriptlang.org/download/).

For a reproducible test, install the exact TypeScript 7.1 nightly used in pnport's macOS and Linux validation. Run this in the workspace you want to type-check:

```sh
yarn add --dev --exact typescript@7.1.0-dev.20260812.1
pnport run -- tsc --version
pnport run -- tsc --noEmit -p tsconfig.json
```

The pinned nightly has the macOS signing entitlements pnport requires. The `typescript@7.0.2` macOS binary lacks them and is rejected.

Confirm that a project without type errors exits with status 0. In a test copy, introduce a deliberate type error, rerun the check, and confirm that the compiler reports it and exits with a nonzero status; then restore the source.

For a project-reference build, use:

```sh
pnport run -- tsc -b
```

## Report results

Report the OS version and CPU architecture, the exact command, whether it passed, and the Next.js or TypeScript 7 version. Include the following version and readiness information:

```sh
pnport --version
node --version
yarn --version
pnport doctor --json
```

For a failure, include the exit status and error output. Remove credentials, private paths and project content before sharing diagnostics. Report reproducible results in [issue #958](https://github.com/delinoio/oss/issues/958), and see [diagnostics and troubleshooting](/pnport/diagnostics) for exit codes and recovery.
