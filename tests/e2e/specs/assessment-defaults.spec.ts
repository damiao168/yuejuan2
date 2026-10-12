import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";
import { openAssessmentConfiguration } from "../fixtures/assessmentNavigation";

const examID = "exam-created-math";
const examPath = `/api/v1/exams/${examID}`;
const mathematicsEvidence = ["selected_option", "exact_text", "text_span", "numeric_value", "math_expression", "math_step", "unit_value", "diagram_feature"];
const scenarios = [
  { questionType: "short_answer", archetype: "short_constructed", evidence: ["text_span", "concept", "relation"], expectedEvidence: ["text_span"], defaultMode: "AI_ASSIST", gradingMode: "ai_assisted", expectedMode: "AI_ASSIST", modeLabel: "AI 建议，教师逐题确认" },
  { questionType: "calculation", archetype: "structured_steps", evidence: ["text_span", "math_expression", "math_step", "unit_value", "chemical_equation"], expectedEvidence: ["text_span", "math_expression", "math_step", "unit_value"], defaultMode: "AI_ASSIST", gradingMode: "ai_assisted", expectedMode: "AI_ASSIST", modeLabel: "AI 建议，教师逐题确认" },
  ...["ai_assisted", "double_mark", "blind_double_mark"].map(gradingMode => ({ questionType: "single_choice", archetype: "selected_response", evidence: ["selected_option"], expectedEvidence: ["selected_option"], defaultMode: "RULE_AUTO", gradingMode, expectedMode: gradingMode === "ai_assisted" ? "RULE_AUTO" : "DUAL_HUMAN", modeLabel: gradingMode === "ai_assisted" ? "规则自动评分" : "两名教师独立评分" }))
];

for (const scenario of scenarios) {
  test(`${scenario.questionType}/${scenario.gradingMode} 的默认配置保存后可正确核对并冻结`, async ({ page }) => {
    test.setTimeout(90_000);
    await page.emulateMedia({ reducedMotion: "reduce" });
    await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
    const question = { id: "question-default", exam_id: examID, question_no: "1", question_type: scenario.questionType, stem: "请根据题意作答。", score: 10, page_no: 1, status: "active", revision: 1, knowledge_points: [], options: ["A", "B"], answer_key: { standard_answer: "A" } };
    const profile = { id: "junior.mathematics.v1", code: "junior.mathematics.standard", education_stage: "junior", subject_code: "mathematics", version: 1, status: "active", parser_policy: {}, evidence_policy: { allowed_types: mathematicsEvidence }, scoring_default: { mode: "AI_ASSIST", require_evidence: true, human_review_below_confidence: true } };
    const exam = { id: examID, tenant_id: "tenant-school", school_id: "school-1", name: "数学准备回归", subject: "math", exam_type: "midterm", total_score: 10, status: "draft", grading_mode: scenario.gradingMode, appeal_enabled: true, publish_policy: "manual", created_by: "user-school_admin", class_ids: ["class-1"], revision: 1 };
    let saved: Record<string, any> | undefined;
    let frozen = false;
    const writes: Array<Record<string, any>> = [];
    const readiness = () => ({ ready: true, confirmed: frozen, configuration_hash: saved ? "configured-question-hash" : "default-question-hash", import_snapshot_available: false, checks: [], advisories: [] });
    // 夹具只模拟 API；组件、Ant Design 表单、题目上下文透传和冻结核对均运行真实页面。
    await page.route(/\/api\/v1(?:\/|$)/, async route => {
      const path = new URL(route.request().url()).pathname;
      const method = route.request().method();
      const json = (body: unknown, status = 200) => route.fulfill({ status, json: body });
      if (path === examPath) return json({ exam: { ...exam, status: frozen ? "ready" : "draft" } });
      if (path === `${examPath}/questions`) return json({ questions: [question] });
      if (path === `${examPath}/papers`) return json({ papers: [] });
      if (path === `${examPath}/paper-imports`) return json({ imports: [] });
      if (path === "/api/v1/questions/question-default/scoring-rules") return json({ scoring_rules: [] });
      if (path === "/api/v1/assessment/subject-profiles") return json({ subject_profiles: [profile] });
      if (path === "/api/v1/assessment/question-archetypes") return json({ question_archetypes: [{ code: scenario.archetype, response_schema: {}, evidence_types: scenario.evidence, default_scoring_mode: scenario.defaultMode }] });
      if (path === `${examPath}/questions/question-default/assessment-snapshot`) return json({ error: { code: "not_found" } }, 404);
      if (path === `${examPath}/questions/question-default/assessment-profile`) {
        if (method === "PUT") {
          const body = route.request().postDataJSON();
          writes.push(body);
          // 与后端相同的学科证据约束拒绝非法默认值，使错误保存不能被宽松夹具掩盖。
          if (body.allowed_evidence_types.some((value: string) => !mathematicsEvidence.includes(value))) return json({ error: { code: "invalid_assessment_configuration" } }, 400);
          saved = { ...body, exam_id: examID, question_id: question.id, education_stage: profile.education_stage, subject_code: profile.subject_code, revision: 1 };
        }
        return saved ? json({ assessment_profile: saved }) : json({ error: { code: "assessment_profile_not_found" } }, 404);
      }
      if (path === `${examPath}/readiness`) return json({ readiness: readiness() });
      if (path === `${examPath}/candidates/refresh`) return json({ candidate_refresh: {} });
      if (path === `${examPath}/roster`) return json({ roster: { summary: { expected: 1, received: 0, graded: 0, absent: 0, unresolved: 0, missing_pages: 0, unidentified: 0 }, entries: [{ key: "student-1", student_id: "student-1", student_name: "学生甲", student_no: "1001", class_name: "一班" }] } });
      if (path === `${examPath}/readiness/confirm`) {
        expect(route.request().postDataJSON().expected_configuration_hash).toBe("configured-question-hash");
        expect(saved?.scoring_policy.mode).toBe(scenario.expectedMode);
        frozen = true;
        return json({ readiness: readiness() });
      }
      return route.fallback();
    });

    await page.goto(`/#/admin/exams/${examID}/settings`, { waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "核对并冻结考试准备", exact: true }).click();
    await expect(page.getByRole("button", { name: "确认冻结并完成准备", exact: true })).toBeDisabled();
    await page.getByRole("button", { name: "前往题目配置", exact: true }).click();
    await page.getByRole("tab", { name: "评分配置", exact: true }).click();
    await openAssessmentConfiguration(page);
    const save = page.getByRole("button", { name: "保存配置", exact: true });
    await expect(save).toBeEnabled();
    await save.click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0].allowed_evidence_types).toEqual(scenario.expectedEvidence);
    expect(writes[0].scoring_policy.mode).toBe(scenario.expectedMode);
    await expect(page.getByText("学科评分配置已保存", { exact: true })).toBeVisible();

    await page.getByRole("navigation", { name: "考试准备", exact: true }).getByRole("button", { name: "确认准备完成", exact: true }).click();
    await page.getByRole("button", { name: "核对并冻结考试准备", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText(scenario.modeLabel, { exact: true })).toBeVisible();
    await dialog.getByRole("button", { name: "确认冻结并完成准备", exact: true }).click();
    await expect.poll(() => frozen).toBe(true);
    await expect(page.getByText("准备已确认", { exact: true })).toBeVisible();
  });
}
