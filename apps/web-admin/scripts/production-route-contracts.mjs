import ts from "typescript";

function functionSource(source, name) {
  const file = ts.createSourceFile("contract.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  return file.statements.find((node) => ts.isFunctionDeclaration(node) && node.name?.text === name)?.getText(file) ?? "";
}

function deferredPages(source) {
  const file = ts.createSourceFile("pages.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const pages = file.statements.filter(ts.isVariableStatement).flatMap((node) => [...node.declarationList.declarations])
    .filter((node) => ts.isIdentifier(node.name) && node.name.text.endsWith("Page"));
  const eagerPageImport = file.statements.filter(ts.isImportDeclaration).some((node) =>
    node.importClause?.phaseModifier !== ts.SyntaxKind.TypeKeyword && ts.isStringLiteral(node.moduleSpecifier) && /\/pages\/.*Page$/.test(node.moduleSpecifier.text));
  return !eagerPageImport && pages.length > 0 && pages.every((node) => node.initializer
    && ts.isCallExpression(node.initializer) && node.initializer.expression.getText(file) === "lazy"
    && /import\(/.test(node.initializer.getText(file)));
}

// 拆分后的约束检查执行规则所在模块以及调用接线，不能只在旧父组件中搜索同名文字。
// 这些静态门禁补充行为测试，不能替代服务端鉴权或证明任意写法都安全。
export function movedProductionRouteFailures({ routes, appLayout, layoutPresentation, appShell, pageRendering, examManagement, managementOverview, gradingFeature }) {
  const failures = [];
  const check = (condition, message) => { if (!condition) failures.push(message); };
  const routeContent = functionSource(pageRendering, "renderAppRouteContent");
  const grading = functionSource(pageRendering, "renderGrading");
  const workspaceModule = functionSource(pageRendering, "renderExamWorkspaceModule");
  check(/experienceLabel\(experience\)/.test(layoutPresentation)
    && /onExperienceChange\(value as ProductExperience\)/.test(layoutPresentation)
    && /<ShellNavigation[\s\S]*?onExperienceChange=\{onExperienceChange\}/.test(appLayout)
    && /<ShellTopbar[\s\S]*?experience=\{experience\}/.test(appLayout),
  "AppLayout must identify the current product end and support explicit mixed-role switching.");
  check(/teacher:\s*\[\]/.test(routes)
    && /if \(examWorkspace && !hasExamWorkspaceSectionAccess\(experience, examWorkspace.section\)\)\s*\{\s*return <ForbiddenState/.test(routeContent),
  "Teacher identities must remain outside administrator exam workspaces for navigation and deep links.");
  check(/if \(experience === "admin"\) return <AdminGradingOperationsPage/.test(grading)
    && /return <GradingWorkbenchPage[\s\S]*?canManageTasks=\{false\}/.test(grading),
  "Administrator grading navigation must open exam-level operations instead of the personal workbench.");
  check(/<Drawer[\s\S]*?open=\{navigationOpen\}/.test(appLayout)
    && /<ShellTopbar[\s\S]*?onOpenNavigation=\{\(\) => setNavigationOpen\(true\)\}/.test(appLayout)
    && /mobile-nav-button[\s\S]*?onClick=\{onOpenNavigation\}/.test(layoutPresentation),
  "Compact viewports must use a navigation drawer.");
  check(/function loadTaskContext\(/.test(gradingFeature)
    && /originalImageUrl:\s*(?:scope\.)?allowOriginalImage\s*\?\s*artifact\.original_image_url\s*:\s*undefined/.test(gradingFeature)
    && /canViewOriginalImage=\{false\}/.test(grading)
    && /canViewOriginalImage=\{experience === "admin"\}/.test(workspaceModule)
    && /mode === "original" && \(!canViewOriginalImage/.test(gradingFeature)
    && /options=\{canViewOriginalImage/.test(gradingFeature),
  "Teacher grading must neither retain nor request the original answer-sheet image.");
  // 当前列表不提供通用阶段变更，已发布是筛选值；不能为满足旧 nextStatus 规则重新添写入口。
  check(!/\bupdateExamStatus\s*\(|\bpublishScores?\s*\(|status:\s*["']published["']/.test(`${examManagement}\n${managementOverview}`),
  "Management exam lists must not bypass score-release workflows through generic status actions.");
  check(deferredPages(pageRendering) && /renderAppRouteContent\(\{/.test(appShell)
    && /<Suspense[\s\S]*?>\{content\}<\/Suspense>/.test(appShell),
  "Production pages must be loaded on demand.");
  return failures;
}
