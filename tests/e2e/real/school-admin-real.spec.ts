import { expect, test } from "@playwright/test";
import { loginAsSchoolAdmin } from "../fixtures/schoolAdminLogin";

// 依赖隔离 STORY-060 环境预置账号和考试；本文件没有 API 拦截，验证浏览器到真实网关的路径。
test("学校管理员通过真实网关登录并查看已持久化考试", async ({ page }, testInfo) => {
  const examId = "00000000-0000-0000-0000-000000000604";
  const examName = "STORY-060 Synthetic Chinese Exam";
  const serverErrors: string[] = [];
  const forbiddenResponses: string[] = [];
  let authenticated = false;
  page.on("response", (response) => {
    if (response.url().includes("/api/") && response.status() >= 500) {
      serverErrors.push(`${response.status()} ${response.request().method()} ${response.url()}`);
    }
    if (authenticated && response.url().includes("/api/") && response.status() === 403) {
      forbiddenResponses.push(`${response.request().method()} ${response.url()}`);
    }
  });

  await loginAsSchoolAdmin(page, {
    schoolCode: "platform",
    identifier: "story060_school_admin",
    password: process.env.EDUGRADE_E2E_SCHOOL_ADMIN_PASSWORD ?? "",
  });
  authenticated = true;
  const readiness = await page.evaluate(async () => {
    const response = await fetch("/api/v1/onboarding/readiness", { credentials: "include" });
    if (!response.ok) throw new Error(`readiness request failed: ${response.status}`);
    return response.json() as Promise<{
      ready_for_use: boolean;
      checks: Array<{ key: string; state: string }>;
    }>;
  });
  expect(readiness.ready_for_use).toBe(true);
  for (const key of ["school", "teaching_structure", "students", "staff"]) {
    expect(readiness.checks.find((check) => check.key === key)?.state).toBe("ready");
  }
  await page.getByText("考试列表", { exact: true }).click();
  await expect(page).toHaveURL(/#\/admin\/exams/);
  const examLink = page.getByText(examName, { exact: true });
  await expect(examLink).toBeVisible();
  await examLink.click();
  // 列表先进入整场总览，再由学科入口进入工作区；固定种子 ID 防止误读另一场同名考试。
  await expect(page).toHaveURL(new URL(`/#/admin/exams/overview/standalone/${examId}`, page.url()).href);
  await expect(page.getByRole("heading", { name: examName, exact: true })).toBeVisible();
  const persistedExam = await page.evaluate(async (id) => {
    const response = await fetch(`/api/v1/exams/${encodeURIComponent(id)}`, { credentials: "include" });
    if (!response.ok) throw new Error(`exam request failed: ${response.status}`);
    const payload = await response.json() as { exam: { id: string; name: string; status: string } };
    return payload.exam;
  }, examId);
  // 夹具已按正式生命周期进入 grading，真实 worker 完成批次后仍应保留考试阶段。
  expect(persistedExam).toMatchObject({ id: examId, name: examName, status: "grading" });
  const subjectRow = page.getByRole("row").filter({ has: page.getByRole("cell", { name: "语文", exact: true }) });
  await subjectRow.getByRole("button", { name: "继续阅卷", exact: true }).click();
  await expect(page).toHaveURL(new URL(`/#/admin/exams/${examId}/grading`, page.url()).href);

  for (const section of ["overview", "students", "paper", "questions", "capture", "processing", "grading", "scores", "reports"]) {
    await page.goto(`/#/admin/exams/${encodeURIComponent(examId)}/${section}`);
    await expect(page.getByRole("heading", { name: examName, exact: true })).toBeVisible();
  }

  const foreignExamID = process.env.EDUGRADE_E2E_FOREIGN_EXAM_ID;
  if (foreignExamID) {
    const status = await page.evaluate(async (id) => {
      const response = await fetch(`/api/v1/exams/${encodeURIComponent(id)}`, { credentials: "include" });
      return response.status;
    }, foreignExamID);
    expect([403, 404]).toContain(status);
  }

  expect(forbiddenResponses).toEqual([]);
  expect(serverErrors).toEqual([]);

  await testInfo.attach("system-boundary", {
    body: "No Playwright API routes were registered; requests traversed browser-gateway and api-gateway.",
    contentType: "text/plain"
  });
});
