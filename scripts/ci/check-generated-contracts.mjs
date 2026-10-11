import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { checkOpenApiRouteDebt } from "../check-openapi-route-debt.mjs";

const generatedPaths = ["packages/sdk/src/generated", "services/api-gateway/openapi/route-coverage.json"];

export function snapshotGenerated(root, paths = generatedPaths) {
  const files = new Map();
  function visit(relative) {
    const absolute = path.join(root, relative);
    let directory;
    try {
      directory = fs.opendirSync(absolute);
    } catch (error) {
      if (error.code === "ENOENT") return;
      if (error.code !== "ENOTDIR") throw error;
      let descriptor;
      try {
        descriptor = fs.openSync(absolute, fs.constants.O_RDONLY | (fs.constants.O_NONBLOCK ?? 0));
      } catch (openError) {
        if (openError.code === "ENOENT") return;
        throw openError;
      }
      try {
        // 路径只在打开时解析一次；链接沿用系统解析规则，类型校验和读取使用同一文件，不能据此承诺目录隔离。
        if (!fs.fstatSync(descriptor).isFile()) throw new Error(`Generated artifact is not a regular file: ${relative}`);
        files.set(relative, fs.readFileSync(descriptor));
      } finally { fs.closeSync(descriptor); }
      return;
    }
    const names = [];
    try {
      // 使用已经打开的目录枚举，避免先检查目录类型，再重新按路径打开它。
      for (let entry = directory.readSync(); entry !== null; entry = directory.readSync()) names.push(entry.name);
    } finally { directory.closeSync(); }
    names.sort((left, right) => left.localeCompare(right));
    for (const name of names) visit(`${relative}/${name}`);
  }
  paths.forEach(visit);
  return files;
}

export function changedGenerated(before, after) {
  return [...new Set([...before.keys(), ...after.keys()])].sort((left, right) => left.localeCompare(right)).filter((name) =>
    !before.has(name) || !after.has(name) || !before.get(name).equals(after.get(name)));
}

// 与传入的工作区比较，而不是 HEAD：正确但尚未提交的产物
// 仍然有效，但缺失或过期的生成内容会使本地和 CI 检查失败。
export function checkGeneratedContracts(root = process.cwd()) {
  const before = snapshotGenerated(root);
  for (const script of ["scripts/generate-sdk.mjs", "scripts/update-api-route-coverage.mjs"]) {
    const result = spawnSync(process.execPath, [script], { cwd: root, stdio: "inherit" });
    if (result.error) throw result.error;
    if (result.status !== 0) throw new Error(`Contract generator failed: ${script}`);
  }
  const changed = changedGenerated(before, snapshotGenerated(root));
  if (changed.length) throw new Error(`Generated contracts were stale; review the regenerated files and rerun:\n${changed.join("\n")}`);
  const { failures } = checkOpenApiRouteDebt(root);
  if (failures.length) throw new Error(`OpenAPI route debt gate failed:\n${failures.join("\n")}`);
  console.log("Generated SDK and route coverage match their source contracts.");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) checkGeneratedContracts();
