import { lazy, type ReactNode } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { hasAnyPermission, hasEveryPermission, type SessionUser } from "../auth/session";
import { canWorkTeacherAppeals } from "../auth/capabilities";
import { passwordMinimumForRoles } from "../auth/loginSecurity";
import { ForbiddenState, NotFoundState } from "../components/PageState";
import { invalidateExamWorkspace } from "../query/examWorkspace";
import { managementExamRoute } from "../features/exams/management/managementPresentation";
import { workspacesForExperience } from "../workspaces/registry";
import { hasExperienceAccess, type ProductExperience } from "./experience";
import { hasExamWorkspaceSectionAccess, hasRouteAccess, managementExamFromPath, notFoundRoute, type AppRoute } from "./routes";

const AppealCenterPage = lazy(() => import("../pages/QuestionAppealCenterPage").then((module) => ({ default: module.QuestionAppealCenterPage })));
const ArbitrationPage = lazy(() => import("../pages/ArbitrationPage").then((module) => ({ default: module.ArbitrationPage })));
const AuditLogPage = lazy(() => import("../pages/AuditLogPage").then((module) => ({ default: module.AuditLogPage })));
const DashboardPage = lazy(() => import("../pages/DashboardPage").then((module) => ({ default: module.DashboardPage })));
const TeacherDashboardPage = lazy(() => import("../pages/TeacherDashboardPage").then((module) => ({ default: module.TeacherDashboardPage })));
const ExamManagementPage = lazy(() => import("../pages/ExamManagementPage").then((module) => ({ default: module.ExamManagementPage })));
const ManagementExamOverviewPage = lazy(() => import("../features/exams/management/ManagementExamOverviewPage").then((module) => ({ default: module.ManagementExamOverviewPage })));
const CreateExamPage = lazy(() => import("../features/exams/create/CreateExamPage").then((module) => ({ default: module.CreateExamPage })));
const GradingWorkbenchPage = lazy(() => import("../pages/GradingWorkbenchPage").then((module) => ({ default: module.GradingWorkbenchPage })));
const AdminGradingOperationsPage = lazy(() => import("../pages/AdminGradingOperationsPage").then((module) => ({ default: module.AdminGradingOperationsPage })));
const LearningReportsPage = lazy(() => import("../pages/LearningReportsPage").then((module) => ({ default: module.LearningReportsPage })));
const SubjectiveGradingBatchPage = lazy(() => import("../pages/SubjectiveGradingBatchPage").then((module) => ({ default: module.SubjectiveGradingBatchPage })));
const OrganizationSetupPage = lazy(() => import("../pages/OrganizationSetupPage").then((module) => ({ default: module.OrganizationSetupPage })));
const StudentManagementPage = lazy(() => import("../pages/StudentManagementPage").then((module) => ({ default: module.StudentManagementPage })));
const ClassManagementPage = lazy(() => import("../features/members/classes/ClassManagementPage").then((module) => ({ default: module.ClassManagementPage })));
const TeacherManagementPage = lazy(() => import("../features/members/teachers/TeacherManagementPage").then((module) => ({ default: module.TeacherManagementPage })));
const PlatformSchoolsPage = lazy(() => import("../pages/PlatformSchoolsPage").then((module) => ({ default: module.PlatformSchoolsPage })));
const PlatformGettingStartedPage = lazy(() => import("../pages/PlatformGettingStartedPage").then((module) => ({ default: module.PlatformGettingStartedPage })));
const PlatformModelConfigPage = lazy(() => import("../pages/PlatformModelConfigPage").then((module) => ({ default: module.PlatformModelConfigPage })));
const SchoolAIChatPage = lazy(() => import("../pages/SchoolAIChatPage").then((module) => ({ default: module.SchoolAIChatPage })));
const ExamWorkspacePage = lazy(() => import("../pages/ExamWorkspacePage").then((module) => ({ default: module.ExamWorkspacePage })));
const AnswerSheetTemplatePage = lazy(() => import("../pages/AnswerSheetTemplatePage").then((module) => ({ default: module.AnswerSheetTemplatePage })));
const ExamCapturePage = lazy(() => import("../features/capture/ExamCapturePage").then((module) => ({ default: module.ExamCapturePage })));
const ExamStudentScopePage = lazy(() => import("../pages/ExamPreparationPage").then((module) => ({ default: module.ExamStudentScopePage })));
const ExamReadinessPage = lazy(() => import("../pages/ExamPreparationPage").then((module) => ({ default: module.ExamReadinessPage })));
const ModulePage = lazy(() => import("../pages/ModulePage").then((module) => ({ default: module.ModulePage })));
const PaperRubricPage = lazy(() => import("../pages/PaperRubricPage").then((module) => ({ default: module.PaperRubricPage })));
const QuestionBankPage = lazy(() => import("../features/question-bank/QuestionBankPage").then((module) => ({ default: module.QuestionBankPage })));
const QualityDashboardPage = lazy(() => import("../pages/QualityDashboardPage").then((module) => ({ default: module.QualityDashboardPage })));
const ScoreManagementPage = lazy(() => import("../pages/ScoreManagementPage").then((module) => ({ default: module.ScoreManagementPage })));
const SubmissionCapturePage = lazy(() => import("../pages/SubmissionCapturePage").then((module) => ({ default: module.SubmissionCapturePage })));
const SystemStatusPage = lazy(() => import("../pages/SystemStatusPage").then((module) => ({ default: module.SystemStatusPage })));
const SessionManagementPage = lazy(() => import("../pages/SessionManagementPage").then((module) => ({ default: module.SessionManagementPage })));
const IdentityLandingPage = lazy(() => import("../pages/IdentityLandingPage").then((module) => ({ default: module.IdentityLandingPage })));

export interface RouteRenderingContext {
  user: SessionUser;
  route: AppRoute;
  path: string;
  experience: ProductExperience;
  navigationExperience: ProductExperience;
  examWorkspace: { examId: string; section: string } | null;
  queryClient: QueryClient;
  navigate: (path: string) => void;
  logout: () => void;
}

function administratorPermissions(user: SessionUser, experience: ProductExperience) {
  return (permissions: string[]) => experience === "admin" && hasEveryPermission(user, permissions);
}

function renderDashboard({ user, experience, navigate }: Readonly<RouteRenderingContext>) {
  if (experience === "admin") return <DashboardPage user={user} onNavigate={navigate} />;
  if (experience === "teacher") return <TeacherDashboardPage user={user} onNavigate={navigate} />;
  return <IdentityLandingPage user={user} experience={experience} onNavigate={navigate} />;
}

function renderGrading({ user, experience, navigate }: Readonly<RouteRenderingContext>) {
  if (experience === "admin") return <AdminGradingOperationsPage canImport={hasEveryPermission(user, ["exam:manage", "capture:manage", "file:manage"])} onNavigate={navigate} />;
  // 教师总入口沿用任一阅卷权限；考试内工作区另按 review:work 判断，不能合并成同一组权限。
  return <GradingWorkbenchPage
          canWork={hasAnyPermission(user, ["review:manage", "review:work"])}
          canManageTasks={false}
          canViewOriginalImage={false}
          canGrade={false}
          canVerifyEvidence={false}
          canReturn={hasEveryPermission(user, ["review:work"])}
          currentUserId={user.id} currentTenantId={user.tenant}
          personalScope
        />;
}

function renderExamWorkspaceModule(context: Readonly<RouteRenderingContext>, readOnly: boolean): ReactNode {
  const { user, experience, examWorkspace, queryClient, navigate } = context;
  if (!examWorkspace) return undefined;
  const adminPermission = administratorPermissions(user, experience);
  const adminAllows = (permissions: string[]) => !readOnly && adminPermission(permissions);
  const writeAllows = (permissions: string[]) => !readOnly && hasEveryPermission(user, permissions);
    const examId = examWorkspace.examId;
    const refreshWorkspace = () => void invalidateExamWorkspace(queryClient, examId);
    switch (examWorkspace.section) {
      case "students":
        return <ExamStudentScopePage examId={examId} canManage={writeAllows(["exam:manage", "org:manage"])} onExamChanged={refreshWorkspace} />;
      case "paper":
      case "questions":
        return <PaperRubricPage canManage={writeAllows(["exam:manage", "file:manage"])} canReadQuestionBank={hasEveryPermission(user,["question_bank:read"])} canImportQuestionBank={writeAllows(["question_bank:read","question_bank:create","question_bank:edit"])} questionBankScope={`${user.tenant}:${user.id}`} canManageAssessment={adminAllows(["exam:manage"])} fixedExamId={examId} mode="embedded" initialView={examWorkspace.section === "questions" ? "questions" : "materials"} onExamChanged={refreshWorkspace} onNavigate={navigate} />;
      case "template":
        return <AnswerSheetTemplatePage key={`${user.tenant}:${user.id}:${examId}`} scopeKey={`${user.tenant}:${user.id}`} examId={examId} canManage={adminAllows(["exam:manage", "file:manage"])} canCalibrate={adminAllows(["grading:manage"])} onExamChanged={refreshWorkspace} />;
      case "settings":
        return <ExamReadinessPage examId={examId} canManage={writeAllows(["exam:manage"])} canManageAssessment={adminAllows(["exam:manage"])} onNavigate={navigate} onExamChanged={refreshWorkspace} />;
      case "capture":
        return <ExamCapturePage key={`${user.tenant}:${user.id}:${examId}`} user={user} examId={examId} canManage={writeAllows(["capture:manage", "file:manage"])} canStart={writeAllows(["exam:manage"])} canReview={writeAllows(["review:manage"])} onNavigate={navigate} />;
      case "processing":
        return <SubmissionCapturePage canManage={writeAllows(["submission:manage", "file:manage", "ocr:manage", "segment:manage"])} canManageProcessing={writeAllows(["capture:manage"])} canReadStudentNames={hasEveryPermission(user, ["org:manage"])} initialExamId={examId} onNavigate={navigate} />;
      case "grading":
        return <GradingWorkbenchPage canWork={writeAllows(["review:work"])} canManageTasks={adminAllows(["review:manage"])} canViewOriginalImage={experience === "admin"} canGrade={adminAllows(["grading:manage"])} canVerifyEvidence={adminAllows(["evidence:manage"])} canReturn={writeAllows(["review:work"])} currentUserId={user.id} currentTenantId={user.tenant} initialExamId={examId} personalScope={experience === "teacher"} />;
      case "quality":
        return <QualityDashboardPage examId={examId} canManage={adminAllows(["review:manage"])} currentUserId={user.id} onNavigate={navigate} />;
      case "scores":
        return <ScoreManagementPage mode={experience} canManage={adminAllows(["score:manage", "exam:manage", "submission:manage"])} canReadStudentNames={adminPermission(["org:manage"])} canReadAudit={adminPermission(["audit:read"])} initialExamId={examId} onNavigate={navigate} canHandleCapture={hasEveryPermission(user,["capture:manage","file:manage"])} canHandleReview={hasAnyPermission(user,["review:work","review:manage"])} canHandleArbitration={hasAnyPermission(user,["arbitration:work","arbitration:manage"])} />;
      case "appeals":
        return <AppealCenterPage key={`${user.tenant}:${user.id}:${examId}`} mode={experience} canRead={hasEveryPermission(user, ["appeal:read"])} canManage={adminAllows(["appeal:manage"])} canWork={!readOnly && canWorkTeacherAppeals(user, experience)} canReadQuestionAppeals={adminPermission(["appeal:manage"]) || canWorkTeacherAppeals(user, experience)} canManageScores={adminAllows(["score:manage"])} canReadIdentities={adminPermission(["org:manage"])} canReadExams={hasEveryPermission(user, ["exam:manage"])} currentUser={user} initialExamId={examId} onNavigate={navigate} />;
      case "reports":
        return <LearningReportsPage canRead={hasEveryPermission(user, ["report:read"])} canExport={hasEveryPermission(user, ["report:export"])} initialExamId={examId} />;
      default:
        return undefined;
    }
}

function forbiddenRouteScope(examWorkspace: RouteRenderingContext["examWorkspace"], platformOnly: boolean, platformDuty: boolean | undefined): "exam" | "platform" | "school" {
  if (examWorkspace) return "exam";
  if (platformOnly || platformDuty) return "platform";
  return "school";
}

export function renderAppRouteContent(context: Readonly<RouteRenderingContext>): ReactNode {
  const { user, route, path, experience, navigationExperience, examWorkspace, navigate, logout } = context;
  // 未知地址先显示未找到；有效深链接还要检查产品身份、路由和考试分区，菜单可见性不能代替此边界。
  if (route === notFoundRoute) return <NotFoundState onBack={() => navigate("/dashboard")} />;
  if (!hasExperienceAccess(user, experience) || !hasRouteAccess(user, route, experience)) {
    // 平台旧地址不能靠前缀识别；共用审计入口按当前工作区职责说明权限申请对象。
    const platformOnly = Boolean(route.workspaces?.length && route.workspaces.every(workspace => workspace === "platform"))
      || Boolean(route.allowedRoles?.length && route.allowedRoles.every(role => role === "platform_admin"));
    const platformDuty = route.workspaces?.includes("platform") && workspacesForExperience(user, experience).includes("platform");
    return <ForbiddenState scope={forbiddenRouteScope(examWorkspace, platformOnly, platformDuty)} feature={route.title} onBack={() => navigate("/dashboard")} />;
  }
  if (examWorkspace && !hasExamWorkspaceSectionAccess(experience, examWorkspace.section)) {
    return <ForbiddenState scope="exam" feature={route.title} onBack={() => navigate("/dashboard")} />;
  }
  if (route.path === "/dashboard") return renderDashboard(context);
  const managementExam = managementExamFromPath(path);
  if (managementExam) return <ManagementExamOverviewPage key={`${user.tenant}:${user.id}:${managementExam.kind}:${managementExam.examId}`} kind={managementExam.kind} examId={managementExam.examId} onNavigate={navigate} />;
  if (examWorkspace) {
    // 归档事实来自工作台投影，不能在数据返回前只凭管理员权限打开写入口。
    return <ExamWorkspacePage examId={examWorkspace.examId} section={examWorkspace.section} experience={experience} currentUser={user} moduleContent={(readOnly: boolean) => renderExamWorkspaceModule(context, readOnly)} onNavigate={navigate} />;
  }
  const adminAllows = administratorPermissions(user, experience);
  switch (route.path) {
    case "/exams/new":
      return <CreateExamPage user={user} onNavigate={navigate} />;
    case "/exams":
      return <ExamManagementPage
        key={`${user.tenant}:${user.id}:${path}`}
        mode={experience}
        canManage={adminAllows(["exam:manage"])}
        currentUser={user}
        onOpenWorkspace={(exam) => navigate(managementExamRoute(exam))}
        onCreateExam={() => navigate("/exams/new")}
      />;
    case "/organization/setup":
      return <OrganizationSetupPage onNavigate={navigate} />;
    case "/members/students":
      return <StudentManagementPage />;
    case "/members/classes":
      return <ClassManagementPage />;
    case "/members/teachers":
      return <TeacherManagementPage currentUser={user} />;
    case "/platform/schools":
      return <PlatformSchoolsPage />;
    case "/platform/getting-started":
      return <PlatformGettingStartedPage onNavigate={navigate} />;
    case "/platform/model-config":
      return <PlatformModelConfigPage />;
    case "/ai-chat":
      return <SchoolAIChatPage key={`${user.tenant}:${user.id}`} user={user} />;
    case "/papers":
      return <PaperRubricPage canManage={hasEveryPermission(user, ["exam:manage", "file:manage"])} canReadQuestionBank={hasEveryPermission(user,["question_bank:read"])} canImportQuestionBank={hasEveryPermission(user,["question_bank:read","question_bank:create","question_bank:edit"])} questionBankScope={`${user.tenant}:${user.id}`} canManageAssessment={adminAllows(["exam:manage"])} />;
    case "/question-bank":
      return <QuestionBankPage key={`${user.tenant}:${user.id}:${path}`} user={user} />;
    case "/capture":
      return <SubmissionCapturePage
        onNavigate={navigate}
        canManage={hasEveryPermission(user, ["submission:manage", "file:manage", "ocr:manage", "segment:manage"])}
        canManageProcessing={hasEveryPermission(user, ["capture:manage"])}
        canReadStudentNames={hasEveryPermission(user, ["org:manage"])}
      />;
    case "/grading":
      return renderGrading(context);
    case "/arbitration":
      return <ArbitrationPage
        initialExamId={new URLSearchParams(path.split("?",2)[1]??"").get("exam")??""}
        canAssign={adminAllows(["arbitration:manage"])}
        canWork={hasAnyPermission(user, ["arbitration:manage", "arbitration:work"])}
        canReadAudit={adminAllows(["audit:read"])}
        canReadExams={hasEveryPermission(user, ["exam:manage"])}
        currentUser={user}
        personalScope={experience === "teacher"}
      />;
    case "/scores":
      return <ScoreManagementPage
        mode={experience}
        canManage={adminAllows(["score:manage", "exam:manage", "submission:manage"])}
        onNavigate={navigate}
        canHandleCapture={hasEveryPermission(user,["capture:manage","file:manage"])}
        canHandleReview={hasAnyPermission(user,["review:work","review:manage"])}
        canHandleArbitration={hasAnyPermission(user,["arbitration:work","arbitration:manage"])}
        canReadStudentNames={adminAllows(["org:manage"])}
        canReadAudit={adminAllows(["audit:read"])}
      />;
    case "/reports":
      return <LearningReportsPage canRead={hasEveryPermission(user, ["report:read"])} canExport={hasEveryPermission(user, ["report:export"])} />;
    case "/appeals":
      return <AppealCenterPage
        key={`${user.tenant}:${user.id}`}
        mode={experience}
        canRead={hasEveryPermission(user, ["appeal:read"])}
        canManage={adminAllows(["appeal:manage"])}
        canWork={canWorkTeacherAppeals(user, experience)}
        canReadQuestionAppeals={adminAllows(["appeal:manage"]) || canWorkTeacherAppeals(user, experience)}
        canManageScores={adminAllows(["score:manage"])}
        canReadIdentities={adminAllows(["org:manage"])}
        canReadExams={hasEveryPermission(user, ["exam:manage"])}
        currentUser={user}
        onNavigate={navigate}
      />;
    case "/audit":
      return <AuditLogPage canRead={hasEveryPermission(user, ["audit:read"])} canExport={hasEveryPermission(user, ["audit:export"])} tenantName={user.tenant} />;
    case "/system/status":
      return <SystemStatusPage />;
    case "/system/models":
      return <PlatformModelConfigPage />;
    case "/account/sessions":
      return <SessionManagementPage key={`${user.tenant}:${user.id}`} onLoggedOut={logout} accountLabel={`${user.tenant}/${user.username}`} minimumPasswordLength={passwordMinimumForRoles(user.roles)} />;
    case "/grading/subjective-batches":
      return <SubjectiveGradingBatchPage key={`${user.tenant}:${user.id}`} scopeKey={`${user.tenant}:${user.id}`} />;
    default:
      return <ModulePage route={route} experience={navigationExperience} onNavigate={navigate} />;
  }
}
