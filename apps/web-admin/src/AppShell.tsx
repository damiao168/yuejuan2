import { PendingBusinessCommands } from "./components/PendingBusinessCommands";
import { setBusinessCommandScope } from "./api/businessCommand";
import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useRouter, useRouterState } from "@tanstack/react-router";
import { App as AntApp, ConfigProvider } from "antd";
import "antd/dist/reset.css";
import { getCurrentUser, lockCurrentSession, login as loginWithPassword, logout as logoutSession, reauthenticate as reauthenticateSession } from "./api/auth";
import { listSchools } from "./api/org";
import { ApiClientError } from "./api/client";
import { hasAnyPermission, hasEveryPermission, sessionFromAuthUser, type SessionUser } from "./auth/session";
import { canWorkTeacherAppeals } from "./auth/capabilities";
import { clearAllReviewDraftFallbacks, clearReviewDraftFallbacks } from "./auth/reviewDraftFallback";
import { clearLegacyRememberedLogin, clearPublicComputerData } from "./auth/loginSecurity";
import { StepUpProvider } from "./auth/stepUpContext";
import { AppLayout } from "./components/AppLayout";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ForbiddenState, LoadingState, NotFoundState } from "./components/PageState";
import { LoginPage } from "./pages/LoginPage";
import { ActivationPage } from "./pages/ActivationPage";
import { RecoveryPage } from "./pages/RecoveryPage";
import { examWorkspaceFromPath, hasExamWorkspaceSectionAccess, hasRouteAccess, notFoundRoute, routeFromPath } from "./router/routes";
import {
  availableExperiences,
  canonicalPathFromPath,
  defaultExperience,
  experienceFromPath,
  hasExperienceAccess,
  pathForExperience,
  type ProductExperience
} from "./router/experience";
import type { LoginFormValues } from "./pages/LoginPage";
import { invalidateExamWorkspace } from "./query/examWorkspace";
import { legacyRedirectForPath, normalizeEntryPath } from "./router/navigation";
import { OnboardingGate } from "./features/onboarding/OnboardingGate";

const AppealCenterPage = lazy(() => import("./pages/AppealCenterPage").then((module) => ({ default: module.AppealCenterPage })));
const ArbitrationPage = lazy(() => import("./pages/ArbitrationPage").then((module) => ({ default: module.ArbitrationPage })));
const AuditLogPage = lazy(() => import("./pages/AuditLogPage").then((module) => ({ default: module.AuditLogPage })));
const DashboardPage = lazy(() => import("./pages/DashboardPage").then((module) => ({ default: module.DashboardPage })));
const TeacherDashboardPage = lazy(() => import("./pages/TeacherDashboardPage").then((module) => ({ default: module.TeacherDashboardPage })));
const ExamManagementPage = lazy(() => import("./pages/ExamManagementPage").then((module) => ({ default: module.ExamManagementPage })));
const CreateExamPage = lazy(() => import("./features/exams/create/CreateExamPage").then((module) => ({ default: module.CreateExamPage })));
const GradingWorkbenchPage = lazy(() => import("./pages/GradingWorkbenchPage").then((module) => ({ default: module.GradingWorkbenchPage })));
const AdminGradingOperationsPage = lazy(() => import("./pages/AdminGradingOperationsPage").then((module) => ({ default: module.AdminGradingOperationsPage })));
const LearningReportsPage = lazy(() => import("./pages/LearningReportsPage").then((module) => ({ default: module.LearningReportsPage })));
const ModelGovernancePage = lazy(() => import("./pages/ModelGovernancePage").then((module) => ({ default: module.ModelGovernancePage })));
const SubjectiveGradingBatchPage = lazy(() => import("./pages/SubjectiveGradingBatchPage").then((module) => ({ default: module.SubjectiveGradingBatchPage })));
const OrganizationSetupPage = lazy(() => import("./pages/OrganizationSetupPage").then((module) => ({ default: module.OrganizationSetupPage })));
const StudentManagementPage = lazy(() => import("./pages/StudentManagementPage").then((module) => ({ default: module.StudentManagementPage })));
const ClassManagementPage = lazy(() => import("./features/members/classes/ClassManagementPage").then((module) => ({ default: module.ClassManagementPage })));
const TeacherManagementPage = lazy(() => import("./features/members/teachers/TeacherManagementPage").then((module) => ({ default: module.TeacherManagementPage })));
const PlatformSchoolsPage = lazy(() => import("./pages/PlatformSchoolsPage").then((module) => ({ default: module.PlatformSchoolsPage })));
const PlatformGettingStartedPage = lazy(() => import("./pages/PlatformGettingStartedPage").then((module) => ({ default: module.PlatformGettingStartedPage })));
const PlatformModelConfigPage = lazy(() => import("./pages/PlatformModelConfigPage").then((module) => ({ default: module.PlatformModelConfigPage })));
const ExamWorkspacePage = lazy(() => import("./pages/ExamWorkspacePage").then((module) => ({ default: module.ExamWorkspacePage })));
const AnswerSheetTemplatePage = lazy(() => import("./pages/AnswerSheetTemplatePage").then((module) => ({ default: module.AnswerSheetTemplatePage })));
const CaptureBatchPage = lazy(() => import("./pages/CaptureBatchPage").then((module) => ({ default: module.CaptureBatchPage })));
const ExamStudentScopePage = lazy(() => import("./pages/ExamPreparationPage").then((module) => ({ default: module.ExamStudentScopePage })));
const ExamReadinessPage = lazy(() => import("./pages/ExamPreparationPage").then((module) => ({ default: module.ExamReadinessPage })));
const ModulePage = lazy(() => import("./pages/ModulePage").then((module) => ({ default: module.ModulePage })));
const PaperRubricPage = lazy(() => import("./pages/PaperRubricPage").then((module) => ({ default: module.PaperRubricPage })));
const QuestionBankPage = lazy(() => import("./features/question-bank/QuestionBankPage").then((module) => ({ default: module.QuestionBankPage })));
const QualityDashboardPage = lazy(() => import("./pages/QualityDashboardPage").then((module) => ({ default: module.QualityDashboardPage })));
const ScoreManagementPage = lazy(() => import("./pages/ScoreManagementPage").then((module) => ({ default: module.ScoreManagementPage })));
const SubmissionCapturePage = lazy(() => import("./pages/SubmissionCapturePage").then((module) => ({ default: module.SubmissionCapturePage })));
const SystemStatusPage = lazy(() => import("./pages/SystemStatusPage").then((module) => ({ default: module.SystemStatusPage })));
const SessionManagementPage = lazy(() => import("./pages/SessionManagementPage").then((module) => ({ default: module.SessionManagementPage })));
const IdentityLandingPage = lazy(() => import("./pages/IdentityLandingPage").then((module) => ({ default: module.IdentityLandingPage })));

export function AppShell() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [user, setUser] = useState<SessionUser | null>(null);
  setBusinessCommandScope(user?.tenant ?? "", user?.id ?? "");
  const [authLoading, setAuthLoading] = useState(true);
  const [loginLoading, setLoginLoading] = useState(false);
  const [loginError, setLoginError] = useState<string | undefined>();
  const routerPath = useRouterState({
    select: (state) => `${state.location.pathname}${state.location.searchStr}`
  });
  const path = normalizeEntryPath(routerPath);

  useEffect(() => {
    if (!user || !user.permissions.includes("org:manage") || user.school !== user.tenant) {
      return;
    }
    let active = true;
    listSchools()
      .then((response) => {
        const school = response.schools[0];
        if (active && school) {
          setUser((current) => current?.id === user.id ? { ...current, school: school.name } : current);
        }
      })
      .catch(() => undefined);
    return () => { active = false; };
  }, [user]);

  useEffect(() => {
    let active = true;
    clearLegacyRememberedLogin();
    setAuthLoading(true);
    getCurrentUser()
      .then((response) => {
        if (active) {
          setUser(sessionFromAuthUser(response.user));
        }
      })
      .catch(() => {
        if (active) {
          clearAllReviewDraftFallbacks();
          setUser(null);
        }
      })
      .finally(() => {
        if (active) {
          setAuthLoading(false);
        }
      });
    return () => {
      active = false;
    };
  }, []);

  const requestedExperience = useMemo(() => experienceFromPath(path), [path]);
  const experience = useMemo<ProductExperience>(
    () => requestedExperience ?? (user ? defaultExperience(user) : "admin"),
    [requestedExperience, user]
  );
  const navigationExperience = useMemo<ProductExperience>(
    () => user && !hasExperienceAccess(user, experience) ? defaultExperience(user) : experience,
    [experience, user]
  );
  const canonicalPath = useMemo(() => canonicalPathFromPath(path), [path]);
  const route = useMemo(() => routeFromPath(canonicalPath), [canonicalPath]);
  const examWorkspace = useMemo(() => examWorkspaceFromPath(canonicalPath), [canonicalPath]);

  useEffect(() => {
    if (!user || requestedExperience) return;
    const nextPath = legacyRedirectForPath(path, user);
    if (nextPath) {
      router.history.replace(nextPath);
    }
  }, [path, requestedExperience, router, user]);

  const navigate = (nextPath: string) => {
    const targetExperience = experienceFromPath(nextPath) ?? navigationExperience;
    const externalPath = pathForExperience(nextPath, targetExperience);
    router.history.push(externalPath);
  };

  const changeExperience = (nextExperience: ProductExperience) => {
    navigate(pathForExperience("/dashboard", nextExperience));
  };

  const login = async (values: LoginFormValues) => {
    setLoginLoading(true);
    setLoginError(undefined);
    try {
      const response = await loginWithPassword(values);
      const nextUser = sessionFromAuthUser(response.user);
      const nextPath = pathForExperience("/dashboard", defaultExperience(nextUser));
      clearAllReviewDraftFallbacks();
      setUser(nextUser);
      router.history.push(nextPath);
    } catch (error) {
      setLoginError(error instanceof ApiClientError && error.code === "invalid_credentials" ? "学校或登录信息不正确。" : error instanceof ApiClientError && error.code === "login_rate_limited" ? `尝试次数过多，请${error.retryAfterSeconds ? `在 ${error.retryAfterSeconds} 秒后` : "稍后"}重试。` : "暂时无法登录，请检查网络连接后重试。");
    } finally {
      setLoginLoading(false);
    }
  };

  const logout = () => {
    void logoutSession().catch(() => undefined);
    if (user?.id) {
      clearReviewDraftFallbacks(user.id);
      if (user.publicComputer) clearPublicComputerData(user.tenant, user.id);
    }
    queryClient.clear();
    setUser(null);
  };

  const reauthenticate = useCallback(async (password: string) => {
    await reauthenticateSession(password);
  }, []);

  const lockSession = useCallback(async () => {
    await lockCurrentSession();
  }, []);

  if (authLoading) {
    return (
      <ConfigProvider>
        <AntApp>
          <LoadingState label="正在恢复登录状态" />
        </AntApp>
      </ConfigProvider>
    );
  }

  const activationPath = window.location.pathname === "/activate";
  const recoveryPath = window.location.pathname === "/recover";
  if (activationPath || recoveryPath) {
    const returnToLogin = (tenantCode?: string) => {
      if (user) logout();
      const loginPath = tenantCode ? `/?tenant=${encodeURIComponent(tenantCode)}` : "/";
      window.history.replaceState({}, "", loginPath);
      router.history.replace(loginPath);
    };
    return (
      <ConfigProvider>
        <AntApp>
          {activationPath ? <ActivationPage onComplete={returnToLogin} /> : <RecoveryPage onComplete={returnToLogin} />}
        </AntApp>
      </ConfigProvider>
    );
  }

  if (!user) {
    return (
      <ConfigProvider>
        <AntApp>
          <LoginPage
            onLogin={login}
            loading={loginLoading}
            error={loginError}
          />
        </AntApp>
      </ConfigProvider>
    );
  }

  const workspaceModule = examWorkspace ? (() => {
    const examId = examWorkspace.examId;
    const refreshWorkspace = () => void invalidateExamWorkspace(queryClient, examId);
    switch (examWorkspace.section) {
      case "students":
        return <ExamStudentScopePage examId={examId} canManage={hasEveryPermission(user, ["exam:manage", "org:manage"])} onExamChanged={refreshWorkspace} />;
      case "paper":
      case "questions":
        return <PaperRubricPage canManage={hasEveryPermission(user, ["exam:manage", "file:manage"])} canReadQuestionBank={hasEveryPermission(user,["question_bank:read"])} canImportQuestionBank={hasEveryPermission(user,["question_bank:read","question_bank:create","question_bank:edit"])} questionBankScope={`${user.tenant}:${user.id}`} canManageAssessment={experience === "admin" && hasEveryPermission(user, ["exam:manage"])} initialExamId={examId} onExamChanged={refreshWorkspace} onNavigate={navigate} />;
      case "template":
        return <AnswerSheetTemplatePage examId={examId} canManage={experience === "admin" && hasEveryPermission(user, ["exam:manage", "file:manage"])} canCalibrate={experience === "admin" && hasEveryPermission(user, ["grading:manage"])} onExamChanged={refreshWorkspace} />;
      case "settings":
        return <ExamReadinessPage examId={examId} canManage={hasEveryPermission(user, ["exam:manage"])} onNavigate={navigate} onExamChanged={refreshWorkspace} />;
      case "capture":
        return <CaptureBatchPage key={`${user.tenant}:${user.id}:${examId}`} user={user} examId={examId} canManage={hasEveryPermission(user, ["capture:manage", "file:manage"])} />;
      case "processing":
        return <SubmissionCapturePage canManage={hasEveryPermission(user, ["submission:manage", "file:manage", "ocr:manage", "segment:manage"])} canReadStudentNames={hasEveryPermission(user, ["org:manage"])} initialExamId={examId} />;
      case "grading":
        return <GradingWorkbenchPage canWork={hasEveryPermission(user, ["review:work"])} canManageTasks={experience === "admin" && hasEveryPermission(user, ["review:manage"])} canViewOriginalImage={experience === "admin"} canGrade={experience === "admin" && hasEveryPermission(user, ["grading:manage"])} canVerifyEvidence={experience === "admin" && hasEveryPermission(user, ["evidence:manage"])} canReturn={hasEveryPermission(user, ["review:work"])} currentUserId={user.id} currentTenantId={user.tenant} initialExamId={examId} personalScope={experience === "teacher"} />;
      case "quality":
        return <QualityDashboardPage examId={examId} canManage={experience === "admin" && hasEveryPermission(user, ["review:manage"])} />;
      case "scores":
        return <ScoreManagementPage mode={experience} canManage={experience === "admin" && hasEveryPermission(user, ["score:manage", "exam:manage", "submission:manage"])} canReadStudentNames={experience === "admin" && hasEveryPermission(user, ["org:manage"])} canReadAudit={experience === "admin" && hasEveryPermission(user, ["audit:read"])} initialExamId={examId} />;
      case "appeals":
        return <AppealCenterPage mode={experience} canRead={hasEveryPermission(user, ["appeal:read"])} canManage={experience === "admin" && hasEveryPermission(user, ["appeal:manage"])} canWork={canWorkTeacherAppeals(user, experience)} canReadAudit={experience === "admin" && hasEveryPermission(user, ["audit:read"])} canReadIdentities={experience === "admin" && hasEveryPermission(user, ["org:manage"])} canReadExams={hasEveryPermission(user, ["exam:manage"])} currentUser={user} initialExamId={examId} />;
      case "reports":
        return <LearningReportsPage canRead={hasEveryPermission(user, ["report:read"])} canExport={hasEveryPermission(user, ["report:export"])} initialExamId={examId} />;
      default:
        return undefined;
    }
  })() : undefined;

  const content =
    route === notFoundRoute ? (
      <NotFoundState onBack={() => navigate("/dashboard")} />
    ) : !hasExperienceAccess(user, experience)
      || !hasRouteAccess(user, route, experience)
      || Boolean(examWorkspace && !hasExamWorkspaceSectionAccess(experience, examWorkspace.section)) ? (
      <ForbiddenState onBack={() => navigate("/dashboard")} />
    ) : route.path === "/dashboard" ? (
      experience === "admin" ? <DashboardPage user={user} onNavigate={navigate} />
        : experience === "teacher" ? <TeacherDashboardPage user={user} onNavigate={navigate} />
          : <IdentityLandingPage user={user} experience={experience} onNavigate={navigate} />
    ) : examWorkspace ? (
      <ExamWorkspacePage examId={examWorkspace.examId} section={examWorkspace.section} experience={experience} currentUser={user} moduleContent={workspaceModule} onNavigate={navigate} />
    ) : route.path === "/exams/new" ? (
      <CreateExamPage user={user} onNavigate={navigate} />
    ) : route.path === "/exams" ? (
      <ExamManagementPage
        key={path}
        mode={experience}
        canManage={experience === "admin" && hasEveryPermission(user, ["exam:manage"])}
        currentUser={user}
        onOpenWorkspace={(examId) => navigate(`/exams/${encodeURIComponent(examId)}/overview`)}
        onCreateExam={() => navigate("/exams/new")}
      />
    ) : route.path === "/organization/setup" ? (
      <OrganizationSetupPage onNavigate={navigate} />
    ) : route.path === "/members/students" ? (
      <StudentManagementPage />
    ) : route.path === "/members/classes" ? (
      <ClassManagementPage />
    ) : route.path === "/members/teachers" ? (
      <TeacherManagementPage currentUser={user} />
    ) : route.path === "/platform/schools" ? (
      <PlatformSchoolsPage />
    ) : route.path === "/platform/getting-started" ? (
      <PlatformGettingStartedPage onNavigate={navigate} />
    ) : route.path === "/platform/model-config" ? (
      <PlatformModelConfigPage />
    ) : route.path === "/papers" ? (
      <PaperRubricPage canManage={hasEveryPermission(user, ["exam:manage", "file:manage"])} canReadQuestionBank={hasEveryPermission(user,["question_bank:read"])} canImportQuestionBank={hasEveryPermission(user,["question_bank:read","question_bank:create","question_bank:edit"])} questionBankScope={`${user.tenant}:${user.id}`} canManageAssessment={experience === "admin" && hasEveryPermission(user, ["exam:manage"])} />
    ) : route.path === "/question-bank" ? (
      <QuestionBankPage key={`${user.tenant}:${user.id}`} user={user} />
    ) : route.path === "/capture" ? (
      <SubmissionCapturePage
        canManage={hasEveryPermission(user, ["submission:manage", "file:manage", "ocr:manage", "segment:manage"])}
        canReadStudentNames={hasEveryPermission(user, ["org:manage"])}
      />
    ) : route.path === "/grading" ? (
      experience === "admin" ? <AdminGradingOperationsPage onNavigate={navigate} /> : (
        <GradingWorkbenchPage
          canWork={hasAnyPermission(user, ["review:manage", "review:work"])}
          canManageTasks={false}
          canViewOriginalImage={false}
          canGrade={false}
          canVerifyEvidence={false}
          canReturn={hasEveryPermission(user, ["review:work"])}
          currentUserId={user.id} currentTenantId={user.tenant}
          personalScope
        />
      )
    ) : route.path === "/arbitration" ? (
      <ArbitrationPage
        canAssign={experience === "admin" && hasEveryPermission(user, ["arbitration:manage"])}
        canWork={hasAnyPermission(user, ["arbitration:manage", "arbitration:work"])}
        canReadAudit={experience === "admin" && hasEveryPermission(user, ["audit:read"])}
        canReadExams={hasEveryPermission(user, ["exam:manage"])}
        currentUser={user}
        personalScope={experience === "teacher"}
      />
    ) : route.path === "/scores" ? (
      <ScoreManagementPage
        mode={experience}
        canManage={experience === "admin" && hasEveryPermission(user, ["score:manage", "exam:manage", "submission:manage"])}
        canReadStudentNames={experience === "admin" && hasEveryPermission(user, ["org:manage"])}
        canReadAudit={experience === "admin" && hasEveryPermission(user, ["audit:read"])}
      />
    ) : route.path === "/reports" ? (
      <LearningReportsPage canRead={hasEveryPermission(user, ["report:read"])} canExport={hasEveryPermission(user, ["report:export"])} />
    ) : route.path === "/appeals" ? (
      <AppealCenterPage
        mode={experience}
        canRead={hasEveryPermission(user, ["appeal:read"])}
        canManage={experience === "admin" && hasEveryPermission(user, ["appeal:manage"])}
        canWork={canWorkTeacherAppeals(user, experience)}
        canReadAudit={experience === "admin" && hasEveryPermission(user, ["audit:read"])}
        canReadIdentities={experience === "admin" && hasEveryPermission(user, ["org:manage"])}
        canReadExams={hasEveryPermission(user, ["exam:manage"])}
        currentUser={user}
      />
    ) : route.path === "/audit" ? (
      <AuditLogPage canRead={hasEveryPermission(user, ["audit:read"])} canExport={hasEveryPermission(user, ["audit:export"])} tenantName={user.tenant} />
    ) : route.path === "/system/status" ? (
      <SystemStatusPage />
    ) : route.path === "/system/models" ? (
      <ModelGovernancePage
        canManageProviders={hasEveryPermission(user, ["model:provider:manage"])}
        canManagePolicy={hasEveryPermission(user, ["model:policy:manage"])}
        canManageEvaluations={hasEveryPermission(user, ["model:evaluation:manage"])}
        canReadEligibility={hasEveryPermission(user, ["model:read"])}
        canReadDisagreements={hasAnyPermission(user, ["review:work", "review:manage"])}
        canManageDisagreements={hasEveryPermission(user, ["review:manage"])}
      />
    ) : route.path === "/account/sessions" ? (
      <SessionManagementPage key={`${user.tenant}:${user.id}`} onLoggedOut={logout} accountLabel={`${user.tenant}/${user.username}`} />
    ) : route.path === "/grading/subjective-batches" ? (
      <SubjectiveGradingBatchPage key={`${user.tenant}:${user.id}`} scopeKey={`${user.tenant}:${user.id}`} />
    ) : (
      <ModulePage route={route} experience={navigationExperience} onNavigate={navigate} />
    );

  return (
    <AntApp>
      <StepUpProvider onReauthenticate={reauthenticate}>
        <AppLayout
          user={user}
          currentRoute={route}
          experience={navigationExperience}
          availableExperiences={availableExperiences(user)}
          onNavigate={navigate}
          onExperienceChange={changeExperience}
          onLogout={logout}
          onLockSession={lockSession}
          onReauthenticate={reauthenticate}
          shellMode={route.path === "/exams/new" ? "focused" : experience === "teacher" && (route.path === "/grading" || examWorkspace?.section === "grading") ? "grading" : "standard"}
        >
          <OnboardingGate user={user} currentPath={canonicalPath} navigate={navigate} />
          <PendingBusinessCommands key={`${user.tenant}:${user.id}`} tenant={user.tenant} actor={user.id} />
          <ErrorBoundary resetKey={`${navigationExperience}:${canonicalPath}`}>
            <Suspense fallback={<LoadingState label="正在加载页面" />}>{content}</Suspense>
          </ErrorBoundary>
        </AppLayout>
      </StepUpProvider>
    </AntApp>
  );
}
