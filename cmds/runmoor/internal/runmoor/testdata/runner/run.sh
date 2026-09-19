#!/bin/sh
set -eu
# This fixture retains the real official Runner.Listener for version/capability
# checks, but never contacts GitHub or consumes real JIT credentials. Integration
# tests execute local workload probes through the production container adapter.
[ "$#" -eq 2 ] && [ "$1" = --jitconfig ] && [ "$2" = fixture-jit ] || exit 78
trap 'exit 0' TERM INT
while [ ! -e /tmp/runmoor-finish ]; do sleep 1 & wait "$!"; done
