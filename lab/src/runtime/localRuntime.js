import { createHash } from "node:crypto";
import { createReadStream, existsSync, readFileSync, statSync } from "node:fs";
import { dirname, isAbsolute, join, normalize, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const LAB_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
export const DEFAULT_RUNTIME_MANIFEST = join(LAB_ROOT, "config", "local-runtime.json");

function isSafeLabRelativePath(path) {
  if (typeof path !== "string" || path.length === 0 || isAbsolute(path)) return false;
  const resolved = resolve(LAB_ROOT, path);
  const rel = relative(LAB_ROOT, resolved);
  return rel !== "" && !rel.startsWith("..") && !isAbsolute(rel);
}

export function loadLocalRuntimeManifest(path = DEFAULT_RUNTIME_MANIFEST) {
  return JSON.parse(readFileSync(path, "utf8"));
}

export function validateLocalRuntimeManifest(manifest) {
  const errors = [];
  if (!manifest || typeof manifest !== "object" || Array.isArray(manifest)) {
    return { valid: false, errors: ["runtime manifest must be an object"] };
  }
  if (manifest.schema_version !== "local-runtime-v1") errors.push("unsupported runtime manifest schema_version");
  if (manifest.runtime?.name !== "llama.cpp") errors.push("runtime.name must be llama.cpp");
  if (!/^b\d+$/.test(manifest.runtime?.release ?? "")) errors.push("runtime.release must be pinned");
  if (!/^https:\/\//.test(manifest.runtime?.url ?? "")) errors.push("runtime.url must use https");
  if (!/^https:\/\/github\.com\//.test(manifest.runtime?.source_url ?? "")) {
    errors.push("runtime.source_url must identify the official GitHub asset");
  }
  if (!Number.isInteger(manifest.runtime?.expected_bytes) || manifest.runtime.expected_bytes <= 0) {
    errors.push("runtime.expected_bytes must be positive");
  }
  if (!/^[a-f0-9]{64}$/.test(manifest.runtime?.expected_sha256 ?? "")) errors.push("runtime.expected_sha256 is required");
  if (!isSafeLabRelativePath(manifest.runtime?.install_dir)) errors.push("runtime.install_dir must stay inside lab");
  if (!isSafeLabRelativePath(manifest.model?.model_path)) errors.push("model.model_path must stay inside lab");
  if (!/^https:\/\//.test(manifest.model?.url ?? "")) errors.push("model.url must use https");
  if (!Number.isInteger(manifest.model?.expected_bytes) || manifest.model.expected_bytes <= 0) {
    errors.push("model.expected_bytes must be positive");
  }
  if (!/^[a-f0-9]{64}$/.test(manifest.model?.expected_sha256 ?? "")) errors.push("model.expected_sha256 is required");
  if (manifest.execution?.gpu_layers !== 0) errors.push("initial baseline must be CPU-only with gpu_layers=0");
  if (manifest.execution?.parallel_requests !== 1) errors.push("initial baseline must use one request at a time");
  if (manifest.execution?.temperature !== 0) errors.push("grading baseline temperature must be zero");
  if (
    !Number.isInteger(manifest.execution?.context_tokens)
    || manifest.execution.context_tokens < 1024
    || manifest.execution.context_tokens > 32768
  ) {
    errors.push("runtime context must be between 1024 and 32768 tokens");
  }
  return { valid: errors.length === 0, errors };
}

export function resolveLocalRuntimePaths(manifest = loadLocalRuntimeManifest()) {
  const validation = validateLocalRuntimeManifest(manifest);
  if (!validation.valid) throw new Error(`Invalid local runtime manifest: ${validation.errors.join("; ")}`);
  const runtimeDir = resolve(LAB_ROOT, normalize(manifest.runtime.install_dir));
  return {
    runtime_dir: runtimeDir,
    server_binary: join(runtimeDir, manifest.runtime.server_binary),
    benchmark_binary: join(runtimeDir, manifest.runtime.benchmark_binary),
    model_path: resolve(LAB_ROOT, normalize(manifest.model.model_path))
  };
}

// 快速检查只看清单、存在性和大小；需要完整性证据时另外调用 sha256File。
export function inspectLocalRuntime(manifest = loadLocalRuntimeManifest()) {
  const manifestValidation = validateLocalRuntimeManifest(manifest);
  if (!manifestValidation.valid) {
    const unavailable = (expectedBytes = null) => ({
      path: null,
      exists: false,
      bytes: null,
      expected_bytes: expectedBytes,
      size_matches: expectedBytes === null ? null : false
    });
    return {
      manifest_validation: manifestValidation,
      server: unavailable(),
      benchmark: unavailable(),
      model: unavailable(Number.isInteger(manifest?.model?.expected_bytes) ? manifest.model.expected_bytes : null)
    };
  }
  const paths = resolveLocalRuntimePaths(manifest);
  const inspect = (path, expectedBytes) => ({
    path,
    exists: existsSync(path),
    bytes: existsSync(path) ? statSync(path).size : null,
    expected_bytes: expectedBytes ?? null,
    size_matches: expectedBytes === undefined ? null : existsSync(path) && statSync(path).size === expectedBytes
  });
  return {
    manifest_validation: manifestValidation,
    server: inspect(paths.server_binary, undefined),
    benchmark: inspect(paths.benchmark_binary, undefined),
    model: inspect(paths.model_path, manifest.model.expected_bytes)
  };
}

export function sha256File(path) {
  return new Promise((resolveHash, reject) => {
    const hash = createHash("sha256");
    createReadStream(path)
      .on("error", reject)
      .on("data", (chunk) => hash.update(chunk))
      .on("end", () => resolveHash(hash.digest("hex")));
  });
}
