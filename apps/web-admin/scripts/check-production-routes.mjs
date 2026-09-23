import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

const root = process.cwd();

function read(path) {
  return readFileSync(join(root, path), "utf8");
}

const routes = read("src/router/routes.tsx");
const dashboard = read("src/pages/DashboardPage.tsx");
const appLayout = read("src/components/AppLayout.tsx");
const appEntry = read("src/App.tsx");
const appShell = read("src/AppShell.tsx");
const appRouter = read("src/router/appRouter.tsx");
const login = read("src/pages/LoginPage.tsx");
const grading = read("src/features/grading/workbench/GradingWorkbench.tsx");
const gradingStyles = read("src/features/grading/workbench/grading-workbench.css");
const experience = read("src/router/experience.ts");
const teacherDashboard = read("src/pages/TeacherDashboardPage.tsx");
const responsiveTable = read("src/components/ResponsiveTable.tsx");
const examWorkspace = read("src/pages/ExamWorkspacePage.tsx");
const examManagement = read("src/pages/ExamManagementPage.tsx");
const appealApi = read("src/api/appeals.ts");
const apiClient = read("src/api/client.ts");
const viteConfig = read("vite.config.ts");

function sourceFiles(directory) {
  return readdirSync(join(root, directory), { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return sourceFiles(path);
    return /\.(ts|tsx)$/.test(entry.name) ? [read(path)] : [];
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

assert(
  /function PlatformDashboard/.test(dashboard) && /setStatus\(await getSystemStatus\(\)\)/.test(dashboard) && /if \(isPlatform\) return <PlatformDashboard/.test(dashboard),
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
  const routePattern = new RegExp(`key:\\s*["']${key}["'][\\s\\S]*?productionReady:\\s*false`);
  assert(routePattern.test(routes), `Route ${key} must remain explicitly marked as non-production.`);
}

const productionEntry = join(root, "dist", "index.html");
if (existsSync(productionEntry)) {
  const entryHtml = readFileSync(productionEntry, "utf8");
  const entryScript = entryHtml.match(/src=["']\/?(assets\/index-[^"']+\.js)["']/)?.[1];
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
  /experienceLabel\(experience\)/.test(appLayout) && /onExperienceChange/.test(appLayout),
  "AppLayout must identify the current product end and support explicit mixed-role switching."
);

assert(
  /listReviewTasks\(\{ assigned_to: user\.id \}\)/.test(teacherDashboard)
    && /listArbitrationTasks\(\{ assigned_to: user\.id \}\)/.test(teacherDashboard),
  "Teacher home must request only review and arbitration tasks assigned to the current user."
);

assert(
  /personalScope \? \{ assigned_to: currentUserId \} : \{\}/.test(grading),
  "Teacher grading must enforce personal task scope in the API query."
);

assert(
  /teacher:\s*\[\]/.test(routes)
    && /hasExamWorkspaceSectionAccess/.test(appShell),
  "Teacher identities must remain outside administrator exam workspaces for navigation and deep links."
);

assert(
  /AdminGradingOperationsPage/.test(appShell) && /experience === "admin"/.test(appShell),
  "Administrator grading navigation must open exam-level operations instead of the personal workbench."
);

assert(
  /\/assign/.test(appealApi) && /\/recommendation/.test(appealApi),
  "Appeal API client must expose assignment and teacher recommendation endpoints."
);

assert(
  /Drawer/.test(appLayout) && /mobile-nav-button/.test(appLayout),
  "Compact viewports must use a navigation drawer."
);

assert(
  /responsive-record-list/.test(responsiveTable)
    && /columns=\{columns\}/.test(responsiveTable)
    && /scroll=\{scroll\}/.test(responsiveTable)
    && /size=\{tableProps\.size \?\? ["']small["']\}/.test(responsiveTable),
  "Wide tables must keep their desktop width, fixed-column, ellipsis, scroll, and compact-size contracts."
);

const allUiSource = sourceFiles("src").join("\n");
const gradingFeature = sourceFiles("src/features/grading/workbench").join("\n");

assert(
  /\.immersive-workspace \.grading-inspector\s*\{[\s\S]*?overflow-y:\s*auto;/.test(gradingStyles)
    && /@media \(min-width: 1100px\) and \(max-height: 820px\)[\s\S]*?\.immersive-workspace \.grading-workspace\s*\{[\s\S]*?overflow:\s*visible;[\s\S]*?\.immersive-workspace \.grading-inspector\s*\{[\s\S]*?overflow:\s*visible;/.test(gradingStyles),
  "Immersive grading must keep the scoring inspector scrollable and restore document flow on short desktops."
);

const reviewerProgress = grading.match(/const reviewerProgress[\s\S]*?\n  \}, \[[^\n]+\]\);/)?.[0] ?? "";
assert(
  /taskAggregate\?\.reviewers/.test(reviewerProgress)
    && /setTaskAggregate\(result\.aggregate \?\? null\)/.test(grading)
    && !/filteredTasks/.test(reviewerProgress),
  "Reviewer progress must use the complete server-side task aggregate, independent of the current queue filter."
);

assert(
  /loadTaskContext\(taskId: string, allowOriginalImage: boolean\)/.test(gradingFeature)
    && /originalImageUrl:\s*allowOriginalImage\s*\?\s*artifact\.original_image_url\s*:\s*undefined/.test(gradingFeature)
    && /canViewOriginalImage=\{experience === "admin"\}/.test(appShell)
    && /mode === "original" && \(!canViewOriginalImage/.test(gradingFeature)
    && /options=\{canViewOriginalImage/.test(gradingFeature),
  "Teacher grading must neither retain nor request the original answer-sheet image."
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
  /function nextStatus\(status: string\) \{[\s\S]*?status === "finalized"[\s\S]*?return null;/.test(examManagement),
  "The generic exam status action must stop at finalized instead of publishing scores."
);

assert(
  /currentRoute\.mock/.test(appLayout) && !/<MockBadge compact \/>/.test(appLayout),
  "AppLayout must show the mock badge only for the current mock route."
);

assert(
  !/global-search|aria-label="任务队列"|aria-label="通知"/.test(appLayout),
  "AppLayout must not expose top-bar controls without implemented behavior."
);

assert(
  /lazy\(\(\) => import/.test(appShell) && /Suspense/.test(appShell),
  "Production pages must be loaded on demand."
);

assert(
  !/Demo 教育集团|平台租户/.test(login) && /name="tenant_code"/.test(login),
  "Login must accept real tenant codes instead of a fixed demo tenant list."
);

assert(
  /function normalizeBaseUrl[\s\S]*?return trimmed;/.test(apiClient)
    && !/return trimmed \|\| ["']http:\/\/127\.0\.0\.1:8080/.test(apiClient),
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
