# with-watch

`with-watch` reruns a delegated command when its inferred or explicit filesystem inputs change.

## Install

```sh
cargo install with-watch
brew install delinoio/tap/with-watch
```

## Examples

```sh
with-watch cp src.txt dest.txt
with-watch --shell 'cat src.txt | grep hello'
with-watch exec --input 'src/**/*.rs' -- cargo test -p with-watch
```
