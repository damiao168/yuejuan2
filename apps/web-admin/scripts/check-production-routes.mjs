import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { movedProductionRouteFailures } from "./production-route-contracts.mjs";

const root = process.cwd();

function read(path) {
  return readFileSync(join(root, path), "utf8");
}

const routes = read("src/router/routes.tsx");
const dashboard = read("src/pages/DashboardPage.tsx");
const appLayout = read("src/components/AppLayout.tsx");
const appEntry = read("src/App.tsx");
const appShell = read("src/AppShell.tsx");
const pageRendering = read("src/router/pageRendering.tsx");
const layoutPresentation = read("src/components/appLayoutPresentation.tsx");
const managementOverview = read("src/features/exams/management/ManagementExamOverviewPage.tsx");
const appRouter = read("src/router/appRouter.tsx");
const login = read("src/pages/LoginPage.tsx");
const grading = read("src/features/grading/workbench/GradingWorkbench.tsx");
const gradingQueue = read("src/features/grading/workbench/hooks/useGradingQueue.ts");
const gradingStyles = read("src/features/grading/workbench/grading-workbench.css");
const experience = read("src/router/experience.ts");
const teacherDashboard = read("src/pages/TeacherDashboardPage.tsx");
const responsiveTable = read("src/components/ResponsiveTable.tsx");
const examManagement = read("src/pages/ExamManagementPage.tsx");
const appealApi = read("src/api/appeals.ts");
const apiClient = read("src/api/client.ts");
const viteConfig = read("vite.config.ts");

function sourceFiles(directory) {
  return readdirSync(join(root, directory), { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return /\.(ts|tsx)$/.test(entry.name) && !/\.(test|spec)\./.test(entry.name) ? [read(path)] : [];
  });
}

const failures = [];

function assert(condition, message) {
  if (!condition) {
    failures.push(message);
  }
}

assert(
  !/from\s+["']\.\.\/data["']/.test(dashboard),
  "DashboardPage must not import static demo data from src/data.ts."
);

assert(
  /getDashboardSummary/.test(dashboard) && /pending_review_question_count/.test(dashboard) && /active_exam_count/.test(dashboard),
  "DashboardPage must read trusted, unit-aware business work from the dashboard summary API."
);

// 平台统计可以并行读取；检查调用所在分支，不能把一种 await 写法当成权限边界。
const platformDashboard = dashboard.split("function PlatformDashboard")[1]?.split("export function DashboardPage")[0] ?? "";
const schoolDashboard = dashboard.split("export function DashboardPage")[1] ?? "";
assert(
  /getSystemStatus\(\)/.test(platformDashboard)
    && !/getSystemStatus\(\)/.test(schoolDashboard)
    && /const isPlatform = user\.roles\.includes\("platform_admin"\)/.test(schoolDashboard)
    && /if \(isPlatform\) return <PlatformDashboard/.test(schoolDashboard),
  "System status must be restricted to the operations home variant."
);

assert(
  /productionReady/.test(routes)
    && /createRouteRegistry/.test(routes)
    && /mockRoutesEnabled/.test(routes)
    && /visibleRoutes/.test(routes),
  "Routes must declare production readiness and build an environment-specific route registry."
);

for (const key of ["review", "quality", "permissions", "settings"]) {
  const routePattern = new RegExp(String.raw`key:\s*["']${key}["'][\s\S]*?productionReady:\s*false`);
  assert(routePattern.test(routes), `Route ${key} must remain explicitly marked as non-production.`);
}

// 源码规则每次都检查；只有存在构建产物时才继续检查入口包，避免把构建目录缺失误判为失败。
const productionEntry = join(root, "dist", "index.html");
if (existsSync(productionEntry)) {
  const entryHtml = readFileSync(productionEntry, "utf8");
  const entryScript = /src=["']\/?(assets\/index-[^"']+\.js)["']/.exec(entryHtml)?.[1];
  assert(Boolean(entryScript), "Production build must expose a discoverable application entry chunk.");
  if (entryScript) {
    const bundle = readFileSync(join(root, "dist", entryScript), "utf8");
    for (const path of ["/review", "/quality", "/permissions", "/settings"]) {
      assert(!bundle.includes(`path:"${path}"`), `Production bundle must not register mock route ${path}.`);
    }
  }
}

assert(
  /MODE === ["']production["'][\s\S]*?return false;/.test(routes)
    && /const includeMockRoutes = import\.meta\.env\.MODE !== ["']production["']/.test(routes)
    && /export const routes = includeMockRoutes[\s\S]*?: productionRouteDefinitions;/.test(routes),
  "Production mode must construct a registry that cannot be overridden to include mock routes."
);

assert(
  /routeFromPath[\s\S]*registry\.find\(\(route\) => route\.path === canonicalPath\)/.test(routes)
    && !/routes\.filter\(isRouteVisible\)/.test(routes),
  "routeFromPath must resolve the active registry rather than filter a registry containing mock routes."
);

assert(
  /hasRouteAccess\(user, route, experience\)/.test(appLayout) && /visibleRoutes\(experience\)/.test(appLayout),
  "AppLayout menu must combine production visibility with user permissions."
);

assert(
  /ADMIN_ROLES[\s\S]*platform_admin[\s\S]*tenant_admin[\s\S]*school_admin/.test(experience)
    && /TEACHER_ROLES[\s\S]*teacher[\s\S]*grader[\s\S]*arbitrator/.test(experience),
  "Product experiences must be derived from the approved administrator and teacher role sets."
);

assert(
  /experienceFromPath/.test(experience) && /canonicalPathFromPath/.test(experience) && /pathForExperience/.test(experience),
  "Experience routing must support prefixed hashes and canonical internal paths."
);

assert(
  /RouterProvider/.test(appEntry) && /component:\s*AppShell/.test(appRouter),
  "The production router must mount AppShell, where role-aware route composition lives."
);

assert(
  /defaultExperience\(nextUser\)/.test(appShell) && /hasExperienceAccess\(user, experience\)/.test(appShell),
  "Login and deep-link access must enforce the role-specific product experience."
);

assert(
  /listReviewTasks\(\{ assigned_to: user\.id \}\)/.test(teacherDashboard)
    && /listArbitrationTasks\(\{ assigned_to: user\.id \}\)/.test(teacherDashboard),
  "Teacher home must request only review and arbitration tasks assigned to the current user."
);

// 列表、分页和进度共用作用域对象；检查其接线，不再要求在每个请求处重复同一权限条件。
const sharedPersonalScope = /const scopeQuery[\s\S]*?\n {2}\}\), \[[^\n]+\]\);/.exec(gradingQueue)?.[0] ?? "";
const filteredTaskRequests = gradingQueue.match(/listReviewTasks\(\{\s*\.\.\.filterQuery,/g) ?? [];
const scopedAggregateRequests = gradingQueue.match(/listReviewTasks\(\{\s*\.\.\.scopeQuery,/g) ?? [];
assert(
  /useGradingQueue/.test(grading)
    && /personalScope\s*\|\|\s*\(canManageTasks\s*&&\s*canWork\s*&&\s*queueScope\s*===\s*"mine"\)/.test(sharedPersonalScope)
    && /assigned_to:\s*currentUserId/.test(sharedPersonalScope)
    && /const filterQuery[\s\S]*?\.\.\.scopeQuery/.test(gradingQueue)
    && filteredTaskRequests.length >= 3 && scopedAggregateRequests.length >= 2,
  "Teacher grading must enforce personal task scope in initial load, pagination, and aggregate refresh."
);

assert(
  /\/assign/.test(appealApi) && /\/recommendation/.test(appealApi),
  "Appeal API client must expose assignment and teacher recommendation endpoints."
);

assert(
  /responsive-record-list/.test(responsiveTable)
    && /columns=\{columns\}/.test(responsiveTable)
    && /scroll=\{scroll\}/.test(responsiveTable)
    && /size=\{tableProps\.size \?\? ["']small["']\}/.test(responsiveTable),
  "Wide tables must keep their desktop width, fixed-column, ellipsis, scroll, and compact-size contracts."
);

const gradingFeature = sourceFiles("src/features/grading/workbench").join("\n");

assert(
  /\.immersive-workspace \.grading-inspector\s*\{[\s\S]*?overflow-y:\s*auto;/.test(gradingStyles)
    && /@media \(min-width: 1100px\) and \(max-height: 820px\)[\s\S]*?\.immersive-workspace \.grading-workspace\s*\{[\s\S]*?overflow:\s*visible;[\s\S]*?\.immersive-workspace \.grading-inspector\s*\{[\s\S]*?overflow:\s*visible;/.test(gradingStyles),
  "Immersive grading must keep the scoring inspector scrollable and restore document flow on short desktops."
);

const reviewerProgress = /const reviewerProgress[\s\S]*?\n {2}\}, \[[^\n]+\]\);/.exec(gradingQueue)?.[0] ?? "";
assert(
  /taskAggregate\?\.reviewers/.test(reviewerProgress)
    && /setTaskAggregate\(progress\.aggregate \?\? null\)/.test(gradingQueue)
    && /listReviewTasks\(\{\s*\.\.\.scopeQuery,\s*limit:\s*1\s*\}\)/.test(gradingQueue)
    && !/filteredTasks/.test(reviewerProgress),
  "Reviewer progress must use the complete server-side task aggregate, independent of the current queue filter."
);

assert(
  /delivery_mode\s*===\s*["']shadow_only["']/.test(gradingFeature)
    && /delivery_mode\s*!==\s*["']shadow_only["']/.test(gradingFeature),
  "Shadow-only AI results must be hidden and impossible to adopt."
);

assert(
  /\.grade_source\s*===\s*["']rule_confirmed["']/.test(gradingFeature)
    && /未生效 · 转人工/.test(gradingFeature),
  "Automatic scoring labels must require the rule_confirmed grade source."
);

assert(
  /currentRoute\.mock/.test(appLayout) && !/<MockBadge compact \/>/.test(appLayout),
  "AppLayout must show the mock badge only for the current mock route."
);

assert(
  !/global-search|aria-label="任务队列"|aria-label="通知"/.test(appLayout),
  "AppLayout must not expose top-bar controls without implemented behavior."
);

failures.push(...movedProductionRouteFailures({ routes, appLayout, layoutPresentation, appShell, pageRendering, examManagement, managementOverview, gradingFeature }));
assert(
  !/Demo 教育集团|平台租户/.test(login) && /name="tenant_code"/.test(login),
  "Login must accept real tenant codes instead of a fixed demo tenant list."
);

// 地址整理已支持线性截取尾斜杠；同源边界由空默认地址及保留空值共同保证，不能只认旧 return 写法。
const baseUrlNormalization = apiClient.split("export function normalizeBaseUrl")[1]?.split("\n}")[0] ?? "";
assert(
  /normalizeBaseUrl\(options\.baseUrl\s*\?\?\s*import\.meta\.env\.VITE_API_BASE_URL\s*\?\?\s*["']["']\)/.test(apiClient)
    && /return trimmed(?:\.slice\(0, end\))?;/.test(baseUrlNormalization)
    && !/return trimmed \|\| ["']http:\/\/127\.0\.0\.1:8080/.test(baseUrlNormalization),
  "Web API calls must default to the same origin so the deployment proxy handles authentication."
);

assert(
  /server:\s*\{[\s\S]*?proxy:\s*backendProxy/.test(viteConfig)
    && /preview:\s*\{[\s\S]*?proxy:\s*backendProxy/.test(viteConfig),
  "Vite development and preview servers must both proxy same-origin API requests."
);

assert(
  !/待后续 API 支持|相似答案检索|历史样例库/.test(grading),
  "Production grading must not expose placeholders for later stories."
);

assert(
  /key:\s*"arbitration"[\s\S]*?anyPermissions:\s*\["arbitration:manage",\s*"arbitration:work"\]/.test(routes),
  "Arbitration navigation must allow existing worker and manager permissions."
);

if (failures.length > 0) {
  console.error("Production route checks failed:");
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log("Production route checks passed.");
