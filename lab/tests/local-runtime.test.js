import test from "node:test";
import assert from "node:assert/strict";
import {
  LAB_ROOT,
  inspectLocalRuntime,
  loadLocalRuntimeManifest,
  resolveLocalRuntimePaths,
  validateLocalRuntimeManifest
} from "../src/runtime/localRuntime.js";

function syntheticRuntimeManifest() {
  return {
    synthetic: true,
    schema_version: "local-runtime-v1",
    runtime: {
      name: "llama.cpp",
      release: "b1",
      url: "https://runtime.example.invalid/runtime.zip",
      source_url: "https://github.com/example/runtime/releases/download/b1/runtime.zip",
      expected_bytes: 1,
      expected_sha256: "0".repeat(64),
      install_dir: ".runtime/synthetic",
      server_binary: "server",
      benchmark_binary: "bench"
    },
    model: {
      model_path: ".models/synthetic.gguf",
      url: "https://models.example.invalid/model.gguf",
      expected_bytes: 1,
      expected_sha256: "1".repeat(64)
    },
    execution: { gpu_layers: 0, parallel_requests: 1, temperature: 0, context_tokens: 1024 }
  };
}

test("local runtime manifest pins a CPU-only 4B grading baseline", () => {
  const manifest = loadLocalRuntimeManifest();
  assert.deepEqual(validateLocalRuntimeManifest(manifest), { valid: true, errors: [] });
  assert.equal(manifest.model.file, "Qwen3-4B-Q4_K_M.gguf");
  assert.equal(manifest.model.quantization, "Q4_K_M");
  assert.match(manifest.runtime.source_url, /^https:\/\/github\.com\//);
  assert.match(manifest.runtime.expected_sha256, /^[a-f0-9]{64}$/);
  assert.equal(manifest.execution.gpu_layers, 0);
  assert.equal(manifest.execution.parallel_requests, 1);
  assert.equal(manifest.execution.context_tokens, 16384);
  assert.equal(manifest.execution.temperature, 0);
});

test("runtime and model paths remain under lab", () => {
  const paths = resolveLocalRuntimePaths();
  for (const path of Object.values(paths)) assert.ok(path.startsWith(LAB_ROOT));
});

test("runtime manifest rejects paths outside lab", () => {
  const manifest = loadLocalRuntimeManifest();
  manifest.model.model_path = "../outside.gguf";
  const result = validateLocalRuntimeManifest(manifest);
  assert.equal(result.valid, false);
  assert.ok(result.errors.some((error) => error.includes("model_path")));
  assert.throws(() => resolveLocalRuntimePaths(manifest), /Invalid local runtime manifest/);
  const inspection = inspectLocalRuntime(manifest);
  assert.equal(inspection.manifest_validation.valid, false);
  assert.equal(inspection.model.path, null);
  assert.equal(inspection.model.exists, false);
});

test("runtime validation keeps artifact and execution errors in order", () => {
  const manifest = syntheticRuntimeManifest();
  manifest.schema_version = "invalid";
  Object.assign(manifest.runtime, {
    name: "other-runtime", release: "latest", url: null,
    source_url: "https://github.com.evil.invalid/asset", expected_bytes: 0,
    expected_sha256: "invalid", install_dir: "../runtime"
  });
  Object.assign(manifest.model, {
    model_path: "../model.gguf", url: "http://models.example.invalid/model.gguf",
    expected_bytes: 0, expected_sha256: "invalid"
  });
  manifest.execution = { gpu_layers: 1, parallel_requests: 2, temperature: 1, context_tokens: 1023 };
  assert.deepEqual(validateLocalRuntimeManifest(manifest).errors, [
    "unsupported runtime manifest schema_version",
    "runtime.name must be llama.cpp",
    "runtime.release must be pinned",
    "runtime.url must use https",
    "runtime.source_url must identify the official GitHub asset",
    "runtime.expected_bytes must be positive",
    "runtime.expected_sha256 is required",
    "runtime.install_dir must stay inside lab",
    "model.model_path must stay inside lab",
    "model.url must use https",
    "model.expected_bytes must be positive",
    "model.expected_sha256 is required",
    "initial baseline must be CPU-only with gpu_layers=0",
    "initial baseline must use one request at a time",
    "grading baseline temperature must be zero",
    "runtime context must be between 1024 and 32768 tokens"
  ]);
  for (const input of [null, [], 1]) {
    assert.deepEqual(validateLocalRuntimeManifest(input), { valid: false, errors: ["runtime manifest must be an object"] });
  }
  for (const [contextTokens, valid] of [[1023, false], [1024, true], [32768, true], [32769, false]]) {
    const boundary = syntheticRuntimeManifest();
    boundary.execution.context_tokens = contextTokens;
    assert.equal(validateLocalRuntimeManifest(boundary).valid, valid);
  }
});

test("runtime URL prefixes preserve ToString and Symbol failure boundaries", () => {
  const fields = [
    ["runtime", "url", "https://", "runtime.url must use https"],
    ["runtime", "source_url", "https://github.com/", "runtime.source_url must identify the official GitHub asset"],
    ["model", "url", "https://", "model.url must use https"]
  ];
  for (const [section, field, prefix, error] of fields) {
    for (const value of [null, undefined, 0, false, {}, prefix.toUpperCase(), `before:${prefix}`, ` ${prefix}`]) {
      const manifest = syntheticRuntimeManifest();
      manifest[section][field] = value;
      assert.deepEqual(validateLocalRuntimeManifest(manifest).errors, [error]);
    }
    for (const value of [prefix, [prefix]]) {
      const manifest = syntheticRuntimeManifest();
      manifest[section][field] = value;
      assert.deepEqual(validateLocalRuntimeManifest(manifest), { valid: true, errors: [] });
    }
    const hints = [];
    const manifest = syntheticRuntimeManifest();
    manifest[section][field] = { synthetic: true, [Symbol.toPrimitive](hint) { hints.push(hint); return prefix; } };
    assert.deepEqual(validateLocalRuntimeManifest(manifest), { valid: true, errors: [] });
    assert.deepEqual(hints, ["string"]);
    // 原正则在 URL 转换失败时就抛错，不能提前读取后面的执行基线配置。
    manifest[section][field] = Symbol("synthetic-url");
    Object.defineProperty(manifest, "execution", { get() { throw new Error("execution must not be read"); } });
    assert.throws(() => validateLocalRuntimeManifest(manifest), TypeError);
  }
  const nearHost = syntheticRuntimeManifest();
  nearHost.runtime.source_url = "https://github.com.evil.invalid/asset";
  assert.deepEqual(validateLocalRuntimeManifest(nearHost).errors, ["runtime.source_url must identify the official GitHub asset"]);
  nearHost.runtime.source_url = "https://github.com";
  assert.deepEqual(validateLocalRuntimeManifest(nearHost).errors, ["runtime.source_url must identify the official GitHub asset"]);
});
