#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0




















































































set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

commit="$(git rev-parse HEAD 2>/dev/null || echo unknown)"















if [ -z "$(git status --porcelain 2>/dev/null -- . ':!.criteria-receipts')" ]; then
  state=clean
else
  state=dirty
fi

printf '%s %s\n' "$commit" "$state"
