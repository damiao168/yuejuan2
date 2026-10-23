import fs from "node:fs";
import path from "node:path";
import { runWebContractTests } from "./ci/run-web-contract-tests.mjs";

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
    // 人员直建按目标角色校验长度；此处检查实际调用，不能继续要求统一管理员密码规则。
    includes: ["decodeAuthJSON(w, r, &input, true)", "PasswordMeetsMinimum(input.Password, PasswordMinimum(", "auth.user_created", "role is not assignable"]
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
    // 考试入口已统一为准备步骤与学生答卷；实际导航和完成状态由下方组件测试验证。
    includes: ["data.exam_id", "preparationNavigationSteps", "preparationSteps", "exam-preparation-nav", "考试准备与答卷导入", "学生答卷", "onNavigate"]
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
runWebContractTests(["src/features/exams/workspace/ExamWorkspaceLayout.test.tsx"]);
console.log("STORY-053 product shell check passed.");
