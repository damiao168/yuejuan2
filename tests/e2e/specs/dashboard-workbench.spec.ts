import { expect, test } from "@playwright/test";
import { installApiMocks } from "../fixtures/apiMocks";
import { loginAsSchoolAdmin } from "../fixtures/schoolAdminLogin";

test("学校工作台按成员、考试、阅卷与成绩组织真实业务工作", async ({ page }) => {
  await installApiMocks(page);
  await loginAsSchoolAdmin(page);

  await expect(page.getByRole("heading", { name: "成员管理" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "考试管理" })).toBeVisible();
  await expect(page.getByText(/进行中 3 场/)).toBeVisible();
  await expect(page.getByRole("heading", { name: "阅卷与成绩" })).toBeVisible();
  await expect(page.getByText("答卷处理失败")).toBeVisible();
  await expect(page.getByText("主观题等待确认")).toBeVisible();
  await expect(page.getByRole("button", { name: /管理学生/ })).toBeVisible();
});

test("桌面壳层取消空置顶栏，并固定品牌、菜单和账户区域", async ({ page }) => {
  await installApiMocks(page, { initiallyAuthenticated: true });
  await page.goto("/#/admin/dashboard", { waitUntil: "networkidle" });

  await expect(page.locator(".topbar")).toHaveCount(0);
  await expect(page.locator(".brand-copy")).toContainText("EduGrade");
  await expect(page.getByRole("button", { name: "账户菜单：学校管理员" })).toBeVisible();

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
  await page.getByRole("button", { name: "账户菜单：学校管理员" }).click();
  await expect(page.getByText("账户安全", { exact: true })).toBeVisible();
  await expect(page.getByText("退出登录", { exact: true })).toBeVisible();
});

test("移动端只保留导航触发条，账户位于抽屉底部", async ({ page }) => {
  await page.setViewportSize({ width: 760, height: 800 });
  await installApiMocks(page, { initiallyAuthenticated: true });
  await page.goto("/#/admin/dashboard", { waitUntil: "networkidle" });

  await expect(page.locator(".topbar")).toHaveCount(0);
  const shellbar = page.locator(".mobile-shellbar");
  await expect(shellbar).toBeVisible();
  expect(await shellbar.evaluate((element) => element.getBoundingClientRect().height)).toBeLessThanOrEqual(48);
  await page.getByRole("button", { name: "打开主导航" }).click();
  await expect(page.locator(".mobile-navigation")).toBeVisible();
  await expect(page.getByRole("button", { name: "账户菜单：学校管理员" })).toBeVisible();
  await expect(page.locator(".mobile-navigation .sidebar-footer")).toBeVisible();
});

test("首页主动作完成单页新建考试并进入考试准备", async ({ page }) => {
  test.setTimeout(90_000);
  await installApiMocks(page);
  await loginAsSchoolAdmin(page);

  await page.getByRole("button", { name: "新建考试" }).click();
  await expect(page).toHaveURL(/#\/admin\/exams\/new/);
  await expect(page.getByRole("heading", { name: "新建考试" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "1. 考试名称与基本信息" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "2. 添加考试学科" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "3. 考试班级范围" })).toBeVisible();
  await page.getByRole("textbox", { name: "考试名称 *" }).fill("2026-2027学年高二期中考试");
  await page.getByRole("combobox", { name: /考试类型/ }).click();
  await page.getByTitle("期中考试").click();
  await page.getByRole("button", { name: /高二（1）班/ }).click();
  await expect(page.getByText(/已选择.*1.*个班级/)).toBeVisible();
  await page.getByRole("button", { name: /使用考试方案/ }).click();
  await expect(page.getByRole("dialog", { name: "选择考试方案" })).toBeVisible();
  await page.getByRole("button", { name: /系统通用高中考试方案/ }).click();
  await expect(page.getByText("已套用试卷结构")).toHaveCount(3);
  await page.locator(".exam-composer-submit").getByRole("button", { name: "使用方案创建考试" }).click();
  await expect(page).toHaveURL(/#\/admin\/exams\/exam-created-math\/students/);

  await page.goto("/#/admin/dashboard");
  await page.getByRole("row", { name: /主观题等待确认/ }).getByRole("button", { name: "继续阅卷" }).click();
  await expect(page).toHaveURL(/#\/admin\/grading\?status=pending/);

  await page.goto("/#/admin/dashboard");
  await page.getByRole("button", { name: /处理答卷/ }).click();
  await expect(page).toHaveURL(/#\/admin\/exams\/exam-1\/capture/);
});

test("没有待办时使用紧凑空状态", async ({ page }) => {
  await installApiMocks(page, { dashboardMode: "empty" });
  await loginAsSchoolAdmin(page);

  await expect(page.getByText("当前没有需要处理的事项")).toBeVisible();
  await expect(page.getByText("暂无进行中考试")).toBeVisible();
  await expect(page.getByText("进行中 0 场", { exact: false })).toBeVisible();
});

test("成员管理入口进入正式班级和教师页面", async ({ page }) => {
  test.setTimeout(60_000);
  await installApiMocks(page);
  await loginAsSchoolAdmin(page);

  await page.getByRole("button", { name: /年级与班级/ }).click();
  await expect(page).toHaveURL(/#\/admin\/members\/classes/);
  await expect(page.getByRole("heading", { name: "年级与班级" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("高二（1）班")).toBeVisible();

  await page.goto("/#/admin/dashboard");
  await page.getByRole("button", { name: /阅卷教师/ }).click();
  await expect(page).toHaveURL(/#\/admin\/members\/teachers/);
  await expect(page.getByRole("heading", { name: "人员与访问管理" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("数学阅卷老师")).toBeVisible();
});
