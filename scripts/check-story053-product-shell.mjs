import fs from "node:fs";
import path from "node:path";

const root = process.cwd();
const checks = [
  {
    name: "story lifecycle",
    file: "docs/stories/STORY-053-product-shell-onboarding-exam-workspace.md",
    includes: ["## Plan Review", "## Spec Fixes", "## Implementation", "## Implementation Review", "## Approval"]
  },
  {
    name: "tenant user administration",
    file: "services/api-gateway/internal/auth/user_admin.go",
    includes: ["decodeAuthJSON(w, r, &input, true)", "strongBootstrapPassword", "auth.user_created", "role is not assignable"]
  },
  {
    name: "strict authentication JSON decoding",
    file: "services/api-gateway/internal/auth/validation.go",
    includes: ["decoder.DisallowUnknownFields()", "http.MaxBytesReader", "request body must contain exactly one JSON value"]
  },
  {
    name: "tenant-safe user store",
    file: "services/api-gateway/internal/auth/store_postgres.go",
    includes: ["ListManagedUsers", "ListAssignableRoles", "CreateManagedUser", "tenant_id = $1::uuid", "code NOT IN ('platform_admin', 'tenant_admin')"]
  },
  {
    name: "user routes",
    file: "services/api-gateway/internal/server/routes_auth.go",
    includes: ["GET /api/v1/users", "POST /api/v1/users", "GET /api/v1/roles", "requireOrgManage"]
  },
  {
    name: "role home shell",
    file: "apps/web-admin/src/pages/DashboardPage.tsx",
    includes: ["user.roles.includes(\"platform_admin\")", "getDashboardSummary", "SchoolDashboard"]
  },
  {
    name: "school operations workbench",
    file: "apps/web-admin/src/features/dashboard/SchoolDashboard.tsx",
    includes: ["成员管理", "考试管理", "阅卷与成绩", "organizationStatistics", "workItems"]
  },
  {
    name: "organization activation",
    file: "apps/web-admin/src/pages/OrganizationSetupPage.tsx",
    includes: ["机构信息", "学年与年级", "Papa.parse", "字段", "createManagedUser", "createExam"]
  },
  {
    name: "exam workspace",
    file: "apps/web-admin/src/features/exams/workspace/ExamWorkspaceLayout.tsx",
    includes: ["WorkspaceStageRail", "ExamStageIndicator", "QualityIndicator", "data.exam_id", "当前下一步"]
  },
  {
    name: "URL exam context",
    file: "apps/web-admin/src/router/routes.tsx",
    includes: ["examWorkspaceFromPath", "/exams/:examId/overview", "allowedRoles"]
  }
];

const failures = [];
for (const check of checks) {
  const absolute = path.join(root, check.file);
  if (!fs.existsSync(absolute)) {
    failures.push(`${check.name}: missing ${check.file}`);
    continue;
  }
  const source = fs.readFileSync(absolute, "utf8");
  for (const expected of check.includes) {
    if (!source.includes(expected)) failures.push(`${check.name}: ${check.file} missing ${expected}`);
  }
}

const forbiddenDashboardTerms = ["MinIO", "PostgreSQL", "dead_letter", "task id", "lease"];
const dashboard = fs.readFileSync(path.join(root, "apps/web-admin/src/pages/DashboardPage.tsx"), "utf8");
for (const term of forbiddenDashboardTerms) {
  if (dashboard.includes(term)) failures.push(`role home: ordinary dashboard exposes ${term}`);
}

if (failures.length) {
  console.error("STORY-053 product shell check failed:");
  for (const failure of failures) console.error(`- ${failure}`);
  process.exit(1);
}
console.log("STORY-053 product shell check passed.");
