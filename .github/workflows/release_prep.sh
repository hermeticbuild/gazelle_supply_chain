#!/usr/bin/env bash

set -o errexit -o nounset -o pipefail

TAG=$1
PREFIX="gazelle_supply_chain-${TAG:1}"
ARCHIVE="gazelle_supply_chain-$TAG.tar.gz"

git archive --format=tar --prefix="${PREFIX}/" "${TAG}" | gzip > "${ARCHIVE}"

cat << EOF
## Add to your \`MODULE.bazel\` file:

\`\`\`starlark
bazel_dep(name = "gazelle_supply_chain", version = "${TAG:1}")
\`\`\`

EOF
