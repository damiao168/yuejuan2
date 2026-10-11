import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, rmdirSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { checkBuildBudget, measureBuild } from "./measure-build.mjs";

function fixture(t, files) {
  const directory = mkdtempSync(join(tmpdir(), "edugrade-build-measure-"));
  mkdirSync(join(directory, "assets"));
  for (const [file, content] of Object.entries(files)) writeFileSync(join(directory, file), content);
  // 只清理本测试明确创建的文件和空目录，不递归删除根据外部输入计算的路径。
  t.after(() => { for (const file of Object.keys(files)) unlinkSync(join(directory, file)); rmdirSync(join(directory, "assets")); rmdirSync(directory); });
  return directory;
}

test("measures static imports, re-exports and preload once, excluding dynamic pages", (t) => {
  const directory = fixture(t, {
    "index.html": '<script type="module" src="/assets/index.js"></script><link rel="modulepreload" href="/assets/vendor.js"><link rel="stylesheet" href="/assets/index.css">',
    "assets/index.js": 'import "./vendor.js"; export { value } from "./shared.js"; const page = () => import("./page.js");',
    "assets/vendor.js": 'import "./shared.js";', "assets/shared.js": 'import "./vendor.js"; export const value = 1;',
    "assets/page.js": 'export const page = true;', "assets/index.css": 'body { color: black; }'
  });
  const report = measureBuild(directory);
  assert.deepEqual(report.initialJavaScript.map((asset) => asset.file), ["assets/index.js", "assets/shared.js", "assets/vendor.js"]);
  assert.deepEqual(report.dynamicImportsFromInitial, ["assets/page.js"]);
  assert.equal(report.initialJavaScriptTotal.bytes, report.initialJavaScript.reduce((sum, asset) => sum + asset.bytes, 0));
  assert.equal(report.initialCSS.length, 1);
});

test("keeps asset report paths in fixed UTF-16 order across case, digits and accents", (t) => {
  const staticNames = ["a", "Z", "_part", "chunk-2", "chunk-10", "\u00e4"];
  const dynamicNames = ["page-a", "page-Z", "page-_part", "page-2", "page-10", "page-\u00e4"];
  const styleNames = ["a", "Z", "_theme", "theme-2", "theme-10", "\u00e4"];
  const directory = fixture(t, {
    "index.html": '<script type="module" src="/assets/index.js"></script><link rel="modulepreload" href="/assets/Z.js">'
      + [...styleNames, "a"].map((name) => `<link rel="stylesheet" href="/assets/${name}.css">`).join(""),
    "assets/index.js": staticNames.map((name) => `import "./${name}.js";`).join("\n")
      + dynamicNames.map((name) => `\nimport("./${name}.js");`).join(""),
    ...Object.fromEntries([...staticNames, ...dynamicNames].map((name) => [`assets/${name}.js`, "export const ready = true;"])),
    ...Object.fromEntries(styleNames.map((name) => [`assets/${name}.css`, "body { color: black; }"]))
  });
  const report = measureBuild(directory);
  assert.deepEqual(report.initialJavaScript.map((asset) => asset.file), [
    "assets/Z.js", "assets/_part.js", "assets/a.js", "assets/chunk-10.js", "assets/chunk-2.js", "assets/index.js", "assets/\u00e4.js"
  ]);
  assert.deepEqual(report.initialCSS.map((asset) => asset.file), [
    "assets/Z.css", "assets/_theme.css", "assets/a.css", "assets/theme-10.css", "assets/theme-2.css", "assets/\u00e4.css"
  ]);
  assert.deepEqual(report.dynamicImportsFromInitial, [
    "assets/page-10.js", "assets/page-2.js", "assets/page-Z.js", "assets/page-_part.js", "assets/page-a.js", "assets/page-\u00e4.js"
  ]);
});

test("rejects a missing static asset instead of reporting an artificially small entry", (t) => {
  const directory = fixture(t, { "index.html": '<script src="/assets/index.js"></script>', "assets/index.js": 'import "./missing.js";' });
  assert.throws(() => measureBuild(directory), /ENOENT/);
});

test("rejects imported assets outside the measured build", (t) => {
  const directory = fixture(t, { "index.html": '<script src="/assets/index.js"></script>', "assets/index.js": 'import "../../outside.js";' });
  assert.throws(() => measureBuild(directory), /escapes build directory/);
});

test("budget rejects both oversized entry and a deferred feature made static", () => {
  const report = { initialJavaScriptTotal: { gzipBytes: 200 }, initialJavaScript: [{ file: "assets/conversationPdf.js" }] };
  assert.equal(checkBuildBudget(report, { maxInitialJavaScriptGzipBytes: 100, blockedInitialChunkPatterns: ["conversationPdf"] }).length, 2);
  assert.deepEqual(checkBuildBudget({ initialJavaScriptTotal: { gzipBytes: 90 }, initialJavaScript: [{ file: "assets/index.js" }] }, { maxInitialJavaScriptGzipBytes: 100, blockedInitialChunkPatterns: ["conversationPdf"] }), []);
});

test("rejects a missing budget limit instead of silently disabling the gate", () => {
  assert.throws(() => checkBuildBudget({ initialJavaScriptTotal: { gzipBytes: 200 } }, {}), /positive integer/);
});

test("CLI rejects an omitted budget path instead of passing without a budget", (t) => {
  const directory = fixture(t, { "index.html": '<script src="/assets/index.js"></script>', "assets/index.js": "export const ready = true;" });
  const result = spawnSync(process.execPath, [fileURLToPath(new URL("./measure-build.mjs", import.meta.url)), "--dist", directory, "--budget"], { encoding: "utf8" });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Missing value for --budget/);
});
