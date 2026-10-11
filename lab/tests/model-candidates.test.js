import test from "node:test";
import assert from "node:assert/strict";
import { getModelCandidate, loadModelCandidates, validateModelCandidates } from "../src/runtime/modelCandidates.js";

function syntheticCandidate(overrides = {}) {
  return {
    synthetic: true,
    candidate_id: "synthetic-model",
    url: "https://models.example.invalid/model.gguf",
    expected_bytes: 1,
    expected_sha256: "0".repeat(64),
    model_path: ".models/synthetic.gguf",
    ...overrides
  };
}

test("model candidate registry pins 4B and 8B artifacts", () => {
  const registry = loadModelCandidates();
  assert.deepEqual(validateModelCandidates(registry), { valid: true, errors: [] });
  assert.equal(getModelCandidate("qwen3_4b", registry).expected_bytes, 2497280256);
  assert.equal(getModelCandidate("qwen3_8b", registry).expected_sha256, "d98cdcbd03e17ce47681435b5150e34c1417f50b5c0019dd560e4882c5745785");
});

test("model candidate paths cannot escape lab", () => {
  const registry = loadModelCandidates();
  registry.candidates[0].model_path = "../outside.gguf";
  const result = validateModelCandidates(registry);
  assert.equal(result.valid, false);
  assert.ok(result.errors.some((error) => error.includes("model_path")));
});

test("malformed model candidate returns validation errors instead of throwing", () => {
  const registry = loadModelCandidates();
  registry.candidates[0] = null;
  const result = validateModelCandidates(registry);
  assert.equal(result.valid, false);
  assert.match(result.errors.join("\n"), /must be an object/);
});

test("candidate validation skips malformed items and preserves accumulated field errors", () => {
  const registry = {
    synthetic: true,
    schema_version: "invalid",
    candidates: [
      null, false, [],
      syntheticCandidate({ candidate_id: undefined, url: null, expected_bytes: 0, expected_sha256: "invalid", model_path: "../outside.gguf" }),
      syntheticCandidate({ candidate_id: undefined })
    ]
  };
  assert.deepEqual(validateModelCandidates(registry).errors, [
    "unsupported model candidate schema_version",
    "candidates[0] must be an object",
    "candidates[1] must be an object",
    "candidates[2] must be an object",
    "candidates[3].candidate_id is required",
    "candidates[3].url must use https",
    "candidates[3].expected_bytes must be positive",
    "candidates[3].expected_sha256 is required",
    "candidates[3].model_path must stay inside lab",
    "candidates[4].candidate_id is required",
    "duplicate candidate_id: undefined"
  ]);
  assert.deepEqual(validateModelCandidates({ synthetic: true, schema_version: "invalid", candidates: [] }).errors, [
    "unsupported model candidate schema_version", "candidates must be non-empty"
  ]);
});

test("candidate URL prefixes preserve ToString and Symbol failure boundaries", () => {
  const validate = (url) => validateModelCandidates({
    synthetic: true, schema_version: "model-candidates-v1", candidates: [syntheticCandidate({ url })]
  });
  for (const url of [null, undefined, 0, false, {}, "HTTPS://", "before:https://", " https://"]) {
    assert.deepEqual(validate(url).errors, ["candidates[0].url must use https"]);
  }
  for (const url of ["https://", ["https://"]]) {
    assert.deepEqual(validate(url), { valid: true, errors: [] });
  }
  const hints = [];
  assert.deepEqual(validate({ synthetic: true, [Symbol.toPrimitive](hint) { hints.push(hint); return "https://"; } }), {
    valid: true, errors: []
  });
  assert.deepEqual(hints, ["string"]);
  assert.throws(() => validate(Symbol("synthetic-url")), TypeError);
  const candidate = syntheticCandidate({ url: Symbol("synthetic-url") });
  Object.defineProperty(candidate, "expected_bytes", { get() { throw new Error("artifact size must not be read"); } });
  assert.throws(() => validateModelCandidates({
    synthetic: true, schema_version: "model-candidates-v1", candidates: [candidate]
  }), TypeError);
});
