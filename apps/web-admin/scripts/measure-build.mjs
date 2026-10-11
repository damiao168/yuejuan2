import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";
import ts from "typescript";

function filesIn(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? filesIn(path) : [path];
  });
}

function localAsset(directory, importer, target) {
  if (/^(?:[a-z]+:|\/\/)/i.test(target)) return null;
  const clean = target.split(/[?#]/)[0];
  const path = resolve(directory, clean.startsWith("/") ? `.${clean}` : join(dirname(importer), clean));
  if (relative(directory, path).startsWith("..")) throw new Error(`Asset escapes build directory: ${target}`);
  return relative(directory, path).replaceAll("\\", "/");
}

// 产物路径按固定 UTF-16 顺序列出，报告不能随运行机器的语言设置改变。
function compareAssetPaths(left, right) {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

// 从实际产物的静态 import/export 计算首屏依赖；动态 import 和 PDF worker 不计入首屏。
// gzip 按文件分别压缩后求和，对应多文件传输大小，不表示真实网络时延或浏览器解析耗时。
export function measureBuild(directory) {
  const root = resolve(directory);
  const html = readFileSync(join(root, "index.html"), "utf8");
  const entryScripts = [...html.matchAll(/<script\b[^>]*\bsrc=["']([^"']+)["'][^>]*>/g)]
    .map((match) => localAsset(root, "index.html", match[1])).filter(Boolean);
  if (!entryScripts.length) throw new Error("Build has no application entry script.");
  const links = [...html.matchAll(/<link\b[^>]*>/g)].map(([tag]) => ({
    rel: /\brel=["']([^"']+)["']/.exec(tag)?.[1], href: /\bhref=["']([^"']+)["']/.exec(tag)?.[1]
  }));
  const preload = links.filter((link) => link.rel === "modulepreload" && link.href)
    .map((link) => localAsset(root, "index.html", link.href)).filter(Boolean);
  const styles = links.filter((link) => link.rel === "stylesheet" && link.href)
    .map((link) => localAsset(root, "index.html", link.href)).filter(Boolean);
  const visited = new Set();
  const dynamic = new Set();
  const visit = (asset) => {
    if (visited.has(asset)) return;
    visited.add(asset);
    const source = ts.createSourceFile(asset, readFileSync(join(root, asset), "utf8"), ts.ScriptTarget.Latest, false, ts.ScriptKind.JS);
    const walk = (node) => {
      if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
        const imported = localAsset(root, asset, node.moduleSpecifier.text);
        if (imported && /\.[cm]?js$/.test(imported)) visit(imported);
      }
      if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword && ts.isStringLiteral(node.arguments[0])) {
        const imported = localAsset(root, asset, node.arguments[0].text);
        if (imported) dynamic.add(imported);
      }
      ts.forEachChild(node, walk);
    };
    walk(source);
  };
  [...entryScripts, ...preload].forEach(visit);
  const assetSize = (asset) => {
    const bytes = readFileSync(join(root, asset));
    return { file: asset, bytes: bytes.length, gzipBytes: gzipSync(bytes).length };
  };
  const initialJavaScript = [...visited].sort(compareAssetPaths).map(assetSize);
  const initialCSS = [...new Set(styles)].sort(compareAssetPaths).map(assetSize);
  const totals = (assets) => assets.reduce((sum, asset) => ({ bytes: sum.bytes + asset.bytes, gzipBytes: sum.gzipBytes + asset.gzipBytes }), { bytes: 0, gzipBytes: 0 });
  return {
    measuredAt: new Date().toISOString(), directory: root,
    manifestAvailable: existsSync(join(root, ".vite/manifest.json")),
    initialJavaScript, initialJavaScriptTotal: totals(initialJavaScript), initialCSS, initialCSSTotal: totals(initialCSS),
    dynamicImportsFromInitial: [...dynamic].filter((asset) => !visited.has(asset)).sort(compareAssetPaths),
    javascriptAssets: filesIn(root).filter((path) => /\.[cm]?js$/.test(path)).map((path) => assetSize(relative(root, path).replaceAll("\\", "/"))).sort((a, b) => b.bytes - a.bytes)
  };
}

export function checkBuildBudget(report, budget) {
  if (!Number.isInteger(budget.maxInitialJavaScriptGzipBytes) || budget.maxInitialJavaScriptGzipBytes <= 0) {
    throw new Error("Build budget must declare a positive integer maxInitialJavaScriptGzipBytes.");
  }
  const failures = [];
  if (report.initialJavaScriptTotal.gzipBytes > budget.maxInitialJavaScriptGzipBytes) failures.push("Initial JavaScript exceeds the gzip budget.");
  for (const pattern of budget.blockedInitialChunkPatterns ?? []) {
    const blocked = report.initialJavaScript.filter((asset) => new RegExp(pattern).test(asset.file));
    if (blocked.length) failures.push(`Deferred feature entered initial graph: ${blocked.map((asset) => asset.file).join(", ")}`);
  }
  return failures;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  const argument = (name) => {
    if (!args.includes(name)) return undefined;
    const value = args[args.indexOf(name) + 1];
    if (!value || value.startsWith("--")) throw new Error(`Missing value for ${name}.`);
    return value;
  };
  const report = measureBuild(argument("--dist") ?? resolve(dirname(fileURLToPath(import.meta.url)), "../dist"));
  const output = argument("--out");
  if (output) { mkdirSync(dirname(resolve(output)), { recursive: true }); writeFileSync(output, `${JSON.stringify(report, null, 2)}\n`); }
  console.log(`Initial JavaScript: ${report.initialJavaScriptTotal.bytes} bytes, ${report.initialJavaScriptTotal.gzipBytes} gzip bytes (${report.initialJavaScript.length} chunks).`);
  console.log(`Initial CSS: ${report.initialCSSTotal.bytes} bytes, ${report.initialCSSTotal.gzipBytes} gzip bytes.`);
  const budget = argument("--budget");
  if (budget) {
    const failures = checkBuildBudget(report, JSON.parse(readFileSync(budget, "utf8")));
    if (failures.length) { failures.forEach((failure) => console.error(failure)); process.exitCode = 1; }
  }
}
