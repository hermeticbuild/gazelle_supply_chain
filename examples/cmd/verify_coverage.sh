#!/bin/bash
# Asserts that each SBOM graph maps BuildBuddy code to the license of the
# subtree it lives in: enterprise/ -> enterprise license, everything else -> MIT.
# Mattermost's server module is reported as AGPL-3.0-only.
set -euo pipefail

oss="$(find -L . -name oss_only_sbom.graph.json | head -1)"
ent="$(find -L . -name with_enterprise_sbom.graph.json | head -1)"

# Collapse each node's JSON so label and metadata_file can be matched together.
flat() { tr -d ' \n' < "$1"; }

fail() { echo "FAIL: $*" >&2; exit 1; }

flat "$ent" | grep -q 'enterprise/server/util/fieldgetter:fieldgetter","metadata_file":"[^"]*enterprise/package_metadata.package-metadata.json"' ||
  fail "enterprise/ package is not covered by the enterprise license"
flat "$ent" | grep -q 'server/util/random:random","metadata_file":"[^"]*_gazelle_generated/package_metadata.package-metadata.json"' ||
  fail "non-enterprise package is not covered by the MIT license"

flat "$oss" | grep -q 'server/util/random:random","metadata_file":"[^"]*_gazelle_generated/package_metadata.package-metadata.json"' ||
  fail "oss_only: non-enterprise package is not covered by the MIT license"
if flat "$oss" | grep -q "enterprise/package_metadata"; then
  fail "oss_only must not pull in the enterprise license"
fi
mm="$(find -L . -name mattermost.spdx.json | head -1)"
flat "$mm" | grep -q '"name":"v8"[^}]*"licenseDeclared":"AGPL-3.0-only"' ||
  fail "mattermost server code is not reported as AGPL-3.0-only"
echo "PASS"
