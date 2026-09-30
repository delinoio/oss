# PR #1232 final browser-removal discovery

Parent: `362fa0e83`, branch `kdy1/delidev-browser-1087-replacement`, 2026-09-30.

The [review finding](https://github.com/delinoio/oss/pull/1232#discussion_r4144983929)
identified a deletion lost between the last observation and quit during the
five-second poll sleep. Quit now denies presentations and closes children
immediately, while the tracked poll worker performs a final discovery pass.
Both zero-child exit and the last child's close callback wait for discovery.
The final pass has an eight-second scheduling budget and preserves each
in-flight sidecar child's existing two-second join bound. Remote scopes beyond
the budget remain deferred. Network and durable intent writes stay off the UI
loop; profile purge still requires joined workers and independently returned CEF
shutdown.

`cargo test -p delidev-desktop --features desktop-host,custom-protocol` passed
against the unchanged pinned CEF: 20 library tests and 16 desktop-host tests,
with four separately qualified real-sidecar tests ignored. New controlled
fixtures verify deletion after a successful poll, final discovery despite the
stop signal, durable intent creation before purge, and exit gating with both
zero and outstanding native children. An expired budget starts no sidecar read.
These fixtures do not launch a real renderer or establish native close/flush,
provider, cross-platform or release acceptance. Full repository repair checks
and their unresolved limits are recorded separately.
