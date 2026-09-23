# pnport

**pnport is in development. Version 0.1.0 has not been released, and installation packages are not available yet.** This guide describes the intended public interface so you can evaluate whether it fits your workflow; it is not a compatibility claim for an unpublished build.

pnport runs commands inside an already installed Yarn 4 Plug'n'Play project. It presents a virtual `node_modules` view to supported subprocesses without creating a project `node_modules` directory. pnport does not install dependencies or change your Yarn lockfile.

The first release targets macOS 13+, Windows 10 22H2+ with MSVC binaries, and Ubuntu 22.04-equivalent GNU libc environments, each on x64 and arm64. Linux support includes static child executables. Musl hosts, mixed architectures, and arbitrary executable compatibility are outside this release.

Start with [installation and availability](/pnport/installation), then read the [command reference](/pnport/commands) and [filesystem behavior](/pnport/filesystem). [Editors](/pnport/editors), [cache management](/pnport/cache), [diagnostics](/pnport/diagnostics), and [release verification](/pnport/releases) cover longer-running use and recovery.

Support and issue reports use [GitHub issues](https://github.com/delinoio/oss/issues). There is no support-response deadline.
