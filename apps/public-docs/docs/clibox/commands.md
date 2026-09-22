# Command index

All 19 commands are available through the native executable and the npm launcher. Use `pnpm exec clibox` or `npm exec -- clibox` for a project-local installation.

| Command | Purpose |
| --- | --- |
| [`clibox env run`](/clibox/system#run-with-environment-variables) | Set a child environment and execute a command. |
| [`clibox port list`](/clibox/system#inspect-and-terminate-port-owners) | Inspect local listening TCP or bound UDP port owners. |
| [`clibox port kill`](/clibox/system#inspect-and-terminate-port-owners) | Terminate revalidated port owners. |
| [`clibox open`](/clibox/system#open-a-resource) | Open a file, directory, or URI. |
| [`clibox clipboard copy`](/clibox/system#copy-and-paste-text) | Copy text from an argument or stdin. |
| [`clibox clipboard paste`](/clibox/system#copy-and-paste-text) | Write desktop clipboard text to stdout. |
| [`clibox text replace`](/clibox/transformations#text-replacement) | Replace literal text or regex matches. |
| [`clibox time format`](/clibox/transformations#time-formatting-and-arithmetic) | Format an explicit timestamp or the current instant. |
| [`clibox time add`](/clibox/transformations#time-formatting-and-arithmetic) | Apply calendar or elapsed-time arithmetic. |
| [`clibox base64 encode`](/clibox/transformations#base64) | Encode bytes as Base64. |
| [`clibox base64 decode`](/clibox/transformations#base64) | Decode Base64 into bytes. |
| [`clibox hash compute`](/clibox/transformations#hashes-and-verification) | Compute SHA-256, SHA-512, or BLAKE3. |
| [`clibox hash verify`](/clibox/transformations#hashes-and-verification) | Verify a digest or GNU checksum manifest. |
| [`clibox wait tcp`](/clibox/wait#tcp) | Wait for a successful TCP connection. |
| [`clibox wait http`](/clibox/wait#http) | Wait for an HTTP response status. |
| [`clibox wait file`](/clibox/wait#files) | Wait for a regular file to exist. |
| [`clibox dotenv list`](/clibox/configuration#list-dotenv-keys) | List sorted unique dotenv keys. |
| [`clibox dotenv merge`](/clibox/configuration#merge-dotenv-files) | Merge dotenv layers in order. |
| [`clibox yaml normalize`](/clibox/configuration#normalize-yaml) | Expand YAML references and normalize formatting. |

## Help and version

`clibox`, `clibox -h`, and `clibox --help` print root help to stdout and exit successfully. `clibox -V` or `clibox --version` prints the installed version. Add `-h` to a command for core rules and examples or `--help` for detailed constraints.

Running a group without its required subcommand prints that group's help to stderr and returns 2. Other invalid arguments also return 2, with redacted diagnostics. See [Output and cancellation](/clibox/output) for runtime statuses and [Migration](/clibox/migration) for removed names.

The executable is the public interface; no JavaScript import or public Rust library API is provided.
