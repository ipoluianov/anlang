#!/bin/sh
# Downloads the 8086 processor test suite of SingleStepTests (MIT license,
# https://github.com/SingleStepTests/8086) into external/8086. The tests
# are used only by the tests of the toolchain and are not committed
# (spec/24-roadmap.md, step 12).
set -e
dir=$(dirname "$0")/../external/8086
mkdir -p "$dir"
base=https://raw.githubusercontent.com/SingleStepTests/8086/main/v1
curl -sfL -o "$dir/metadata.json" "$base/metadata.json"
curl -sfL https://api.github.com/repos/SingleStepTests/8086/git/trees/main?recursive=1 |
	grep -o '"path": *"v1/[^"]*\.json\.gz"' | sed 's/.*"v1\///; s/"$//' |
	while read -r f; do
		[ -s "$dir/$f" ] || curl -sfL -o "$dir/$f" "$base/$f"
	done
echo "8086 tests in $dir: $(ls "$dir" | wc -l) files"
