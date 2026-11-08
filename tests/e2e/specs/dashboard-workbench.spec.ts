import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";
import { loginAsSchoolAdmin } from "../fixtures/schoolAdminLogin";

// 默认模拟数据含 4 类阻断事项、一场准备中的草稿和两道未阅完的题，覆盖首页各区块的排序与折叠规则（STORY-080）。
test("学校工作台按状态结论、待办和进度排列", async ({ page }) => {
  await installApiMocks(page);
  await loginAsSchoolAdmin(page);

  await expect(page.getByRole("region", { name: "当前状态" })).toContainText("4 件事会卡住出成绩：1 份答卷处理失败，1 份答卷未匹配学生等");
  await expect(page.getByRole("heading", { name: "成员管理" })).toHaveCount(0);

  const todo = page.getByRole("region", { name: "需要我处理" });
  await expect(todo.getByRole("row").nth(1)).toContainText("答卷处理失败");
  await expect(todo.getByRole("row", { name: /图像质量需要处理/ })).toContainText("2026 春季数学期中考试");
  await expect(todo.getByRole("row", { name: /待人工复核/ })).toContainText("高一英语期末考试");
  await expect(todo.getByRole("row", { name: /成绩复核申请待分派/ })).toContainText("2");
  await expect(todo.getByText("成绩复核待裁决")).toHaveCount(0);
  await expect(todo.getByRole("row", { name: /成绩待发布/ })).toContainText("高一物理月考");

  const exams = page.getByRole("region", { name: "考试进度" });
  await expect(exams.getByRole("row").nth(1)).toContainText("已阅 145/252 项，待复核 3 项");
  await expect(exams.getByRole("row").nth(2)).toContainText("已收 47/50 人");
  await expect(exams.getByRole("row").nth(3)).toContainText("阅卷完成，待发布");
  await expect(exams.getByText("另有 1 场考试在准备中")).toBeVisible();
  await expect(exams.getByText("高二语文月考")).toHaveCount(0);

  const byQuestion = page.getByRole("region", { name: "按题" });
  await expect(byQuestion.getByRole("row").nth(1)).toContainText("17");
  await expect(byQuestion.getByRole("row").nth(1)).toContainText("约 7 小时");

  const byTeacher = page.getByRole("region", { name: "按老师" });
  await expect(byTeacher.getByText("数学阅卷老师")).toBeVisible();
  await expect(byTeacher.getByText("新教师")).toHaveCount(0);
  await byTeacher.getByRole("button", { name: "显示无任务教师（1）" }).click();
  await expect(byTeacher.getByText("新教师")).toBeVisible();
});

test("桌面壳层取消空置顶栏，并固定品牌、菜单和账户区域", async ({ page }) => {
  await installApiMocks(page, { initiallyAuthenticated: true });
  await page.goto("/#/admin/dashboard", { waitUntil: "domcontentloaded" });

  await expect(page.locator(".topbar")).toHaveCount(0);
  await expect(page.locator(".brand-copy")).toContainText("EduGrade");
  await expect(page.getByRole("button", { name: "账户菜单：school_admin（学校管理员）" })).toBeVisible();
  await expect(page.locator(".sidebar-account-copy strong")).toHaveText("school_admin");

  const expandedWidth = await page.locator(".sidebar").evaluate((element) => element.getBoundingClientRect().width);
  await expect(page.locator(".brand-copy")).toBeVisible();
  await page.getByRole("button", { name: "收起导航" }).click();
  await expect(page.getByRole("button", { name: "展开导航" })).toBeVisible();
  let collapsedWidth = expandedWidth;
  await expect.poll(async () => {
    collapsedWidth = await page.locator(".sidebar").evaluate((element) => element.getBoundingClientRect().width);
    return collapsedWidth;
  }).toBeLessThanOrEqual(66);
  expect(expandedWidth).toBeGreaterThan(collapsedWidth);
  await expect(page.locator(".brand-copy")).toBeHidden();
  await expect(page.locator(".sidebar-account-copy")).toBeHidden();

  await page.getByRole("button", { name: "展开导航" }).click();
  await page.getByRole("button", { name: "账户菜单：school_admin（学校管理员）" }).click();
  await expect(page.getByText("当前登录用户名：school_admin（学校管理员）", { exact: true })).toBeVisible();
  await expect(page.getByText("退出登录", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByText("个人信息与账户安全", { exact: true })).toBeVisible();
});

test("移动端只保留导航触发条，账户位于抽屉底部", async ({ page }) => {
  await page.setViewportSize({ width: 760, height: 800 });
  await installApiMocks(page, { initiallyAuthenticated: true });
  await page.goto("/#/admin/dashboard", { waitUntil: "domcontentloaded" });

  await expect(page.locator(".topbar")).toHaveCount(0);
  const shellbar = page.locator(".mobile-shellbar");
  await expect(shellbar).toBeVisible();
  expect(await shellbar.evaluate((element) => element.getBoundingClientRect().height)).toBeLessThanOrEqual(48);
  await page.getByRole("button", { name: "打开主导航" }).click();
  await expect(page.locator(".mobile-navigation")).toBeVisible();
  await expect(page.getByRole("button", { name: "账户菜单：school_admin（学校管理员）" })).toBeVisible();
  await expect(page.locator(".mobile-navigation .sidebar-footer")).toBeVisible();
});

test("首页主动作完成单页新建考试并进入考试准备", async ({ page }) => {
  test.setTimeout(90_000);
  await installApiMocks(page);
  await loginAsSchoolAdmin(page);

  await page.getByRole("button", { name: "新建考试" }).click();
  await expect(page).toHaveURL(/\/admin\/exams\/new$/);
  await expect(page.getByRole("region", { name: "创建方式" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "1. 考试名称与基本信息" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "2. 添加考试学科" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "3. 考试班级范围" })).toBeVisible();
  await page.getByRole("textbox", { name: "考试名称 *" }).fill("2026-2027学年高二期中考试");
  await page.getByRole("button", { name: /高二（1）班/ }).click();
  await expect(page.getByText(/已选择.*1.*个班级/)).toBeVisible();
  await page.getByRole("button", { name: /使用考试方案/ }).click();
  await expect(page.getByRole("dialog", { name: "选择考试方案" })).toBeVisible();
  await page.getByRole("button", { name: /系统通用高中考试方案/ }).click();
  await expect(page.getByText("已套用试卷结构")).toHaveCount(3);
  await page.locator(".exam-composer-submit").getByRole("button", { name: "使用方案创建考试" }).click();
  await expect(page).toHaveURL(/\/admin\/exams\/exam-created-math\/students$/);
});

// 考试进度的“下一步”沿用 examAction；与新建考试流程分开，避免建考页面的变动掩盖首页跳转结果。
test("考试进度的下一步进入对应考试环节", async ({ page }) => {
  await installApiMocks(page);
  await loginAsSchoolAdmin(page);

  await page.getByRole("region", { name: "考试进度" }).getByRole("button", { name: "继续阅卷" }).click();
  await expect(page).toHaveURL(/\/admin\/exams\/exam-3\/grading$/);

  await page.goto("/#/admin/dashboard");
  await page.getByRole("region", { name: "考试进度" }).getByRole("button", { name: /处理答卷/ }).click();
  await expect(page).toHaveURL(/\/admin\/exams\/exam-1\/capture$/);
});

test("没有待办时使用紧凑空状态", async ({ page }) => {
  await installApiMocks(page, { dashboardMode: "empty", onboardingReady: true });
  await loginAsSchoolAdmin(page);

  await expect(page).toHaveURL(/\/admin\/dashboard$/);
  await expect(page.getByRole("region", { name: "当前状态" })).toContainText("目前没有卡住出成绩的事");
  await expect(page.getByText("当前没有需要处理的事", { exact: true })).toBeVisible();
  await expect(page.getByText("暂无进行中的考试")).toBeVisible();
  await expect(page.getByRole("heading", { name: "阅卷进度" })).toHaveCount(0);
});

// 成员卡片已移除；班级和教师的缺口改由“需要我处理”进入正式页面。
test("基础数据缺口从需要我处理进入正式班级和教师页面", async ({ page }) => {
  test.setTimeout(60_000);
  await installApiMocks(page);
  await loginAsSchoolAdmin(page);

  const todo = page.getByRole("region", { name: "需要我处理" });
  await todo.getByRole("row", { name: /班级没有学生/ }).getByRole("button", { name: "导入学生" }).click();
  await expect(page).toHaveURL(/\/admin\/members\/students$/);
  await expect(page.getByRole("heading", { name: "学生管理" })).toBeVisible({ timeout: 30_000 });

  await page.goto("/#/admin/dashboard");
  await page.getByRole("region", { name: "需要我处理" }).getByRole("row", { name: /教师未设置任教学科/ }).getByRole("button", { name: "设置学科" }).click();
  await expect(page).toHaveURL(/\/admin\/members\/teachers$/);
  await expect(page.getByRole("heading", { name: "人员与权限", exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("数学阅卷老师")).toBeVisible();
});
