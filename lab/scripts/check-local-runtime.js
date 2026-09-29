#!/usr/bin/env node
import { inspectLocalRuntime, loadLocalRuntimeManifest, resolveLocalRuntimePaths, sha256File } from "../src/runtime/localRuntime.js";

// 默认只检查存在性和大小；完整内容校验必须显式传入 --full-hash。
const fullHash = process.argv.includes("--full-hash");
const manifest = loadLocalRuntimeManifest();
const inspection = inspectLocalRuntime(manifest);
if (fullHash && inspection.model.exists) {
  inspection.model.sha256 = await sha256File(resolveLocalRuntimePaths(manifest).model_path);
  inspection.model.sha256_matches = inspection.model.sha256 === manifest.model.expected_sha256;
}
inspection.ready = inspection.manifest_validation.valid && inspection.server.exists && inspection.benchmark.exists &&
  inspection.model.size_matches && (!fullHash || inspection.model.sha256_matches);
console.log(JSON.stringify(inspection, null, 2));
if (!inspection.ready) process.exitCode = 1;
