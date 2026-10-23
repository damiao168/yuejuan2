import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { movedProductionRouteFailures, personalGradingQueueFailures } from "./production-route-contracts.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const read = (path) => readFileSync(join(root, path), "utf8");
function sourceFiles(directory) {
  return readdirSync(join(root, directory), { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? sourceFiles(path) : /\.(ts|tsx)$/.test(entry.name) && !/\.(test|spec)\./.test(entry.name) ? [read(path)] : [];
  });
}
const source = {
  routes: read("src/router/routes.tsx"), appLayout: read("src/components/AppLayout.tsx"),
  layoutPresentation: read("src/components/appLayoutPresentation.tsx"), appShell: read("src/AppShell.tsx"),
  routeStaging: read("src/router/RouteContent.tsx"),
  pageRendering: read("src/router/pageRendering.tsx"), examManagement: read("src/pages/ExamManagementPage.tsx"),
  managementOverview: read("src/features/exams/management/ManagementExamOverviewPage.tsx"),
  gradingFeature: sourceFiles("src/features/grading/workbench").join("\n")
};

test("the split production modules retain their connected contracts", () => {
  assert.deepEqual(movedProductionRouteFailures(source), []);
});

// 每次破坏一处真实接线，验证修正文件归属后门禁仍会失败，而非仅因找到组件名就放行。
const mutations = [
  ["experience switching", "layoutPresentation", "onExperienceChange(value as ProductExperience)", "onNavigate('/dashboard')", "explicit mixed-role switching"],
  ["workspace deep-link permission", "pageRendering", "!hasExamWorkspaceSectionAccess(experience, examWorkspace.section)", "false", "navigation and deep links"],
  ["administrator grading branch", "pageRendering", 'if (experience === "admin") return <AdminGradingOperationsPage', 'if (false) return <AdminGradingOperationsPage', "exam-level operations"],
  ["mobile drawer trigger", "layoutPresentation", "onClick={onOpenNavigation}", "onClick={() => undefined}", "navigation drawer"],
  ["teacher original image permission", "pageRendering", "canViewOriginalImage={false}", "canViewOriginalImage={true}", "original answer-sheet image"],
  ["original image retention", "gradingFeature", /originalImageUrl:\s*(?:scope\.)?allowOriginalImage\s*\?\s*artifact\.original_image_url\s*:\s*undefined/, "originalImageUrl: artifact.original_image_url", "original answer-sheet image"],
  ["original image selector permission", "gradingFeature", '...(canViewOriginalImage ? [{ label: "原图", value: "original" as const }] : [])', '...([{ label: "原图", value: "original" as const }])', "original answer-sheet image"],
  ["eager page import", "pageRendering", "import { lazy, type ReactNode }", 'import { GradingWorkbenchPage as EagerPage } from "../pages/GradingWorkbenchPage";\nimport { lazy, type ReactNode }', "loaded on demand"],
  ["staged route content", "appShell", /\{content\}\s*<\/RouteContent>/, "{null}</RouteContent>", "loaded on demand"],
  ["staging scope identity", "routeStaging", "<StagedRouteContent key={scopeKey}", "<StagedRouteContent key=\"global\"", "loaded on demand"],
  ["suspense boundary", "routeStaging", "{slot.children}</Suspense>", "{null}</Suspense>", "loaded on demand"]
];
for (const [name, key, before, after, message] of mutations) {
  test(`rejects removal of ${name}`, () => {
    const changed = source[key].replace(before, after);
    assert.notEqual(changed, source[key], "Fixture mutation must touch the actual implementation.");
    assert.ok(movedProductionRouteFailures({ ...source, [key]: changed }).some((failure) => failure.includes(message)));
  });
}

test("rejects adding generic score publication to the management list", () => {
  assert.ok(movedProductionRouteFailures({ ...source, examManagement: `${source.examManagement}\nupdateExamStatus('exam', 'published', 1);` })
    .some((failure) => failure.includes("score-release workflows")));
});

const queueSources = {
  grading: read("src/features/grading/workbench/GradingWorkbench.tsx"),
  gradingQueue: read("src/features/grading/workbench/hooks/useGradingQueue.ts"),
  gradingQueuePages: read("src/features/grading/workbench/hooks/useGradingQueuePages.ts"),
};
test("split queue pages preserve personal scope and complete reviewer progress", () => {
  assert.deepEqual(personalGradingQueueFailures(queueSources), []);
});
for (const [name, key, before, after] of [
  ["personal assignment", "gradingQueue", "assigned_to: currentUserId", "assigned_to: undefined"],
  ["scope forwarding", "gradingQueue", /filterQuery, scopeQuery\s*\}\);/, "filterQuery\n    });"],
  ["pagination scope", "gradingQueuePages", "listReviewTasks({ ...filterQuery, limit: 50, cursor })", "listReviewTasks({ limit: 50, cursor })"],
  ["complete progress scope", "gradingQueuePages", /listReviewTasks\(\{ \.\.\.scopeQuery, limit: 1 \}\)/g, "listReviewTasks({ ...filterQuery, limit: 1 })"],
]) {
  test(`rejects removal of queue ${name}`, () => {
    const changed = queueSources[key].replace(before, after);
    assert.notEqual(changed, queueSources[key], "Fixture mutation must touch the actual implementation.");
    assert.ok(personalGradingQueueFailures({ ...queueSources, [key]: changed }).length > 0);
  });
}
