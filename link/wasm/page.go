package wasm

import (
	"encoding/base64"
	"html"
	"strings"
)

// Page returns an HTML page that runs a module of the platform web in a
// browser (target.format.html): the module is in the page, so that it
// runs from a file; standard output and standard error appear in the
// console of the page; standard input is what the user types into its
// input field, each character as it is typed and Enter as a line feed,
// and a read waits for it with JavaScript Promise Integration. The query
// parameter input gives input before the first key, and the console has
// the attribute data-status when the program ends.
func Page(title string, module []byte) []byte {
	r := strings.NewReplacer("{{title}}", html.EscapeString(title), "{{module}}", base64.StdEncoding.EncodeToString(module))
	return []byte(r.Replace(pageTemplate))
}

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{title}}</title>
<style>
:root { --bg: #fbfbfa; --fg: #1d1d1b; --muted: #6b6b66; --line: #d8d8d2; --panel: #ffffff; }
@media (prefers-color-scheme: dark) {
	:root { --bg: #161615; --fg: #e8e8e3; --muted: #9a9a93; --line: #3a3a36; --panel: #1f1f1d; }
}
body { margin: 0; padding: 16px; background: var(--bg); color: var(--fg); font: 15px/1.45 system-ui, sans-serif; }
main { max-width: 760px; margin: 0 auto; }
h1 { font-size: 17px; font-weight: 600; margin: 0 0 12px; }
#console { background: var(--panel); border: 1px solid var(--line); border-radius: 6px; padding: 12px; min-height: 12em;
	margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; font: 14px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace; }
label { display: block; margin: 12px 0 4px; color: var(--muted); font-size: 13px; }
#input { width: 100%; box-sizing: border-box; padding: 8px; font: 14px ui-monospace, monospace; color: var(--fg);
	background: var(--panel); border: 1px solid var(--line); border-radius: 6px; }
#status { color: var(--muted); font-size: 13px; min-height: 1.4em; margin: 8px 0 0; }
</style>
</head>
<body>
<main>
<h1>{{title}}</h1>
<pre id="console" aria-live="polite"></pre>
<label for="input">Input: each character is sent as it is typed, Enter sends a line feed</label>
<input id="input" autocomplete="off" autocapitalize="off" spellcheck="false">
<p id="status"></p>
</main>
<script>
"use strict";
const moduleBase64 = "{{module}}";
const out = document.getElementById("console");
const field = document.getElementById("input");
const status = document.getElementById("status");
const encoder = new TextEncoder();
const decoders = [null, new TextDecoder(), new TextDecoder()];
let pending = Array.from(encoder.encode(new URLSearchParams(location.search).get("input") || ""));
let wake = null;
function give(bytes) {
	pending.push(...bytes);
	if (wake) { const w = wake; wake = null; w(); }
}
field.addEventListener("input", () => { give(encoder.encode(field.value)); field.value = ""; });
field.addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); give([10]); } });
class Exit { constructor(code) { this.code = code; } }
let memory = null;
const view = () => new DataView(memory.buffer);
function iovecs(iovs, n) {
	const v = [];
	for (let k = 0; k < n; k++) v.push([view().getUint32(iovs + 8 * k, true), view().getUint32(iovs + 8 * k + 4, true)]);
	return v;
}
function fdWrite(fd, iovs, n, written) {
	if (fd !== 1 && fd !== 2) return 8;
	let total = 0;
	for (const [p, l] of iovecs(iovs, n)) {
		out.textContent += decoders[fd].decode(new Uint8Array(memory.buffer, p, l), { stream: true });
		total += l;
	}
	view().setUint32(written, total, true);
	return 0;
}
function readNow(iovs, n, nread) {
	let total = 0;
	for (const [p, l] of iovecs(iovs, n)) {
		const k = Math.min(l, pending.length);
		new Uint8Array(memory.buffer, p, k).set(pending.splice(0, k));
		total += k;
		if (k < l) break;
	}
	view().setUint32(nread, total, true);
	return 0;
}
async function fdRead(fd, iovs, n, nread) {
	if (fd !== 0) return 8;
	while (pending.length === 0) {
		status.textContent = "Waiting for input";
		field.focus();
		await new Promise(r => { wake = r; });
	}
	status.textContent = "";
	return readNow(iovs, n, nread);
}
function procExit(code) { throw new Exit(code); }
function finish(code, message) {
	status.textContent = message;
	out.dataset.status = String(code);
}
async function run() {
	const bytes = Uint8Array.from(atob(moduleBase64), c => c.charCodeAt(0));
	const jspi = typeof WebAssembly.Suspending === "function" && typeof WebAssembly.promising === "function";
	const wasi = {
		fd_write: fdWrite,
		proc_exit: procExit,
		fd_read: jspi ? new WebAssembly.Suspending(fdRead) : (fd, iovs, n, nread) => fd === 0 ? readNow(iovs, n, nread) : 8,
	};
	const { instance } = await WebAssembly.instantiate(bytes, { wasi_snapshot_preview1: wasi });
	memory = instance.exports.memory;
	const start = jspi ? WebAssembly.promising(instance.exports._start) : instance.exports._start;
	if (!jspi) status.textContent = "This browser cannot wait for input: reads see only the input given before the start.";
	try {
		await start();
		finish(0, "The program ended.");
	} catch (e) {
		if (e instanceof Exit) finish(e.code, e.code === 0 ? "The program ended." : "The program ended with status " + e.code + ".");
		else finish(-1, "The program stopped: " + e);
	}
	field.disabled = true;
}
run();
</script>
</body>
</html>
`
