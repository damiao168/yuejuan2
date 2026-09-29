import { readFileSync, readdirSync, statSync } from "node:fs";
import { extname, join, relative } from "node:path";

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
  { name: "返回原生 Error.message", pattern: /return\s+[^\n;]*\b\w*[Ee]rror\.message/ },
  { name: "Error 三元表达式直接回退 message", pattern: /instanceof\s+Error\s*\?[^\n;]*\.message/ },
  { name: "状态标签回退到机器枚举", pattern: /statusLabels\[[^\]]+\]\s*\?\?\s*(?:\w+\.)?status\b/ }
];

const failures = [];
for (const sourceRoot of sourceRoots) {
  for (const file of walk(join(root, sourceRoot))) {
    const displayPath = relative(root, file).replaceAll("\\", "/");
    if (allowedFiles.has(displayPath) || ![".ts", ".tsx"].includes(extname(file))) continue;
    const lines = readFileSync(file, "utf8").split(/\r?\n/u);
    lines.forEach((line, index) => {
      if (/\bconsole\.(?:error|warn|info|debug)\b|\blogEvent\(/u.test(line)) return;
      for (const rule of forbidden) {
        if (rule.pattern.test(line)) failures.push(`${displayPath}:${index + 1} ${rule.name}`);
      }
    });
  }
}

if (failures.length) {
  console.error(`用户可见文案静态检查失败：\n${failures.join("\n")}`);
  process.exit(1);
}
console.log("用户可见文案静态检查通过。");

function* walk(directory) {
  for (const name of readdirSync(directory)) {
    const path = join(directory, name);
    if (statSync(path).isDirectory()) yield* walk(path);
    else yield path;
  }
}
