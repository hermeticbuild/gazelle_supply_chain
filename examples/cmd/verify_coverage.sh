#!/bin/bash
# Asserts that each SBOM graph maps BuildBuddy code to the license of the
# subtree it lives in: enterprise/ -> enterprise license, everything else -> MIT.
set -euo pipefail

oss="$(find -L . -name oss_only_sbom.graph.json | head -1)"
ent="$(find -L . -name with_enterprise_sbom.graph.json | head -1)"

# Collapse each node's JSON so label and metadata_file can be matched together.
flat() { tr -d ' \n' < "$1"; }

fail() { echo "FAIL: $*" >&2; exit 1; }

flat "$ent" | grep -q 'enterprise/server/util/fieldgetter:fieldgetter","metadata_file":"[^"]*buildbuddy_enterprise.package-metadata.json"' ||
  fail "enterprise/ package is not covered by the enterprise license"
flat "$ent" | grep -q 'server/util/random:random","metadata_file":"[^"]*buildbuddy_mit.package-metadata.json"' ||
  fail "non-enterprise package is not covered by the MIT license"

flat "$oss" | grep -q 'server/util/random:random","metadata_file":"[^"]*buildbuddy_mit.package-metadata.json"' ||
  fail "oss_only: non-enterprise package is not covered by the MIT license"
if flat "$oss" | grep -q 'buildbuddy_enterprise'; then
  fail "oss_only must not pull in the enterprise license"
fi
echo "PASS"
