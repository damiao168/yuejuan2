import { PendingBusinessCommands } from "./components/PendingBusinessCommands";
import { setBusinessCommandScope } from "./api/businessCommand";
import { useCallback, useEffect, useMemo, useReducer, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useRouter, useRouterState } from "@tanstack/react-router";
import { App as AntApp, ConfigProvider } from "antd";
import "antd/dist/reset.css";
import { changePassword, getCurrentUser, lockCurrentSession, login as loginWithPassword, logout as logoutSession, reauthenticate as reauthenticateSession, type LoginResponse } from "./api/auth";
import { listSchools } from "./api/org";
import { sessionFromAuthUser, type SessionUser } from "./auth/session";
import { createSessionLifecycle } from "./auth/sessionLifecycle";
import { loginErrorMessage } from "./auth/loginError";
import { clearAllReviewDraftFallbacks, clearReviewDraftFallbacks } from "./auth/reviewDraftFallback";
import { clearLegacyRememberedLogin, clearPublicComputerData, passwordMinimumForRoles } from "./auth/loginSecurity";
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
import { clearConversationRefresh } from "./features/school-assistant/conversationRefresh";
import { hasPendingLogout, rememberPendingLogout } from "./auth/pendingLogout";
import { FirstPasswordChange } from "./auth/FirstPasswordChange";
import { openStudentPortal, studentPortalDestination } from "./auth/loginDestination";

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
  const [loginSchoolCode, setLoginSchoolCode] = useState("");
  const [logoutState, setLogoutState] = useState<"idle" | "pending" | "failed">(() => hasPendingLogout() ? "pending" : "idle");
  const routerPath = useRouterState({
    select: (state) => `${state.location.pathname}${state.location.searchStr}`
  });
  const path = normalizeEntryPath(routerPath);
  const revokeSession = useCallback(async () => {
    clearConversationRefresh();
    setLogoutState("pending");
    try {
      await session.enqueueAuthentication(logoutSession);
      rememberPendingLogout(false);
      setLogoutState("idle");
    } catch {
      // HttpOnly Cookie 只能由服务端清除；失败时停留在隔离界面，不能宣称退出成功。
      setLogoutState("failed");
    }
  }, [session]);

  useEffect(() => {
    if (user?.mustChangePassword || !user?.permissions.includes("org:manage") || user.school !== user.tenant) {
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
    if (hasPendingLogout()) {
      setAuthLoading(false);
      void revokeSession();
      return () => { active = false; };
    }
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
          clearConversationRefresh();
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
  }, [session, revokeSession]);

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
    if (!user || requestedExperience || defaultExperience(user) === "student") return;
    const nextPath = legacyRedirectForPath(path, user);
    if (nextPath) {
      router.history.replace(nextPath);
    }
  }, [path, requestedExperience, router, user]);

  useEffect(() => {
    if (authLoading || logoutState !== "idle" || !user || defaultExperience(user) !== "student") return;
    const params = new URLSearchParams(window.location.search);
    openStudentPortal(studentPortalDestination(window.location.search, loginSchoolCode || params.get("tenant") || params.get("school") || ""));
  }, [authLoading, loginSchoolCode, logoutState, user]);

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

  const finishLogin = useCallback((response: LoginResponse, generation: number, schoolCode = "") => {
    if (!session.isCurrent(generation)) return;
    const nextUser = sessionFromAuthUser(response.user);
    const nextPath = pathForExperience("/dashboard", defaultExperience(nextUser));
    clearAllReviewDraftFallbacks();
    // 明确重新认证后不保留前一会话的刷新检查点；正常刷新恢复身份不经过此入口。
    clearConversationRefresh();
    setLoginSchoolCode(schoolCode);
    session.commit(generation, nextUser);
    if (defaultExperience(nextUser) !== "student") router.history.push(nextPath);
  }, [router, session]);

  const authenticateWechat = useCallback(async (authenticate: () => Promise<LoginResponse | undefined>) => {
    const generation = session.begin();
    setLoginLoading(false);
    // 轮询本身可能写 Cookie，必须等此前认证响应落定后才发送；只串行完成回调无法保护 Cookie。
    const response = await session.enqueueAuthentication(() => {
      session.assertCurrent(generation);
      return authenticate();
    });
    if (response) finishLogin(response, generation);
  }, [finishLogin, session]);

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
      finishLogin(response, generation, values.tenant_code || values.tenant_hint || "");
    } catch (error) {
      if (session.isCurrent(generation)) setLoginError(loginErrorMessage(error));
    } finally {
      if (session.isCurrent(generation)) setLoginLoading(false);
    }
  };

  const logout = () => {
    if (!session.isCurrent(sessionGeneration)) return;
    const previousUser = session.user;
    // 退出意图先清当前标签页的聊天检查点；普通卸载不能清，否则刷新仍会丢对话。
    clearConversationRefresh();
    session.begin();
    setAuthLoading(false);
    setLoginLoading(false);
    setLoginError(undefined);
    if (previousUser?.id) {
      clearReviewDraftFallbacks(previousUser.id);
      if (previousUser.publicComputer) clearPublicComputerData(previousUser.tenant, previousUser.id);
    }
    // 先使业务命令失效，再取消读请求与清缓存；已经发出的远端写入仍需按原命令查询回执。
    void queryClient.cancelQueries();
    queryClient.clear();
    rememberPendingLogout(true);
    void revokeSession();
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

  const changeInitialPassword = async (input: { current_password: string; new_password: string }) => {
    await session.enqueueAuthentication(() => {
      session.assertCurrent(sessionGeneration);
      return changePassword(input);
    });
    session.assertCurrent(sessionGeneration);
    // 服务端已经改密并撤销全部会话；清本机身份后直接回登录，不再用旧会话恢复业务页。
    session.begin();
    clearAllReviewDraftFallbacks();
    clearConversationRefresh();
    void queryClient.cancelQueries(); queryClient.clear();
    rememberPendingLogout(false);
    setAuthLoading(false); setLoginLoading(false); setLoginError(undefined);
  };

  if (logoutState !== "idle") {
    return <main className="portal-loading" role="status">{logoutState === "pending" ? "正在退出登录…" : <><p>退出尚未完成，服务端会话可能仍有效。请连接网络后重试。</p><button type="button" onClick={() => void revokeSession()}>重试退出登录</button></>}</main>;
  }

  if (authLoading) {
    return (
      <ConfigProvider>
        <AntApp>
          <LoadingState label="正在恢复登录状态" />
        </AntApp>
      </ConfigProvider>
    );
  }

  if (user && defaultExperience(user) === "student") {
    return <ConfigProvider><AntApp><LoadingState label="正在进入学生成绩页" /></AntApp></ConfigProvider>;
  }

  // 放在激活/恢复和业务路由之前；改URL也不能挂载初始密码会话的业务内容。
  if (user?.mustChangePassword) {
    return <ConfigProvider><AntApp><FirstPasswordChange key={`${user.tenant}:${user.id}:${sessionGeneration}`} minimumPasswordLength={passwordMinimumForRoles(user.roles)} onChangePassword={changeInitialPassword} onLogout={logout} /></AntApp></ConfigProvider>;
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
            onWechatAuthenticate={authenticateWechat}
            loading={loginLoading}
            error={loginError}
          />
        </AntApp>
      </ConfigProvider>
    );
  }

  // 普通导航只在新模块加载期间保留旧画面，取数由目标页展示；账户、身份或授权变化时立即清除旧范围内容。
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
