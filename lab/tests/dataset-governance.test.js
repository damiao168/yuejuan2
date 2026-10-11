import test from "node:test";
import assert from "node:assert/strict";
import {
  auditSplitLeakage,
  getApprovedDatasetSource,
  loadDatasetSourceRegistry,
  questionGroupKey,
  scanSensitiveText,
  splitByQuestionGroup,
  validateDatasetSourceRegistry,
  validateGovernedSamples
} from "../src/datasets/governance.js";

function sample(id, questionId, questionText = "Why?") {
  return {
    sample_id: id,
    synthetic: true,
    question_id: questionId,
    question_text: questionText,
    answer_text: "Because evidence supports it.",
    human_rationale: "Synthetic rationale.",
    rubric: { rubric_version: "rubric-v1" }
  };
}

test("source registry allows only reviewed uses", () => {
  const registry = loadDatasetSourceRegistry();
  assert.deepEqual(validateDatasetSourceRegistry(registry), { valid: true, errors: [] });
  assert.equal(getApprovedDatasetSource("lab_synthetic", "evaluation", registry).source_type, "synthetic");
  assert.throws(() => getApprovedDatasetSource("semeval_2013_task_7", "evaluation", registry), /not license-approved/);
});

test("malformed source registry entry returns validation errors instead of throwing", () => {
  const registry = loadDatasetSourceRegistry();
  registry.sources[0] = null;
  const result = validateDatasetSourceRegistry(registry);
  assert.equal(result.valid, false);
  assert.match(result.errors.join("\n"), /must be an object/);
});

test("source registry accumulates malformed entries and field errors in order", () => {
  const result = validateDatasetSourceRegistry({
    schema_version: "unsupported",
    sources: [
      null,
      { source_id: "same", homepage: "http://example.invalid", license_status: "approved", allowed_uses: "evaluation", evidence_scope: "unreviewed" },
      { source_id: "same", homepage: null, license_status: "unsupported", allowed_uses: ["evaluation"] },
      { homepage: "https://example.invalid", license_status: "review_required", allowed_uses: ["evaluation"] }
    ]
  });
  assert.deepEqual(result, {
    valid: false,
    errors: [
      "unsupported source registry schema_version",
      "sources[0] must be an object",
      "sources[1].homepage must be https or lab-local",
      "sources[1].allowed_uses must be an array",
      "sources[1].evidence_scope is required for approved sources",
      "duplicate source_id: same",
      "sources[2].homepage must be https or lab-local",
      "sources[2].license_status is unsupported",
      "sources[2] cannot allow use before license approval",
      "sources[3].source_id is required",
      "sources[3] cannot allow use before license approval"
    ]
  });
});

test("source homepage keeps case-sensitive https and lab-local prefixes", () => {
  for (const [homepage, valid] of [
    ["https://example.invalid", true], ["lab/fixtures", true],
    ["HTTPS://example.invalid", false], ["https:/example.invalid", false],
    ["lab", false], [null, false], [42, false]
  ]) {
    const result = validateDatasetSourceRegistry({
      schema_version: "dataset-source-registry-v1",
      sources: [{ source_id: "fixture", homepage, license_status: "review_required", allowed_uses: [] }]
    });
    assert.equal(result.valid, valid, String(homepage));
  }
});

test("privacy scanner detects common direct identifiers", () => {
  assert.deepEqual(scanSensitiveText("student email is learner@example.com"), ["email"]);
  assert.ok(scanSensitiveText("手机号：13812345678").includes("cn_mobile"));
  assert.ok(scanSensitiveText("姓名：张三").includes("name_label"));
  assert.ok(scanSensitiveText("Name: Alice Smith\n").includes("name_label"));
  assert.equal(scanSensitiveText("They should have a descriptive name: it explains the test.").includes("name_label"), false);
  assert.equal(scanSensitiveText("* Name: the JSON field naming convention.").includes("name_label"), false);
});

test("privacy labels retain optional colons and minimum value lengths", () => {
  for (const [text, detector, detected] of [
    ["学号 AB-1", "student_number_label", true],
    ["student\tID： abc9", "student_number_label", true],
    ["STUDENT \n NUMBER : ----", "student_number_label", true],
    ["student id: !!; 学号: ABCD", "student_number_label", true],
    ["student id: A12", "student_number_label", false],
    ["学号： A_B1", "student_number_label", false],
    [`student id${" ".repeat(256)}!`, "student_number_label", false],
    ["phone: +12- 34", "phone_label", true],
    ["电话 1234567", "phone_label", true],
    ["MOBILE： +1\t2-34", "phone_label", true],
    ["phone: +      ", "phone_label", true],
    ["phone: !!; mobile: 1234567", "phone_label", true],
    ["phone:123456", "phone_label", false],
    ["mobile: x1234567", "phone_label", false],
    [`phone${" ".repeat(256)}!`, "phone_label", false]
  ]) {
    assert.equal(scanSensitiveText(text).includes(detector), detected, text);
  }
});

test("privacy names retain field boundaries, unicode letters and word limits", () => {
  // 多行模式会把姓名后的换行视为结束；下一行的普通说明不能取消已发现的姓名。
  for (const [text, detected] of [
    ["姓名：张·三", true], ["学生姓名：\u{20000}\u{20001}", true],
    ["姓名：一二三四五六七八九", true],
    ["{ name: alice smith; }", true], [", \tNAME: Alice-Marie O'Neil }", true],
    ["Name:\nAlice\nordinary explanation.", true],
    ["Name: Alice Bob Carol David,", true], ["\u2028Name: \u212Aelly \u017Fmith\u2029", true],
    ["prefix name: Alice", false], ["* Name: Alice", false], ["; Name: Alice", false],
    ["Name：Alice", false], ["Name: Alice Bob Carol David Eve", false],
    ["Name: \u00C9lodie", false], ["Name: Alice2", false], ["Name: Alice.", false],
    ["姓名：张", false], ["姓名 张三", false], [`${" \n".repeat(128)}Name: !`, false]
  ]) {
    assert.equal(scanSensitiveText(text).includes("name_label"), detected, text);
  }
});

test("governance rejects duplicate ids and sensitive text", () => {
  const source = getApprovedDatasetSource("lab_synthetic", "evaluation");
  const samples = [sample("same", "q1"), sample("same", "q2", "姓名：张三")];
  const result = validateGovernedSamples(samples, source);
  assert.equal(result.valid, false);
  assert.ok(result.errors.some((error) => error.includes("duplicate sample_id")));
  assert.ok(result.errors.some((error) => error.includes("name_label")));
});

test("grouped split never leaks a question and rubric across splits", () => {
  const samples = [];
  for (let question = 0; question < 12; question += 1) {
    samples.push(sample(`s-${question}-a`, `q-${question}`), sample(`s-${question}-b`, `q-${question}`));
  }
  const result = splitByQuestionGroup(samples, { seed: "fixed" });
  assert.equal(result.group_count, 12);
  assert.deepEqual(auditSplitLeakage(result.splits), { passed: true, leaked_groups: [], unique_groups: 12 });
  assert.ok(result.splits.train.length > 0);
  assert.ok(result.splits.validation.length > 0);
  assert.ok(result.splits.test.length > 0);
});

test("leakage audit preserves UTF-16 string ordering for reported group keys", () => {
  const records = ["q-z", "\uFF3A", "q-2", "\u{21551}", "q-Z", "q-10"].map((questionId, index) => sample(`s-${index}`, questionId));
  assert.deepEqual(auditSplitLeakage({ train: records, test: [...records].reverse() }), {
    passed: false,
    leaked_groups: ["q-10::rubric-v1", "q-2::rubric-v1", "q-Z::rubric-v1", "q-z::rubric-v1", "\u{21551}::rubric-v1", "\uFF3A::rubric-v1"],
    unique_groups: records.length
  });
});

test("grouped split is deterministic", () => {
  const samples = Array.from({ length: 10 }, (_, index) => sample(`s-${index}`, `q-${index}`));
  const first = splitByQuestionGroup(samples, { seed: "fixed" });
  const second = splitByQuestionGroup([...samples].reverse(), { seed: "fixed" });
  for (const name of ["train", "validation", "test"]) {
    assert.deepEqual(first.splits[name].map(questionGroupKey).sort(), second.splits[name].map(questionGroupKey).sort());
  }
});

test("grouped split rejects negative or non-finite ratios", () => {
  const samples = [sample("s-1", "q-1")];
  assert.throws(
    () => splitByQuestionGroup(samples, { ratios: { train: 2, validation: -0.5, test: -0.5 } }),
    /between 0 and 1/
  );
  assert.throws(
    () => splitByQuestionGroup(samples, { ratios: { train: Number.NaN, validation: 0.5, test: 0.5 } }),
    /between 0 and 1/
  );
});

test("real samples require explicit question ids and anonymized status", () => {
  const source = { source_id: "real", source_type: "human" };
  const real = { ...sample("r1", undefined), synthetic: false };
  const result = validateGovernedSamples([real], source);
  assert.equal(result.valid, false);
  assert.ok(result.errors.some((error) => error.includes("privacy_status")));
  assert.ok(result.errors.some((error) => error.includes("question_id")));
});
