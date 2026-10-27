import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import ts from "typescript";
import { createBreakingBaseline, generateTypes, readOpenApi, schemaToType } from "../lib/openapi-sdk.mjs";
import { findBreakingChanges } from "../check-openapi-breaking.mjs";

const spec = readOpenApi("services/api-gateway/openapi/edugrade-api.openapi.json");

function compile(t, types, fixture) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "sdk-remediation-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  fs.writeFileSync(path.join(directory, "types.ts"), types);
  const consumer = path.join(directory, "consumer.ts");
  fs.writeFileSync(consumer, fixture);
  // 编译实际生成类型与合法/非法消费者，不引入仓库环境类型；expect-error未命中也会导致失败。
  const program = ts.createProgram([consumer], { noEmit: true, strict: true, types: [], module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Bundler, target: ts.ScriptTarget.ES2022 });
  assert.deepEqual(ts.getPreEmitDiagnostics(program).map(item => ts.flattenDiagnosticMessageText(item.messageText, "\n")), []);
}

test("actual SDK retains nullable numbers, rubric evidence and both review draft branches", t => {
  compile(t, generateTypes(spec), `
import type { MathRubricDraftPoint, MathRubricDraftSuggestion, PaperImportRubricPoint, ReviewTaskContext, ReviewDraft } from './types';
declare const point: MathRubricDraftPoint;
declare const suggestion: MathRubricDraftSuggestion;
const pointScore: number | null = point.suggested_score;
const maximum: number | null = suggestion.max_score;
const missing: MathRubricDraftPoint['suggested_score'] = null;
const zero: MathRubricDraftSuggestion['max_score'] = 0;
// @ts-expect-error 可空数字不能接收字符串。
const wrongScore: MathRubricDraftPoint['suggested_score'] = '1';
// @ts-expect-error 可空数字不能接收对象。
const wrongMaximum: MathRubricDraftSuggestion['max_score'] = {};
const rubric: PaperImportRubricPoint = { id: 'p', description: '结论', score: 1, required: true,
  evidence_requirements: [{ type: 'final_result', target: 'x=1' }] };
const target: string | undefined = rubric.evidence_requirements?.[0].target;
// @ts-expect-error 合法证据要求的type枚举仍受约束。
const invalid: PaperImportRubricPoint = { id: 'p', description: '', score: 1, required: true, evidence_requirements: [{ type: 'unknown_rule' }] };
declare const saved: ReviewDraft;
const firstOpen: ReviewTaskContext['draft'] = null;
const reopened: ReviewTaskContext['draft'] = saved;
// @ts-expect-error 草稿不是任意标量。
const badDraft: ReviewTaskContext['draft'] = 'saved';
`);
});

test("type unions retain object and array constraints without applying them to null", t => {
  const schemas = {
    Item: { type: ["object", "null"], required: ["id"], properties: { id: { type: "number" } } },
    Items: { type: ["array", "null"], items: { type: ["integer", "number", "null"] } },
  };
  assert.equal(schemaToType({ type: ["number", "integer", "null"] }), "number | null");
  compile(t, generateTypes({ components: { schemas } }), `
import type { Item, Items } from './types';
const empty: Item = null;
const item: Item = { id: 1 };
const array: Items = [1, null];
const absent: Items = null;
// @ts-expect-error 对象分支仍要求id。
const missing: Item = {};
// @ts-expect-error 数组分支仍校验元素。
const invalid: Items = ['wrong'];
`);
});

test("3.1 authority no longer relies on nullable and preserves the explicit empty draft", () => {
  assert.equal(spec.openapi, "3.1.0");
  function visit(value) {
    if (!value || typeof value !== "object") return;
    assert.equal(Object.hasOwn(value, "nullable"), false);
    for (const child of Object.values(value)) visit(child);
  }
  visit(spec);
  assert.deepEqual(spec.components.schemas.ReviewTaskContext.properties.draft.anyOf,
    [{ $ref: "#/components/schemas/ReviewDraft" }, { type: "null" }]);
});

test("breaking gate rejects the first type or enum restriction on an existing field", () => {
  const before = createBreakingBaseline(spec);
  for (const mutate of [
    current => { current.components.schemas.AuthLoginRequest.properties.username.enum = ["only-one-name"]; },
    current => { current.components.schemas.PaperImportAnswerKeyInput.properties.standard_answer.type = "number"; },
  ]) {
    const after = structuredClone(spec);
    mutate(after);
    assert.ok(findBreakingChanges(before, createBreakingBaseline(after)).length > 0);
  }
  const addition = structuredClone(spec);
  addition.components.schemas.AuthLoginRequest.properties.optional_hint = { type: "string", enum: ["hint"] };
  assert.deepEqual(findBreakingChanges(before, createBreakingBaseline(addition)), []);
});

test("legacy nullable normalization permits equal declarations but rejects removed null or changed value types", () => {
  for (const [old, replacement] of [
    [{ type: "number", nullable: true }, { type: ["number", "null"] }],
    [{ type: "array", items: { type: "string" }, nullable: true }, { type: ["array", "null"], items: { type: "string" } }],
    [{ $ref: "#/components/schemas/Draft", nullable: true }, { anyOf: [{ $ref: "#/components/schemas/Draft" }, { type: "null" }] }],
  ]) {
    const baseline = { operations: {}, schemas: { Value: old } };
    assert.deepEqual(findBreakingChanges(baseline, { operations: {}, schemas: { Value: replacement } }), []);
    const withoutNull = replacement.anyOf ? { $ref: "#/components/schemas/Draft" } : { ...replacement, type: replacement.type[0] };
    assert.ok(findBreakingChanges(baseline, { operations: {}, schemas: { Value: withoutNull } }).length > 0);
    const changed = replacement.anyOf ? { anyOf: [{ $ref: "#/components/schemas/Other" }, { type: "null" }] } : { ...replacement, type: ["boolean", "null"] };
    assert.ok(findBreakingChanges(baseline, { operations: {}, schemas: { Value: changed } }).length > 0);
  }
});
