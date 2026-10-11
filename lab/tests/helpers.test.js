import assert from "node:assert/strict";
import test from "node:test";
import { baseInput, baseRubric } from "./helpers.js";

test("量规空值覆盖仍回退默认量规，显式测试字段覆盖保持有效", () => {
  for (const rubric of [null, undefined]) {
    const input = baseInput({ synthetic: true, rubric, request_id: "synthetic-null-rubric" });
    assert.deepEqual(input.rubric, baseRubric());
    assert.equal(input.max_score, input.rubric.max_score);
    assert.equal(input.rubric_version, input.rubric.rubric_version);
    assert.equal(input.request_id, "synthetic-null-rubric");
    assert.equal(Object.keys(input).indexOf("rubric"), Object.keys(input).indexOf("max_score") + 1);
  }
});

test("自定义量规被保留，默认满分和版本取自所选量规", () => {
  const rubric = baseRubric({ max_score: 5, rubric_version: "synthetic-custom-v1" });
  const input = baseInput({ synthetic: true, rubric });
  assert.equal(input.rubric, rubric);
  assert.equal(input.max_score, 5);
  assert.equal(input.rubric_version, "synthetic-custom-v1");
  // 无效输入测试仍可显式覆盖满分和版本，不能把夹具整理变成提前校验。
  const invalid = baseInput({ synthetic: true, rubric, max_score: -1, rubric_version: "synthetic-mismatch" });
  assert.equal(invalid.max_score, -1);
  assert.equal(invalid.rubric_version, "synthetic-mismatch");
});
