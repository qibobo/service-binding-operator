#!/usr/bin/env bash
set -eE -o pipefail

WORKSPACE="$(cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)"

pushd ${WORKSPACE}

# make test-unit-with-coverage
