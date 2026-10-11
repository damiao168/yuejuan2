import { isValidElement, type ReactNode } from "react";
import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../auth/session";
import { ForbiddenState, NotFoundState } from "../components/PageState";
import type { ProductExperience } from "./experience";
import { examWorkspaceFromPath, notFoundRoute, routeFromPath } from "./routes";
import { renderAppRouteContent, type RouteRenderingContext } from "./pageRendering";

function user(roles: string[], permissions: string[]): SessionUser {
  return {
    id: "user-1", username: "tester", displayName: "Tester", name: "Tester", role: roles[0] ?? "user", roles,
    tenant: "school-1", school: "School 1", currentExam: "", publicComputer: false, permissions,
    organizationScope: { resolved: false, tenantWide: false, schoolIds: [], gradeIds: [], classIds: [] }
  };
}

function context(path: string, sessionUser: SessionUser, experience: ProductExperience = "admin"): RouteRenderingContext {
  return {
    user: sessionUser, route: routeFromPath(path), path, experience, navigationExperience: experience,
    examWorkspace: examWorkspaceFromPath(path), queryClient: new QueryClient(), navigate: vi.fn(), logout: vi.fn()
  };
}

function element(node: ReactNode) {
  if (!isValidElement<Record<string, unknown>>(node)) throw new Error("Expected a page element");
  return node;
}

function workspaceModule(renderContext: RouteRenderingContext, readOnly = false) {
  const page = element(renderAppRouteContent(renderContext));
  const moduleContent = page.props.moduleContent as ((readOnly: boolean) => ReactNode);
  return element(moduleContent(readOnly));
}

describe("app route rendering access", () => {
  it.each(["/system/models", "/system/status"])("低权限打开平台别名%s仍说明平台权限申请范围", (path) => {
    const renderContext = context(path, user(["school_admin"], []));
    const page = element(renderAppRouteContent(renderContext));
    expect(page.type).toBe(ForbiddenState);
    expect(page.props).toMatchObject({ scope: "platform", feature: renderContext.route.title });
    (page.props.onBack as () => void)();
    expect(renderContext.navigate).toHaveBeenCalledWith("/dashboard");
  });

  it("共用审计入口按当前平台或学校职责给出申请范围", () => {
    for (const [role, scope] of [["platform_admin", "platform"], ["school_admin", "school"]]) {
      const page = element(renderAppRouteContent(context("/audit", user([role], []))));
      expect(page.type).toBe(ForbiddenState);
      expect(page.props.scope).toBe(scope);
    }
  });
  it("returns not-found before checking the requested identity", () => {
    const renderContext = context("/missing", user(["grader"], ["review:work"]));
    expect(renderContext.route).toBe(notFoundRoute);
    const page = element(renderAppRouteContent(renderContext));
    expect(page.type).toBe(NotFoundState);
    (page.props.onBack as () => void)();
    expect(renderContext.navigate).toHaveBeenCalledWith("/dashboard");
  });

  it.each([
    ["/exams", ["grader"], ["exam:manage"], "admin"],
    ["/exams", ["school_admin"], [], "admin"],
    ["/platform/schools", ["school_admin"], ["tenant:manage"], "admin"],
    ["/exams/exam-1/not-a-section", ["school_admin"], ["exam:manage"], "admin"],
    ["/exams/exam-1/grading", ["school_admin", "grader"], ["exam:manage", "review:work"], "teacher"]
  ] satisfies [string, string[], string[], ProductExperience][])("guards the deep link %s", (path, roles, permissions, experience) => {
    const page = element(renderAppRouteContent(context(path, user(roles, permissions), experience)));
    expect(page.type).toBe(ForbiddenState);
  });

  it("selects a lazy dashboard for each permitted identity without loading any page module", () => {
    const administrator = context("/dashboard", user(["school_admin"], []));
    const teacher = context("/dashboard", user(["teacher"], []), "teacher");
    const auditor = context("/dashboard", user(["auditor"], []), "auditor");
    const adminPage = element(renderAppRouteContent(administrator));
    const teacherPage = element(renderAppRouteContent(teacher));
    const auditorPage = element(renderAppRouteContent(auditor));
    for (const page of [adminPage, teacherPage, auditorPage]) {
      expect(page.type).toEqual(expect.objectContaining({ $$typeof: Symbol.for("react.lazy") }));
      expect(page.key).toBeNull();
    }
    expect(adminPage.type).not.toBe(teacherPage.type);
    expect(auditorPage.type).not.toBe(adminPage.type);
    expect(adminPage.props).toMatchObject({ user: administrator.user, onNavigate: administrator.navigate });
    expect(auditorPage.props.experience).toBe("auditor");
  });
});

describe("route permission props and identity keys", () => {
  it("采集上传权限不替代异常处理权限，两种入口分别传 capture:manage", () => {
    const uploadOnly = user(["school_admin"], ["exam:manage", "submission:manage", "file:manage", "ocr:manage", "segment:manage"]);
    const captureEnabled = { ...uploadOnly, permissions: [...uploadOnly.permissions, "capture:manage"] };
    for (const path of ["/capture", "/exams/exam-1/processing"]) {
      const page = path === "/capture" ? element(renderAppRouteContent(context(path, uploadOnly))) : workspaceModule(context(path, uploadOnly));
      expect(page.props).toMatchObject({ canManage: true, canManageProcessing: false });
      const permitted = path === "/capture" ? element(renderAppRouteContent(context(path, captureEnabled))) : workspaceModule(context(path, captureEnabled));
      expect(permitted.props.canManageProcessing).toBe(true);
    }
    expect(workspaceModule(context("/exams/exam-1/processing", captureEnabled), true).props.canManageProcessing).toBe(false);
  });
  it("keeps review management sufficient for the teacher grading entry but reserves return for review work", () => {
    const renderContext = context("/grading", user(["grader"], ["review:manage"]), "teacher");
    expect(element(renderAppRouteContent(renderContext)).props).toMatchObject({
      canWork: true, canManageTasks: false, canViewOriginalImage: false, canGrade: false,
      canVerifyEvidence: false, canReturn: false, personalScope: true,
      currentUserId: "user-1", currentTenantId: "school-1"
    });
  });

  it("requires every import permission for the administrator grading entry", () => {
    const administrator = user(["school_admin"], ["review:manage", "exam:manage", "capture:manage"]);
    expect(element(renderAppRouteContent(context("/grading", administrator))).props.canImport).toBe(false);
    expect(element(renderAppRouteContent(context("/grading", { ...administrator, permissions: [...administrator.permissions, "file:manage"] }))).props.canImport).toBe(true);
  });

  it("keeps teacher arbitration and appeal views free of administrator permissions even for a dual-role account", () => {
    const dualRole = user(["school_admin", "teacher", "arbitrator"], ["arbitration:manage", "audit:read", "exam:manage", "appeal:read", "appeal:manage", "appeal:work", "org:manage"]);
    expect(element(renderAppRouteContent(context("/arbitration", dualRole, "teacher"))).props).toMatchObject({
      canAssign: false, canWork: true, canReadAudit: false, canReadExams: true, personalScope: true
    });
    expect(element(renderAppRouteContent(context("/appeals", dualRole, "teacher"))).props).toMatchObject({
      canRead: true, canManage: false, canWork: true, canReadIdentities: false, canReadExams: true
    });
  });

  it("keeps workspace grading work and management as separate permissions", () => {
    const renderContext = context("/exams/exam-1/grading", user(["school_admin"], ["exam:manage", "review:manage"]));
    const page = element(renderAppRouteContent(renderContext));
    expect(page.props).toMatchObject({ examId: "exam-1", section: "grading", experience: "admin", currentUser: renderContext.user });
    expect(workspaceModule(renderContext).props).toMatchObject({
      canWork: false, canManageTasks: true, canViewOriginalImage: true, canGrade: false,
      canVerifyEvidence: false, canReturn: false, initialExamId: "exam-1", personalScope: false
    });
    expect(page.key).toBeNull();
  });

  it.each(["template", "capture"])("preserves the account and exam key for the %s module", (section) => {
    const renderContext = context(`/exams/exam%201/${section}`, user(["school_admin"], ["exam:manage"]));
    expect(workspaceModule(renderContext).key).toBe("school-1:user-1:exam 1");
  });

  it("keeps paper and question views distinct and refreshes only the current exam workspace", () => {
    const administrator = user(["school_admin"], ["exam:manage", "file:manage", "question_bank:read"]);
    const paperContext = context("/exams/exam-1/paper", administrator);
    const questionContext = context("/exams/exam-1/questions", administrator);
    const paper = workspaceModule(paperContext);
    const questions = workspaceModule(questionContext);
    const invalidation = vi.spyOn(paperContext.queryClient, "invalidateQueries");
    expect(paper.type).toBe(questions.type);
    expect(paper.props).toMatchObject({ initialView: "materials", fixedExamId: "exam-1", mode: "embedded", questionBankScope: "school-1:user-1", canReadQuestionBank: true, canImportQuestionBank: false });
    expect(questions.props.initialView).toBe("questions");
    (paper.props.onExamChanged as () => void)();
    expect(invalidation).toHaveBeenCalledWith({ queryKey: ["exam-workspace", "exam-1"] });
  });

  it.each(["/account/sessions", "/question-bank", "/ai-chat", "/grading/subjective-batches"])("preserves the account key for %s", (path) => {
    const renderContext = context(path, user(["school_admin"], ["question_bank:read", "grading:manage"]));
    const page = element(renderAppRouteContent(renderContext));
    expect(page.key).toBe(path === "/question-bank" ? `school-1:user-1:${path}` : "school-1:user-1");
    if (path === "/account/sessions") {
      expect(page.props.accountLabel).toBe("school-1/tester");
      expect(page.props.onLoggedOut).toBe(renderContext.logout);
    }
  });

  it("different question-bank draft deep links receive different page keys", () => {
    const actor = user(["school_admin"], ["question_bank:read"]);
    const first = element(renderAppRouteContent(context("/question-bank?bank=bank-a&item=item-a&version=draft-a", actor)));
    const second = element(renderAppRouteContent(context("/question-bank?bank=bank-a&item=item-b&version=draft-b", actor)));
    expect(first.key).toBe("school-1:user-1:/question-bank?bank=bank-a&item=item-a&version=draft-a");
    expect(second.key).not.toBe(first.key);
  });

  it("keeps the account and full path as the exam-list key and forwards the create navigation", () => {
    const renderContext = context("/exams?filter=draft", user(["school_admin"], ["exam:manage"]));
    const page = element(renderAppRouteContent(renderContext));
    expect(page.key).toBe("school-1:user-1:/exams?filter=draft");
    (page.props.onCreateExam as () => void)();
    expect(renderContext.navigate).toHaveBeenCalledWith("/exams/new");
  });

  it("考试列表打开整场总览，不代选某一学科工作台", () => {
    const renderContext = context("/exams", user(["school_admin"], ["exam:manage"]));
    const page = element(renderAppRouteContent(renderContext));
    (page.props.onOpenWorkspace as (exam: { kind: string; id: string }) => void)({ kind: "session", id: "session 1" });
    expect(renderContext.navigate).toHaveBeenCalledWith("/exams/overview/session/session%201");
    const overview = element(renderAppRouteContent(context("/exams/overview/session/session%201", renderContext.user)));
    expect(overview.props).toMatchObject({ kind: "session", examId: "session 1" });
    expect(overview.key).toBe("school-1:user-1:session:session 1");
  });

  it("归档投影关闭各分区写权限，保留成绩和审计读取", () => {
    const administrator = user(["school_admin"], ["exam:manage", "org:manage", "score:manage", "submission:manage", "review:work", "review:manage", "grading:manage", "evidence:manage", "file:manage", "audit:read"]);
    expect(workspaceModule(context("/exams/exam-1/students", administrator), true).props.canManage).toBe(false);
    expect(workspaceModule(context("/exams/exam-1/paper", administrator), true).props).toMatchObject({ canManage: false, canManageAssessment: false, canImportQuestionBank: false });
    expect(workspaceModule(context("/exams/exam-1/grading", administrator), true).props).toMatchObject({ canWork: false, canManageTasks: false, canGrade: false, canVerifyEvidence: false, canReturn: false, canViewOriginalImage: true });
    expect(workspaceModule(context("/exams/exam-1/scores", administrator), true).props).toMatchObject({ canManage: false, canReadStudentNames: true, canReadAudit: true });
  });
});
