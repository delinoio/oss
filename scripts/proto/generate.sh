#!/usr/bin/env bash

set -euo pipefail

buf lint
buf generate
