#!/bin/sh
set -eu
export XDG_RUNTIME_DIR=/tmp/delidev-cli-test-runtime
mkdir -m 700 "$XDG_RUNTIME_DIR"
printf '%s' 'disposable-container-keyring-password' | gnome-keyring-daemon --unlock --components=secrets
export DELIDEV_TEST_ISOLATED_SECRET_SERVICE=1
export DELIDEV_TEST_CLI_BINARY=/opt/delidev-tests/delidev
exec /opt/delidev-tests/cli.test -test.v -test.run '^TestNativeAccountCLISecretService$'
