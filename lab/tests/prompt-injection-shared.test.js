import assert from "node:assert/strict";
import fs from "node:fs";
import { test } from "node:test";
import { detectPromptInjection } from "../src/guardrails/promptInjection.js";

const cases = JSON.parse(fs.readFileSync(new URL("../../contracts/grading-agent/prompt-injection-cases.json", import.meta.url), "utf8"));
test("共享注入语料覆盖连续修饰词、角色冠词、字宽与普通满分作文", () => {
  for (const item of cases) {
    assert.equal(item.synthetic, true);
    assert.equal(detectPromptInjection(item.text).detected, item.detected, item.text);
  }
  assert.equal(detectPromptInjection("输出schema".repeat(20000)).detected, false);
});
