// Smoke-Test im echten Browser (Headless-Chrome) gegen den Testbetrieb (./dev.sh up):
//   node tool/browser_smoke.mjs
// Meldet sich über Mailpit an und legt per Formular einen Raum an (und löscht ihn wieder).
import { spawn } from "node:child_process";
const CHROME = process.env.CHROME || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const chrome = spawn(CHROME,
  ["--headless=new", "--remote-debugging-port=9333", "--user-data-dir=" + (await import("node:fs")).mkdtempSync((await import("node:os")).tmpdir() + "/talkie-smoke-"), "about:blank"], { stdio: "ignore" });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let ws, id = 0; const pending = new Map();
for (let i = 0; i < 50 && !ws; i++) {
  try { const t = await (await fetch("http://127.0.0.1:9333/json")).json();
    const p = t.find((x) => x.type === "page"); ws = new WebSocket(p.webSocketDebuggerUrl); } catch { await sleep(200); }
}
await new Promise((r) => ws.addEventListener("open", r));
ws.addEventListener("message", (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); } });
const cmd = (method, params = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });
const js = async (e) => (await cmd("Runtime.evaluate", { expression: e, awaitPromise: true })).result.value;
await cmd("Page.navigate", { url: "http://localhost:8090/login" }); await sleep(1000);
await js(`document.querySelector("input[name=email]").value="admin@talkie.local"; document.querySelector("form").submit(); 1`);
await sleep(1500);
const fail = (msg) => { console.log("FEHLER:", msg); chrome.kill(); process.exit(1); };
if (await js("location.pathname") !== "/login/code") fail("E-Mail-Formular: " + await js("document.body.innerText.slice(0,120)"));
console.log("OK  E-Mail-Formular");
const mails = await (await fetch("http://127.0.0.1:8026/api/v1/messages")).json();
const code = mails.messages[0].Subject.split(": ")[1];
await js(`document.querySelector("input[name=code]").value="${code}"; document.querySelector("form").submit(); 1`);
await sleep(1500);
if (await js("location.pathname") !== "/") fail("Code-Formular: " + await js("document.body.innerText.slice(0,120)"));
console.log("OK  Anmeldung mit Code aus Mailpit");
await js(`location.href="/rooms"; 1`); await sleep(1000);
await js(`document.querySelector("form[action='/rooms'] input[name=name]").value="Browsertest"; document.querySelector("form[action='/rooms']").submit(); 1`);
await sleep(1500);
const flash = await js("document.querySelector('.flash')?.innerText || ''");
if (!flash.includes("angelegt")) fail("Raum anlegen: " + flash);
console.log("OK  Formular mit CSRF-Token (Raum angelegt)");
await js(`[...document.querySelectorAll("form")].find((f) => f.action.endsWith("/delete") && f.closest("tr")?.querySelector("input[value=Browsertest]"))?.submit(); 1`);
await sleep(1000);
console.log("Alles bestanden.");
chrome.kill(); process.exit(0);
