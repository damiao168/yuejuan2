import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const contractRoot = join(root, "contracts", "grading-agent", "v1");
const load = (path) => JSON.parse(readFileSync(join(contractRoot, path), "utf8"));
const contractV2Root = join(root, "contracts", "grading-agent", "v2");
const loadV2 = (path) => JSON.parse(readFileSync(join(contractV2Root, path), "utf8"));

const contract = load("contract.json");
const matrix = load("capability-matrix.json");
const request = load("fixtures/valid-request.json");
const response = load("fixtures/valid-response.json");
const invalid = load("fixtures/invalid-response-evidence-mismatch.json");

assert.equal(contract.deployment_mode, "shadow");
assert.equal(contract.final_grade_publication_allowed, false);
assert.equal(contract.student_visible, false);
assert.equal(contract.human_review_required, true);
assert.equal(matrix.final_grade_publication_allowed, false);
assert.ok(matrix.capabilities.every((item) => item.review_policy === "always"));
assert.ok(matrix.capabilities.filter((item) => ["essay", "discussion"].includes(item.question_type)).every((item) => item.delivery === "shadow_only"));

for (const field of contract.forbidden_request_fields) {
  assert.equal(Object.hasOwn(request, field), false, `request leaks forbidden field ${field}`);
}
assert.equal(request.schema_version, "grading-agent-v1");
assert.equal(request.grade_level, "junior");
assert.deepEqual(contract.allowed_grade_levels, ["junior", "senior"]);
assert.deepEqual(contract.allowed_subjects, [
  "chinese", "mathematics", "english", "physics", "chemistry", "biology", "history", "geography", "ethics_politics",
]);
assert.deepEqual(matrix.grade_levels, ["junior", "senior"]);
assert.deepEqual(contract.allowed_agent_roles, ["single", "primary", "arbiter"]);
assert.equal(request.agent_role, "single");
assert.equal(request.model_policy.mode, "shadow");
assert.equal(request.prompt_guard.student_answer_is_untrusted, true);
assert.equal(request.rubric_version, request.rubric.rubric_version);
assert.equal(request.max_score, request.rubric.max_score);
assert.equal(request.rubric.points.reduce((sum, point) => sum + point.score, 0), request.max_score);

assert.equal(response.request_id, request.request_id);
assert.equal(response.status, "suggestion");
assert.equal(response.needs_human_review, true);
assert.equal(response.mock, false);
assert.equal(response.suggested_score, response.matched_points.reduce((sum, point) => sum + point.score, 0));
assert.equal(response.rubric_version, request.rubric_version);
assert.equal(response.prompt_version, request.prompt_version);
assert.ok(response.risk_flags.every((flag) => contract.risk_flags.includes(flag)));

const evidence = new Map(response.evidence.map((item) => [item.evidence_id, item]));
for (const point of response.matched_points) {
  for (const id of point.evidence_ids) {
    assert.equal(evidence.get(id)?.rubric_point_id, point.rubric_point_id);
  }
}
assert.ok(invalid.matched_points.some((point) => point.evidence_ids.some((id) => !invalid.evidence.some((item) => item.evidence_id === id))));

const contractV2 = loadV2("contract.json");
const requestSchemaV2 = loadV2("request.schema.json");
const responseSchemaV2 = loadV2("response.schema.json");
const errorSchemaV2 = loadV2("error.schema.json");
const requestV2 = loadV2("fixtures/valid-request.json");
const responseV2 = loadV2("fixtures/valid-response.json");
const invalidURLV2 = loadV2("fixtures/invalid-request-remote-url.json");
const invalidWholePageV2 = loadV2("fixtures/invalid-request-whole-page.json");
const invalidResponseV2 = loadV2("fixtures/invalid-response-unknown-evidence-id.json");
const errorV2 = loadV2("fixtures/valid-error.json");

assert.equal(contractV2.deployment_mode, "unreachable_fixture_only");
assert.equal(contractV2.http_route_enabled, false);
assert.equal(contractV2.external_provider_enabled, false);
assert.equal(contractV2.final_grade_publication_allowed, false);
assert.equal(contractV2.human_review_required, true);
assert.equal(requestSchemaV2.properties.schema_version.const, "grading-agent-v2");
assert.equal(responseSchemaV2.properties.schema_version.const, "grading-agent-v2");
assert.equal(errorSchemaV2.properties.schema_version.const, "grading-agent-v2");
assert.equal(request.schema_version, "grading-agent-v1", "v1 fixture must remain unchanged");

for (const field of contractV2.forbidden_request_fields) {
  assert.equal(Object.hasOwn(requestV2, field), false, `v2 request leaks forbidden field ${field}`);
}
assert.equal(requestV2.media_evidence.kind, "answer_segment_crop");
assert.equal(requestV2.subject, "mathematics");
assert.equal(requestV2.grade_level, "senior");
assert.equal(requestV2.agent_role, "primary");
assert.deepEqual(contractV2.allowed_agent_roles, ["single", "primary", "arbiter"]);
assert.equal(requestV2.media_evidence.encoding, "base64");
assert.equal(requestV2.media_evidence.media_type, "image/png");
const decodedCrop = Buffer.from(requestV2.media_evidence.data_base64, "base64");
assert.equal(decodedCrop.byteLength, requestV2.media_evidence.byte_size);
assert.equal(decodedCrop.subarray(0, 8).toString("hex"), "89504e470d0a1a0a");
assert.equal(decodedCrop.readUInt32BE(16), requestV2.media_evidence.width_pixels);
assert.equal(decodedCrop.readUInt32BE(20), requestV2.media_evidence.height_pixels);
assert.equal(createHash("sha256").update(decodedCrop).digest("hex"), requestV2.media_evidence.sha256);

const bbox = requestV2.media_evidence.normalized_bbox;
const bindingParts = [
  requestV2.schema_version,
  requestV2.request_id,
  requestV2.question_id,
  requestV2.answer_segment_id,
  requestV2.media_evidence.sha256,
  ...[bbox.x, bbox.y, bbox.width, bbox.height].map((value) => String(Math.round(value * 1_000_000))),
];
const bindingMaterial = bindingParts.map((part) => `${Buffer.byteLength(part, "utf8")}:${part}`).join("");
assert.equal(createHash("sha256").update(bindingMaterial).digest("hex"), requestV2.media_evidence.binding_hash);
assert.ok(bbox.width * bbox.height < contractV2.maximum_source_bbox_area);
assert.notEqual(invalidURLV2.media_evidence.encoding, "base64");
assert.match(invalidURLV2.media_evidence.data_base64, /^https:/);
assert.ok(
  invalidWholePageV2.media_evidence.normalized_bbox.width *
    invalidWholePageV2.media_evidence.normalized_bbox.height >=
    contractV2.maximum_source_bbox_area,
);

assert.equal(responseV2.request_id, requestV2.request_id);
assert.equal(responseV2.needs_human_review, true);
assert.equal(responseV2.mock, false);
const knownEvidenceIds = new Set([
  ...requestV2.math_evidence.steps.map((item) => item.id),
  ...requestV2.math_evidence.formulas.map((item) => item.id),
]);
const rubricPointIds = new Set(requestV2.rubric.points.map((item) => item.id));
for (const candidate of responseV2.criterion_candidates) {
  assert.ok(rubricPointIds.has(candidate.rubric_point_id), `unknown rubric point: ${candidate.rubric_point_id}`);
  assert.ok(
    candidate.evidence_ids.every((id) => knownEvidenceIds.has(id)),
    `candidate references unknown math evidence: ${candidate.evidence_ids.join(", ")}`,
  );
}
assert.ok(
  invalidResponseV2.criterion_candidates.some((candidate) =>
    candidate.evidence_ids.some((id) => !knownEvidenceIds.has(id)),
  ),
  "invalid fixture must reference unknown math evidence",
);
assert.equal(errorV2.schema_version, "grading-agent-v2");
assert.equal(errorV2.error.retryable, false);

console.log("STORY-057 grading-agent contract invariants passed");
