import { readFileSync, readdirSync, statSync } from "node:fs";
import { extname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = process.cwd();
const sourceRoots = ["apps/web-admin/src", "apps/desktop-client/src"];
const allowedFiles = new Set([
  "apps/web-admin/src/api/userError.ts",
  "apps/desktop-client/src/api/userError.ts"
]);
// 逐行规则用于发现常见内部错误泄露写法，不追踪变量数据流；白名单中的转换函数需单独评审。
const forbidden = [
  { name: "response.statusText 进入客户端文案", pattern: /response\.statusText/ },
  { name: "message.error 直接显示 Error.message", pattern: /message\.error\([^\n;]*\b\w*[Ee]rror\.message/ },
  { name: "错误描述直接显示 Error.message", pattern: /description=\{[^\n}]*\b\w*[Ee]rror\.message/ },
  { name: "错误状态直接保存 Error.message", pattern: /set\w*Error\([^\n;]*\b\w*[Ee]rror\.message/ },
  { name: "返回原生 Error.message", matches: hasRawErrorReturn },
  { name: "Error 三元表达式直接回退 message", pattern: /instanceof\s+Error\s*\?[^\n;]*\.message/ },
  { name: "状态标签回退到机器枚举", matches: hasStatusEnumFallback }
];

function hasRawErrorReturn(line) {
  // 分号仍结束原规则的返回表达式；只检查 return 之后的属性，避免重叠空白反复回溯。
  for (const expression of line.split(";")) {
    const prefix = /return\s/.exec(expression);
    if (!prefix) continue;
    let cursor = prefix.index + prefix[0].length;
    while (cursor < expression.length) {
      const property = expression.indexOf(".message", cursor);
      if (property === -1) break;
      const suffix = expression.slice(property - 5, property);
      // 原规则允许任意 ASCII 标识符前缀，只要求末尾为 Error 或 error。
      if (suffix === "Error" || suffix === "error") return true;
      cursor = property + ".message".length;
    }
  }
  return false;
}

function hasStatusEnumFallback(line) {
  const fallback = /\s*\?\?\s*(?:\w+\.)?status\b/y;
  let cursor = 0;
  while (cursor < line.length) {
    const start = line.indexOf("statusLabels[", cursor);
    if (start === -1) return false;
    const valueStart = start + "statusLabels[".length;
    // 沿用旧规则的首个右括号边界，不解析 TypeScript 的嵌套下标。
    const end = line.indexOf("]", valueStart);
    if (end === -1) return false;
    fallback.lastIndex = end + 1;
    if (end > valueStart && fallback.test(line)) return true;
    // 同一右括号前的嵌套标记共享回退部分；按括号段前进，避免反复扫描同一长行。
    cursor = end + 1;
  }
  return false;
}

export function checkUserFacingCopy(source, sourcePath) {
  const displayPath = sourcePath.replaceAll("\\", "/");
  // 测试替身不是生产文案；只排除约定的测试文件后缀，不能豁免同目录的业务源码。
  if (allowedFiles.has(displayPath) || ![".ts", ".tsx"].includes(extname(displayPath)) || /\.(?:test|spec)\.tsx?$/u.test(displayPath)) {
    return [];
  }
  const failures = [];
  const lines = source.split(/\r?\n/u);
  lines.forEach((line, index) => {
    if (/\bconsole\.(?:error|warn|info|debug)\b|\blogEvent\(/u.test(line)) return;
    for (const rule of forbidden) {
      if (rule.matches ? rule.matches(line) : rule.pattern.test(line)) {
        failures.push(`${displayPath}:${index + 1} ${rule.name}`);
      }
    }
  });
  return failures;
}

function main() {
  const failures = [];
  for (const sourceRoot of sourceRoots) {
    for (const file of walk(join(root, sourceRoot))) {
      if (![".ts", ".tsx"].includes(extname(file))) continue;
      failures.push(...checkUserFacingCopy(readFileSync(file, "utf8"), relative(root, file)));
    }
  }
  if (failures.length) {
    console.error(`用户可见文案静态检查失败：\n${failures.join("\n")}`);
    process.exitCode = 1;
    return;
  }
  console.log("用户可见文案静态检查通过。");
}

function* walk(directory) {
  for (const name of readdirSync(directory)) {
    const path = join(directory, name);
    if (statSync(path).isDirectory()) yield* walk(path);
    else yield path;
  }
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) main();
