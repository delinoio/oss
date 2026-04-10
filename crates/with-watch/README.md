# with-watch

`with-watch` reruns a delegated command when its inferred or explicit filesystem inputs change.

## Examples

```sh
with-watch cp src.txt dest.txt
with-watch ls -l
with-watch --shell 'cat src.txt | grep hello'
with-watch sed -i.bak -e 's/old/new/' config.txt
with-watch exec --input 'src/**/*.rs' -- cargo test -p with-watch
```

## Inference Model

- Passthrough and shell modes use built-in command adapters before falling back to conservative path heuristics.
- Known outputs, inline scripts, patterns, and shell output redirects are filtered out of the watch set.
- Pathless defaults are intentionally narrow: only `ls`, `dir`, `vdir`, `du`, and `find` implicitly watch the current directory.
- `exec --input` remains the explicit escape hatch when a delegated command has no meaningful filesystem inputs or when fallback inference would be ambiguous.
