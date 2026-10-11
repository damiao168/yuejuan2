import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const root = process.cwd();
const failures = [];

function assert(condition, message) {
  if (!condition) {
    failures.push(message);
  }
}

function read(path) {
  return readFileSync(join(root, path), "utf8");
}

assert(existsSync(join(root, "services/ocr-worker/ocr_worker/runner.py")), "services/ocr-worker must provide a runnable OCR worker.");
assert(existsSync(join(root, "services/ocr-worker/ocr_worker/engine.py")), "services/ocr-worker must provide an OCR engine adapter module.");
assert(existsSync(join(root, "services/ocr-worker/ocr_worker/api.py")), "services/ocr-worker must provide an API client module.");
assert(existsSync(join(root, "docs/evaluation/ocr-sample-set.md")), "OCR sample-set evaluation document is required.");
assert(existsSync(join(root, "docs/evaluation/ocr-metrics.md")), "OCR metrics document is required.");

const compose = read("infra/docker-compose/docker-compose.yml");
assert(/ocr-worker:/.test(compose), "Docker Compose must define an optional ocr-worker service.");
assert(/profiles:\s*\[\s*["']ocr["']\s*\]/.test(compose), "ocr-worker service must be behind an ocr profile.");

const systemInfo = read("services/api-gateway/internal/handlers/handlers.go");
const capabilities = /"capabilities": \[]string\{([\s\S]*?)\}/.exec(systemInfo)?.[1] ?? "";
const notImplemented = /"not_implemented": \[]string\{([\s\S]*?)\}/.exec(systemInfo)?.[1] ?? "";
assert(capabilities.includes('"ocr_engine_inference"'), "system/info must explicitly declare OCR inference capability.");
assert(!notImplemented.includes('"ocr_engine_inference"'), "ocr_engine_inference must not remain in not_implemented.");

if (existsSync(join(root, "services/ocr-worker/ocr_worker/runner.py"))) {
  const runner = read("services/ocr-worker/ocr_worker/runner.py");
  assert(!/Synthetic OCR text|mock_ocr|hard-coded OCR/i.test(runner), "OCR worker must not contain hard-coded OCR output.");
}

if (failures.length > 0) {
  console.error("STORY-049 OCR worker checks failed:");
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log("STORY-049 OCR worker checks passed.");
