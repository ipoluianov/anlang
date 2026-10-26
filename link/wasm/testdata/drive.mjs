// drive.mjs runs a page in headless Chrome and types into it over the
// DevTools protocol: node drive.mjs CHROME URL STEP... where a STEP is
// "wait:TEXT" (wait until the console of the page contains TEXT),
// "type:TEXT" (type TEXT into the input field) or "enter". It prints the
// console of the page and its status at the end.
import { spawn } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const [chromePath, url, ...steps] = process.argv.slice(2);
const profile = mkdtempSync(join(tmpdir(), "anlang-chrome-"));
const chrome = spawn(chromePath, ["--headless=new", "--disable-gpu", "--no-sandbox", "--no-first-run",
	"--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank"], { stdio: ["ignore", "ignore", "pipe"] });
const endpoint = await new Promise((resolve, reject) => {
	let text = "";
	chrome.stderr.on("data", d => {
		text += d;
		const m = text.match(/DevTools listening on (ws:\/\/\S+)/);
		if (m) resolve(m[1]);
	});
	chrome.on("exit", () => reject(new Error("chrome exited: " + text)));
});
const ws = new WebSocket(endpoint);
await new Promise(r => ws.addEventListener("open", r));
let id = 0;
const waiting = new Map();
ws.addEventListener("message", e => {
	const msg = JSON.parse(e.data);
	if (msg.id && waiting.has(msg.id)) {
		waiting.get(msg.id)(msg);
		waiting.delete(msg.id);
	}
});
function send(method, params = {}, sessionId) {
	const n = ++id;
	ws.send(JSON.stringify({ id: n, method, params, sessionId }));
	return new Promise(r => waiting.set(n, r));
}
const { result: { targetId } } = await send("Target.createTarget", { url: "about:blank" });
const { result: { sessionId } } = await send("Target.attachToTarget", { targetId, flatten: true });
await send("Page.enable", {}, sessionId);
await send("Page.navigate", { url }, sessionId);
const evaluate = async expr => (await send("Runtime.evaluate", { expression: expr, returnByValue: true }, sessionId)).result?.result?.value;
const consoleText = () => evaluate(`document.getElementById("console")?.textContent ?? ""`);
async function waitFor(text) {
	for (let k = 0; k < 200; k++) {
		if ((await consoleText()).includes(text)) return;
		await new Promise(r => setTimeout(r, 50));
	}
	throw new Error("the console does not show " + JSON.stringify(text) + ":\n" + await consoleText());
}
try {
	for (const step of steps) {
		if (step.startsWith("wait:")) await waitFor(step.slice(5));
		else if (step.startsWith("type:")) {
			await evaluate(`document.getElementById("input").focus()`);
			await send("Input.insertText", { text: step.slice(5) }, sessionId);
		} else if (step === "enter") {
			await send("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 }, sessionId);
			await send("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13 }, sessionId);
		}
	}
	for (let k = 0; k < 100 && (await evaluate(`document.getElementById("console").dataset.status ?? ""`)) === ""; k++) {
		await new Promise(r => setTimeout(r, 50));
	}
	console.log(await consoleText());
	console.log("status: " + await evaluate(`document.getElementById("console").dataset.status ?? "running"`));
} finally {
	ws.close();
	chrome.kill();
}
