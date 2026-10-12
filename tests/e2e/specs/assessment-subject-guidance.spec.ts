import { expect, test, type Page } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";
import { openAssessmentConfiguration } from "../fixtures/assessmentNavigation";

test.setTimeout(90_000);

type ConfigurationWrite = {
  subject_profile_id: string;
  allowed_evidence_types: string[];
  scoring_policy: { mode: string; require_evidence: boolean };
  expected_revision: number;
};

async function openAssessment(page: Page, subject = "math", questionType = "short_answer", frozen = false, openDrawer = true) {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  const examID = "exam-created-math";
  const examPath = `/api/v1/exams/${examID}`;
  const numeric = questionType === "formula";
  const objective = questionType === "single_choice";
  const extended = ["essay", "discussion", "coding"].includes(questionType);
  const calculation = questionType === "calculation";
  const archetype = numeric ? "numeric_expression" : objective ? "selected_response" : extended ? "extended_response" : calculation ? "structured_steps" : "short_constructed";
  const evidence = numeric ? ["numeric_value", "math_expression", "unit_value"] : objective ? ["selected_option"] : calculation ? ["text_span", "math_expression", "math_step", "unit_value"] : ["text_span", "concept", "relation"];
  const profiles = [
    { id: "math-template", code: "junior.mathematics.standard", education_stage: "junior", subject_code: "mathematics", version: 1, status: "active", parser_policy: {}, evidence_policy: { allowed_types: ["text_span", "selected_option", "numeric_value", "math_expression", "math_step", "unit_value"] }, scoring_default: { mode: "AI_ASSIST", require_evidence: true } },
    { id: "english-template", code: "junior.english.standard", education_stage: "junior", subject_code: "english", version: 1, status: "active", parser_policy: {}, evidence_policy: { allowed_types: ["text_span", "concept", "relation"] }, scoring_default: { mode: "AI_ASSIST", require_evidence: true } }
  ];
  const exam = { id: examID, tenant_id: "tenant-school", school_id: "school-1", name: "学科与评分提示回归", subject, exam_type: "midterm", total_score: 10, status: "draft", grading_mode: "ai_assisted", appeal_enabled: true, publish_policy: "manual", created_by: "user-school_admin", class_ids: ["class-1"], revision: 1 };
  const question = { id: "question-scope", exam_id: examID, question_no: "1", question_type: questionType, stem: "请根据题意作答。", score: 10, page_no: 1, status: "active", revision: 1, knowledge_points: [], options: ["A", "B"], answer_key: { standard_answer: "A" } };
  const snapshot = { id: "snapshot-scope", tenant_id: exam.tenant_id, exam_id: examID, question_id: question.id, snapshot_version: 1, subject_profile_id: "english-template", subject_profile_code: "junior.english.standard", subject_profile_version: 1, education_stage: "junior", subject_code: "english", archetype_code: archetype, allowed_evidence_types: ["text_span"], risk_tier: "R2", profile_snapshot: {}, archetype_snapshot: {}, rubric_snapshot: {}, scoring_policy_snapshot: { mode: "HUMAN_PRIMARY" }, content_hash: "frozen-scope-hash", created_at: "2026-10-06T00:00:00Z" };
  const writes: ConfigurationWrite[] = [];
  let saved: Record<string, unknown> | undefined;
  // 界面和 Ant Design 使用真实代码；API 夹具按本场考试和所选模板同时检查学科与证据，不能模拟非法保存成功。
  await page.route(/\/api\/v1(?:\/|$)/, async route => {
    const path = new URL(route.request().url()).pathname;
    const json = (body: unknown, status = 200) => route.fulfill({ status, json: body });
    if (path === examPath) return json({ exam });
    if (path === `${examPath}/questions`) return json({ questions: [question] });
    if (path === `${examPath}/papers`) return json({ papers: [] });
    if (path === `${examPath}/paper-imports`) return json({ imports: [] });
    if (path === "/api/v1/questions/question-scope/scoring-rules") return json({ scoring_rules: [] });
    if (path === "/api/v1/assessment/subject-profiles") return json({ subject_profiles: profiles });
    if (path === "/api/v1/assessment/question-archetypes") return json({ question_archetypes: [{ code: archetype, response_schema: {}, evidence_types: evidence, default_scoring_mode: numeric || objective ? "RULE_AUTO" : extended ? "HUMAN_PRIMARY" : "AI_ASSIST" }] });
    if (path === `${examPath}/questions/question-scope/assessment-snapshot`) return frozen ? json({ assessment_snapshot: snapshot }) : json({ error: { code: "assessment_not_found" } }, 404);
    if (path === `${examPath}/questions/question-scope/assessment-profile`) {
      if (route.request().method() === "PUT") {
        const body = route.request().postDataJSON() as ConfigurationWrite;
        writes.push(body);
        const selected = profiles.find(profile => profile.id === body.subject_profile_id);
        if (!selected || (subject === "math" && selected.subject_code !== "mathematics") || (body.scoring_policy.require_evidence && !body.allowed_evidence_types.length) || body.allowed_evidence_types.some(value => !selected.evidence_policy.allowed_types.includes(value) || !evidence.includes(value))) return json({ error: { code: "invalid_assessment_configuration" } }, 400);
        saved = { ...body, education_stage: selected.education_stage, subject_code: selected.subject_code, archetype_code: archetype, risk_tier: "R2", exam_id: examID, question_id: question.id, revision: 1 };
      }
      return saved ? json({ assessment_profile: saved }) : json({ error: { code: "assessment_not_found" } }, 404);
    }
    if (path === `${examPath}/readiness`) return json({ readiness: { ready: true, confirmed: false, configuration_hash: "scope-hash", import_snapshot_available: false, checks: [], advisories: [] } });
    if (path === `${examPath}/candidates/refresh`) return json({ candidate_refresh: {} });
    if (path === `${examPath}/roster`) return json({ roster: { summary: { expected: 1, received: 0, graded: 0, absent: 0, unresolved: 0, missing_pages: 0, unidentified: 0 }, entries: [{ key: "student-1", student_id: "student-1", student_name: "学生甲", student_no: "1001", class_name: "一班" }] } });
    return route.fallback();
  });
  await page.goto(`/#/admin/exams/${examID}/settings`, { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "核对并冻结考试准备", exact: true }).click();
  await page.getByRole("button", { name: "前往题目配置", exact: true }).click();
  await page.getByRole("tab", { name: "评分配置", exact: true }).click();
  if (!openDrawer) return writes;
  await openAssessmentConfiguration(page, frozen);
  if (frozen) await expect(page.getByText("考试配置已冻结", { exact: true })).toBeVisible();
  else await expect(page.getByRole("button", { name: "保存配置", exact: true })).toBeVisible();
  return writes;
}

test("数学考试学科锁定，英语模板不会成为可提交配置", async ({ page }) => {
  const writes = await openAssessment(page);
  await expect(page.getByLabel("学科", { exact: true })).toBeDisabled();
  await expect(page.getByText("junior.mathematics.standard · v1", { exact: true })).toBeVisible();
  const save = page.getByRole("button", { name: "保存配置", exact: true });
  await expect(save).toBeEnabled();
  await save.click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0].subject_profile_id).toBe("math-template");
  await expect(page.getByText("学科评分配置已保存", { exact: true })).toBeVisible();
});

test("综合考试仍可显式选择英语模板并保存合法交集", async ({ page }) => {
  const writes = await openAssessment(page, "综合");
  const subject = page.getByLabel("学科", { exact: true });
  await expect(subject).toBeEnabled();
  await subject.click();
  await page.getByTitle("英语", { exact: true }).click();
  await expect(page.getByText("junior.english.standard · v1", { exact: true })).toBeVisible();
  const save = page.getByRole("button", { name: "保存配置", exact: true });
  await expect(save).toBeEnabled();
  await save.click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0].subject_profile_id).toBe("english-template");
  expect(writes[0].allowed_evidence_types).toEqual(["text_span", "concept", "relation"]);
  await expect(page.getByText("学科评分配置已保存", { exact: true })).toBeVisible();
});

test("综合考试冻结快照的学科继承真实表单只读状态", async ({ page }) => {
  const writes = await openAssessment(page, "综合", "short_answer", true);
  // Ant Design 的显式 disabled=false 会覆盖 Form 上下文；此处验证真实控件，防止替身掩盖只读回归。
  await expect(page.getByLabel("学科", { exact: true })).toBeDisabled();
  await expect(page.getByLabel("学段", { exact: true })).toBeDisabled();
  await expect(page.getByLabel("默认评分模式", { exact: true })).toBeDisabled();
  await expect(page.getByText("junior.english.standard · v1", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "保存配置", exact: true })).toHaveCount(0);
  expect(writes).toHaveLength(0);
});

test("公式题规则和 AI 模式均说明人工要求，显式策略保持原选择", async ({ page }) => {
  const writes = await openAssessment(page, "math", "formula", false, false);
  await expect(page.getByText("本题需要教师人工评分", { exact: true })).toBeVisible();
  await expect(page.getByText(/最终分数仍由规则或教师确认/)).toHaveCount(0);
  await openAssessmentConfiguration(page);
  const drawer = page.getByRole("dialog", { name: "1 · 学科评分配置", exact: true });
  await expect(drawer.getByText(/如需自动判分，请选择规则自动评分/)).toHaveCount(0);
  await expect(drawer.getByText(/本题.*人工评分/, { exact: true })).toBeVisible();
  // 选中值覆盖内部搜索框，使用真实组合框的键盘操作打开选项，避免点击到被遮挡的输入。
  await page.getByLabel("默认评分模式", { exact: true }).press("ArrowDown");
  await page.getByTitle("AI 辅助人工评分", { exact: true }).click();
  await expect(drawer.getByText(/本题.*人工评分/, { exact: true })).toBeVisible();
  await expect(drawer.getByText(/如需自动判分，请选择规则自动评分/)).toHaveCount(0);
  await page.getByRole("button", { name: "保存配置", exact: true }).click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0].scoring_policy.mode).toBe("AI_ASSIST");
  await expect(page.getByText("学科评分配置已保存", { exact: true })).toBeVisible();
  await expect(drawer).not.toBeVisible();
  // 抽屉关闭后会保留隐藏内容；核对主界面的提示，避免被抽屉里的同名文本干扰。
  await expect(page.locator("section.question-editor").getByText("本题需要教师人工评分", { exact: true })).toBeVisible();
  await expect(page.getByText(/最终分数仍由规则或教师确认/)).toHaveCount(0);
});

test("选择题显式 AI 模式仍提示可选的规则自动路径", async ({ page }) => {
  await openAssessment(page, "math", "single_choice");
  await page.getByLabel("默认评分模式", { exact: true }).press("ArrowDown");
  await page.getByTitle("AI 辅助人工评分", { exact: true }).click();
  await expect(page.getByText(/如需自动判分，请选择规则自动评分并核对固定规则/)).toBeVisible();
});

for (const questionType of ["short_answer", "calculation", "essay", "discussion"]) {
  test(`${questionType} 的规则模式说明实际转人工，保存保留显式选择`, async ({ page }) => {
    const writes = await openAssessment(page, "math", questionType);
    const drawer = page.getByRole("dialog", { name: "1 · 学科评分配置", exact: true });
    await page.getByLabel("默认评分模式", { exact: true }).press("ArrowDown");
    await page.getByTitle("规则自动评分", { exact: true }).click();
    // 题型能力按真实题目核对，保存兼容旧策略；提示必须在发 PUT 之前可见。
    await expect(drawer.getByText(/会转入人工评分/)).toBeVisible();
    await expect(drawer.getByText(/规则.*不会自动/)).toBeVisible();
    await page.getByRole("button", { name: "保存配置", exact: true }).click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0].scoring_policy.mode).toBe("RULE_AUTO");
    await expect(page.getByText("学科评分配置已保存", { exact: true })).toBeVisible();
  });
}

test("编程题在主界面和配置抽屉均说明教师人工评分", async ({ page }) => {
  await openAssessment(page, "math", "coding", false, false);
  await expect(page.getByText("本题需要教师人工评分", { exact: true })).toBeVisible();
  await expect(page.getByText(/编程题目前需要教师人工评分/)).toBeVisible();
  await openAssessmentConfiguration(page);
  const drawer = page.getByRole("dialog", { name: "1 · 学科评分配置", exact: true });
  await expect(drawer.getByText("本题需要教师人工评分", { exact: true })).toBeVisible();
  await drawer.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(drawer).not.toBeVisible();
  await expect(page.locator("section.question-editor").getByText("本题需要教师人工评分", { exact: true })).toBeVisible();
});

test("计算题的步骤证据不承诺规则路径，AI 建议仍说明教师确认", async ({ page }) => {
  await openAssessment(page, "math", "calculation", false, false);
  await expect(page.getByText(/证据本身不会自动给分/)).toBeVisible();
  await expect(page.getByText(/最终分数仍由规则或教师确认/)).toHaveCount(0);
  await openAssessmentConfiguration(page);
  const drawer = page.getByRole("dialog", { name: "1 · 学科评分配置", exact: true });
  await expect(drawer.getByText("AI 建议分数仍须教师确认", { exact: true })).toBeVisible();
});
