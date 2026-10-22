import { PendingBusinessCommands } from "./components/PendingBusinessCommands";
import { setBusinessCommandScope } from "./api/businessCommand";
import { useCallback, useEffect, useMemo, useReducer, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useRouter, useRouterState } from "@tanstack/react-router";
import { App as AntApp, ConfigProvider } from "antd";
import "antd/dist/reset.css";
import { getCurrentUser, lockCurrentSession, login as loginWithPassword, logout as logoutSession, reauthenticate as reauthenticateSession, type LoginResponse } from "./api/auth";
import { listSchools } from "./api/org";
import { sessionFromAuthUser, type SessionUser } from "./auth/session";
import { createSessionLifecycle } from "./auth/sessionLifecycle";
import { loginErrorMessage } from "./auth/loginError";
import { clearAllReviewDraftFallbacks, clearReviewDraftFallbacks } from "./auth/reviewDraftFallback";
import { clearLegacyRememberedLogin, clearPublicComputerData } from "./auth/loginSecurity";
import { StepUpProvider } from "./auth/stepUpContext";
import { AppLayout } from "./components/AppLayout";
import { LoadingState } from "./components/PageState";
import { LoginPage } from "./pages/LoginPage";
import { ActivationPage } from "./pages/ActivationPage";
import { RecoveryPage } from "./pages/RecoveryPage";
import { examWorkspaceFromPath, routeFromPath } from "./router/routes";
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
import { legacyRedirectForPath, normalizeEntryPath } from "./router/navigation";
import { OnboardingGate } from "./features/onboarding/OnboardingGate";
import { renderAppRouteContent } from "./router/pageRendering";
import { RouteContent } from "./router/RouteContent";
import "./router/route-content.css";
import { SchoolAssistantProvider } from "./features/school-assistant/SchoolAssistantProvider";

export function AppShell() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [user, setUser] = useState<SessionUser | null>(null);
  // 这里只触发会话转换后的渲染；真实代次仍取 session.generation，解锁即使不换代次也要刷新。
  const [, refreshSession] = useReducer((revision: number) => revision + 1, 0);
  const [session] = useState(() => createSessionLifecycle({
    publishUser: setUser,
    bindCommandScope: setBusinessCommandScope,
    onTransition: () => refreshSession(),
  }));
  const sessionGeneration = session.generation;
  const [authLoading, setAuthLoading] = useState(true);
  const [loginLoading, setLoginLoading] = useState(false);
  const [loginError, setLoginError] = useState<string | undefined>();
  const routerPath = useRouterState({
    select: (state) => `${state.location.pathname}${state.location.searchStr}`
  });
  const path = normalizeEntryPath(routerPath);

  useEffect(() => {
    if (!user?.permissions.includes("org:manage") || user.school !== user.tenant) {
      return;
    }
    let active = true;
    const generation = session.generation;
    listSchools()
      .then((response) => {
        const school = response.schools[0];
        if (active && school && session.isCurrent(generation)) {
          session.updateUser(generation, { ...user, school: school.name });
        }
      })
      .catch(() => undefined);
    return () => { active = false; };
  }, [session, user]);

  useEffect(() => {
    let active = true;
    const generation = session.begin();
    clearLegacyRememberedLogin();
    setAuthLoading(true);
    getCurrentUser()
      .then((response) => {
        if (active) {
          session.commit(generation, sessionFromAuthUser(response.user));
        }
      })
      .catch(() => {
        if (active && session.isCurrent(generation)) {
          clearAllReviewDraftFallbacks();
          session.commit(generation, null);
        }
      })
      .finally(() => {
        if (active && session.isCurrent(generation)) {
          setAuthLoading(false);
        }
      });
    return () => {
      active = false;
      session.invalidate();
    };
  }, [session]);

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
  const examWorkspace = useMemo(() => {
    const workspace = examWorkspaceFromPath(canonicalPath);
    // 历史概览链接继续可用，但内容归到学生范围，不再提供跨阶段概览。
    return workspace?.section === "overview" ? { ...workspace, section: "students" } : workspace;
  }, [canonicalPath]);

  useEffect(() => {
    if (!user || requestedExperience) return;
    const nextPath = legacyRedirectForPath(path, user);
    if (nextPath) {
      router.history.replace(nextPath);
    }
  }, [path, requestedExperience, router, user]);

  useEffect(() => {
    if (user && canonicalPath === "/system/models") {
      router.history.replace(pathForExperience("/platform/model-config?tab=governance", "admin"));
    }
  }, [canonicalPath, router, user]);

  const navigate = (nextPath: string) => {
    const targetExperience = experienceFromPath(nextPath) ?? navigationExperience;
    const externalPath = pathForExperience(nextPath, targetExperience);
    router.history.push(externalPath);
  };

  const changeExperience = (nextExperience: ProductExperience) => {
    navigate(pathForExperience("/dashboard", nextExperience));
  };

  const finishLogin = useCallback((response: LoginResponse, generation: number) => {
    if (!session.isCurrent(generation)) return;
    const nextUser = sessionFromAuthUser(response.user);
    const nextPath = pathForExperience("/dashboard", defaultExperience(nextUser));
    clearAllReviewDraftFallbacks();
    session.commit(generation, nextUser);
    router.history.push(nextPath);
  }, [router, session]);

  const finishWechatLogin = useCallback((response: LoginResponse) => {
    // 登录页面关闭或密码登录开始后，旧二维码的迟到回调不能重新发布身份。
    if (!session.isCurrent(sessionGeneration)) return;
    finishLogin(response, session.begin());
  }, [finishLogin, session, sessionGeneration]);

  const login = async (values: LoginFormValues) => {
    const generation = session.begin();
    setAuthLoading(false);
    setLoginLoading(true);
    setLoginError(undefined);
    try {
      const response = await session.enqueueAuthentication(() => {
        session.assertCurrent(generation);
        return loginWithPassword(values);
      });
      finishLogin(response, generation);
    } catch (error) {
      if (session.isCurrent(generation)) setLoginError(loginErrorMessage(error));
    } finally {
      if (session.isCurrent(generation)) setLoginLoading(false);
    }
  };

  const logout = () => {
    if (!session.isCurrent(sessionGeneration)) return;
    const previousUser = session.user;
    session.begin();
    setAuthLoading(false);
    setLoginLoading(false);
    setLoginError(undefined);
    void session.enqueueAuthentication(logoutSession).catch(() => undefined);
    if (previousUser?.id) {
      clearReviewDraftFallbacks(previousUser.id);
      if (previousUser.publicComputer) clearPublicComputerData(previousUser.tenant, previousUser.id);
    }
    // 先使业务命令失效，再取消读请求与清缓存；已经发出的远端写入仍需按原命令查询回执。
    void queryClient.cancelQueries();
    queryClient.clear();
  };

  const reauthenticate = useCallback(async (password: string) => {
    session.assertCurrent(sessionGeneration);
    await session.enqueueAuthentication(() => {
      session.assertCurrent(sessionGeneration);
      return reauthenticateSession(password);
    });
    session.assertCurrent(sessionGeneration);
    session.unlock(sessionGeneration);
  }, [session, sessionGeneration]);

  const lockSession = useCallback(async () => {
    session.assertCurrent(sessionGeneration);
    const generation = session.begin(true);
    await session.enqueueAuthentication(() => {
      session.assertCurrent(generation);
      return lockCurrentSession();
    });
    session.assertCurrent(generation);
  }, [session, sessionGeneration]);

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
            onWechatLogin={finishWechatLogin}
            loading={loginLoading}
            error={loginError}
          />
        </AntApp>
      </ConfigProvider>
    );
  }

  // 普通导航保留旧画面直到新模块和首屏数据就绪；账户、身份或授权变化时立即清除旧范围内容。
  const routeScopeKey = JSON.stringify([user.tenant, user.id, experience,
    [...user.roles].sort(), [...user.permissions].sort(), user.organizationScope, user.publicComputer]);
  const content = renderAppRouteContent({
    user, route, path, experience, navigationExperience, examWorkspace, queryClient, navigate, logout
  });

  return (
    <AntApp>
      <StepUpProvider onReauthenticate={reauthenticate}>
        <AppLayout
          user={user}
          currentRoute={route}
          examWorkspaceSection={examWorkspace?.section}
          experience={navigationExperience}
          availableExperiences={availableExperiences(user)}
          onNavigate={navigate}
          onExperienceChange={changeExperience}
          onLogout={logout}
          onLockSession={lockSession}
          onReauthenticate={reauthenticate}
          shellMode={experience === "teacher" && (route.path === "/grading" || examWorkspace?.section === "grading") ? "grading" : "standard"}
        >
          <SchoolAssistantProvider user={user} onNavigate={navigate} locationKey={`${navigationExperience}:${canonicalPath}`}>
            <OnboardingGate user={user} currentPath={canonicalPath} navigate={navigate} />
            <PendingBusinessCommands key={`${user.tenant}:${user.id}`} tenant={user.tenant} actor={user.id} onNavigate={navigate} />
            <RouteContent scopeKey={routeScopeKey} locationKey={`${navigationExperience}:${path}`} title={route.title}>
              {content}
            </RouteContent>
          </SchoolAssistantProvider>
        </AppLayout>
      </StepUpProvider>
    </AntApp>
  );
}
