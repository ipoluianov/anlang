#!/bin/sh
# Downloads the WebAssembly 2.0 specification test suite, the .wast
# scripts of tag wg-2.0 of the WebAssembly spec repository, into
# external/wasm/spec. The WebAssembly interpreter of the toolchain is
# checked against them (conf.oracle.use); they are not committed or
# distributed (spec/24-roadmap.md, step 18).
set -e
dir=$(cd "$(dirname "$0")/.." && pwd)/external/wasm
mkdir -p "$dir"
if [ ! -d "$dir/spec/test/core" ]; then
	rm -rf "$dir/spec"
	git clone -q --depth 1 --branch wg-2.0 https://github.com/WebAssembly/spec.git "$dir/spec"
fi
ls "$dir/spec/test/core"/*.wast "$dir/spec/test/core"/simd/*.wast | wc -l
