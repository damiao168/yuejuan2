import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Alert, Avatar, Button, ConfigProvider, Drawer, Dropdown, Grid, Input, Layout, Menu, Modal, Segmented, Space, Tag, theme, Tooltip } from "antd";
import { ArrowLeft, ChevronDown, Menu as MenuIcon, PanelLeft, UserRound } from "lucide-react";
import { productIdentityLabel, type SessionUser } from "../auth/session";
import type { AppRoute } from "../router/routes";
import { hasRouteAccess, routeGroups, routePresentation, visibleRoutes } from "../router/routes";
import { experienceLabel, type ProductExperience } from "../router/experience";
import { workspaceLabel } from "../workspaces/registry";
import { MockBadge } from "./MockBadge";
import { applyReadingSize, readReadingSize } from "@edugrade/design-tokens";
import { isPublicComputerIdle, PUBLIC_COMPUTER_IDLE_LOCK_MS } from "../auth/loginSecurity";
import { getUserErrorMessage } from "../api/client";
import { StepUpDialog, useStepUp } from "../auth/stepUpContext";

const { Header, Sider, Content } = Layout;
const DESKTOP_NAVIGATION_WIDTH = 192;
const DESKTOP_NAVIGATION_COLLAPSED_WIDTH = 64;
const MOBILE_NAVIGATION_WIDTH = 280;
const NAVIGATION_COLLAPSED_STORAGE_KEY = "edugrade.navigation.collapsed";
export type ShellMode = "standard" | "focused" | "grading";
export interface FocusedShellContext {
  title: string;
  backLabel: string;
  backPath: string;
}

export function AppLayout({
  user,
  currentRoute,
  experience,
  availableExperiences,
  children,
  onNavigate,
  onExperienceChange,
  onLogout,
  onLockSession,
  onReauthenticate,
  shellMode = "standard",
  focusedContext = { title: "新建考试", backLabel: "返回考试列表", backPath: "/exams" }
}: {
  user: SessionUser;
  currentRoute: AppRoute;
  experience: ProductExperience;
  availableExperiences: ProductExperience[];
  children: ReactNode;
  onNavigate: (path: string) => void;
  onExperienceChange: (experience: ProductExperience) => void;
  onLogout: () => void;
  onLockSession: () => Promise<void>;
  onReauthenticate: (password: string) => Promise<void>;
  shellMode?: ShellMode;
  focusedContext?: FocusedShellContext;
}) {
  const immersive = shellMode !== "standard";
  const focused = shellMode === "focused";
  const screens = Grid.useBreakpoint();
  const [readingSize, setReadingSize] = useState(readReadingSize);
  useEffect(() => { applyReadingSize(readingSize); }, [readingSize]);
  const desktopNavigation = Boolean(screens.lg);
  const [navigationOpen, setNavigationOpen] = useState(false);
  const [navigationScrollbarVisible, setNavigationScrollbarVisible] = useState(false);
  const [navigationScrollbarFading, setNavigationScrollbarFading] = useState(false);
  const navigationScrollbarHideTimer = useRef<number | null>(null);
  const [workspaceScrollbarVisible, setWorkspaceScrollbarVisible] = useState(false);
  const [workspaceScrollbarFading, setWorkspaceScrollbarFading] = useState(false);
  const workspaceScrollbarHideTimer = useRef<number | null>(null);
  const [workspaceLocked, setWorkspaceLocked] = useState(false);
  const [unlockPassword, setUnlockPassword] = useState("");
  const [unlocking, setUnlocking] = useState(false);
  const [lockingSession, setLockingSession] = useState(false);
  const [unlockError, setUnlockError] = useState<string>();
  const { cancel: cancelStepUp } = useStepUp();
  const [navigationCollapsed, setNavigationCollapsed] = useState(() => {
    try {
      return window.localStorage.getItem(NAVIGATION_COLLAPSED_STORAGE_KEY) === "true";
    } catch {
      return false;
    }
  });
  const permittedRoutes = visibleRoutes(experience).filter((route) => hasRouteAccess(user, route, experience));
  const selectedPath = currentRoute.key === "examWorkspace" ? "/exams" : currentRoute.path;
  const currentPresentation = routePresentation(currentRoute, experience);
  useEffect(() => () => {
    if (navigationScrollbarHideTimer.current !== null) {
      window.clearTimeout(navigationScrollbarHideTimer.current);
    }
    if (workspaceScrollbarHideTimer.current !== null) {
      window.clearTimeout(workspaceScrollbarHideTimer.current);
    }
  }, []);
  useEffect(() => {
    if (!user.publicComputer) {
      setWorkspaceLocked(false);
      return;
    }
    if (workspaceLocked) return;
    let lastActivityAt = Date.now();
    let timer = 0;
    let lockTriggered = false;
    const lockWorkspace = () => {
      if (lockTriggered) return;
      lockTriggered = true;
      cancelStepUp();
      setWorkspaceLocked(true);
      setUnlockError(undefined);
      setLockingSession(true);
      void onLockSession()
        .catch(() => setUnlockError("暂时无法确认服务端锁定状态，请恢复网络后重新验证或退出。"))
        .finally(() => setLockingSession(false));
    };
    const checkIdle = () => {
      if (isPublicComputerIdle(lastActivityAt)) {
        lockWorkspace();
        return;
      }
      timer = window.setTimeout(checkIdle, Math.max(1, PUBLIC_COMPUTER_IDLE_LOCK_MS - (Date.now() - lastActivityAt)));
    };
    const recordActivity = () => {
      lastActivityAt = Date.now();
      window.clearTimeout(timer);
      timer = window.setTimeout(checkIdle, PUBLIC_COMPUTER_IDLE_LOCK_MS);
    };
    const checkOnReturn = () => {
      if (document.visibilityState === "visible") checkIdle();
    };
    window.addEventListener("pointerdown", recordActivity);
    window.addEventListener("keydown", recordActivity);
    window.addEventListener("touchstart", recordActivity, { passive: true });
    window.addEventListener("pagehide", lockWorkspace);
    document.addEventListener("visibilitychange", checkOnReturn);
    timer = window.setTimeout(checkIdle, PUBLIC_COMPUTER_IDLE_LOCK_MS);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("pointerdown", recordActivity);
      window.removeEventListener("keydown", recordActivity);
      window.removeEventListener("touchstart", recordActivity);
      window.removeEventListener("pagehide", lockWorkspace);
      document.removeEventListener("visibilitychange", checkOnReturn);
    };
  }, [cancelStepUp, onLockSession, user.publicComputer, workspaceLocked]);
  const unlockWorkspace = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!unlockPassword || unlocking || lockingSession) return;
    setUnlocking(true);
    setUnlockError(undefined);
    try {
      await onReauthenticate(unlockPassword);
      setUnlockPassword("");
      setWorkspaceLocked(false);
    } catch (error) {
      setUnlockError(getUserErrorMessage(error, "验证失败，请检查当前账号密码后重试。"));
    } finally {
      setUnlocking(false);
    }
  };
  const showNavigationScrollbar = () => {
    if (navigationScrollbarHideTimer.current !== null) {
      window.clearTimeout(navigationScrollbarHideTimer.current);
      navigationScrollbarHideTimer.current = null;
    }
    setNavigationScrollbarVisible(true);
    setNavigationScrollbarFading(false);
  };
  const scheduleNavigationScrollbarHide = () => {
    if (navigationScrollbarHideTimer.current !== null) {
      window.clearTimeout(navigationScrollbarHideTimer.current);
    }
    setNavigationScrollbarFading(true);
    navigationScrollbarHideTimer.current = window.setTimeout(() => {
      setNavigationScrollbarVisible(false);
      setNavigationScrollbarFading(false);
      navigationScrollbarHideTimer.current = null;
    }, 3000);
  };
  useEffect(() => {
    const root = document.documentElement;
    root.classList.toggle("is-workspace-scrollbar-visible", workspaceScrollbarVisible);
    root.classList.toggle("is-workspace-scrollbar-fading", workspaceScrollbarFading);
    return () => {
      root.classList.remove("is-workspace-scrollbar-visible", "is-workspace-scrollbar-fading");
    };
  }, [workspaceScrollbarVisible, workspaceScrollbarFading]);
  const showWorkspaceScrollbar = () => {
    if (workspaceScrollbarHideTimer.current !== null) {
      window.clearTimeout(workspaceScrollbarHideTimer.current);
      workspaceScrollbarHideTimer.current = null;
    }
    setWorkspaceScrollbarVisible(true);
    setWorkspaceScrollbarFading(false);
  };
  const scheduleWorkspaceScrollbarHide = () => {
    if (workspaceScrollbarHideTimer.current !== null) {
      window.clearTimeout(workspaceScrollbarHideTimer.current);
    }
    setWorkspaceScrollbarFading(true);
    workspaceScrollbarHideTimer.current = window.setTimeout(() => {
      setWorkspaceScrollbarVisible(false);
      setWorkspaceScrollbarFading(false);
      workspaceScrollbarHideTimer.current = null;
    }, 3000);
  };
  const toggleNavigation = () => {
    const next = !navigationCollapsed;
    setNavigationCollapsed(next);
    try { window.localStorage.setItem(NAVIGATION_COLLAPSED_STORAGE_KEY, String(next)); } catch { /* persistence is optional */ }
  };
  const menuItems = routeGroups(experience)
    .map((group) => {
      const children = permittedRoutes
        .filter((route) => routePresentation(route, experience).group === group)
        .map((route) => ({
          key: route.path,
          icon: route.icon,
          label: routePresentation(route, experience).title
        }));
      return children.length > 0
        ? {
            key: group,
            label: group,
            type: "group" as const,
            children
          }
        : null;
    })
    .filter((item): item is NonNullable<typeof item> => item !== null);
  const accountMenu = {
    inlineCollapsed: false,
    items: [
      {
        key: "account",
        type: "group" as const,
        label: user.displayName && user.displayName !== user.username
          ? `${user.displayName}（${user.username}）`
          : user.username
      },
      { key: "sessions", label: "账户安全" },
      { key: "reading-size", label: readingSize === "large" ? "✓ 大字阅读 · 切换标准字号" : "大字阅读" },
      ...availableExperiences.filter((value) => value !== experience).map((value) => ({ key: `experience:${value}`, label: `切换到${workspaceLabel(user, value)}` })),
      { key: "logout", label: "退出登录" }
    ],
    onClick: ({ key }: { key: string }) => {
      setNavigationOpen(false);
      if (key === "logout") {
        onLogout();
      } else if (key === "reading-size") {
        setReadingSize((size) => size === "large" ? "standard" : "large");
      } else if (key.startsWith("experience:")) {
        onExperienceChange(key.slice(11) as ProductExperience);
      } else if (key === "sessions") {
        onNavigate("/account/sessions");
      }
    }
  };
  const navigation = (
    <div
      id="primary-navigation"
      className={desktopNavigation && navigationCollapsed ? "sidebar-inner is-collapsed" : "sidebar-inner"}
      onMouseEnter={desktopNavigation ? showNavigationScrollbar : undefined}
      onMouseLeave={desktopNavigation ? scheduleNavigationScrollbarHide : undefined}
    >
      <div className="sidebar-header">
        <button type="button" className="brand-block brand-button" onClick={() => { setNavigationOpen(false); onNavigate("/dashboard"); }} aria-label={`返回${experienceLabel(experience)}工作台`}>
          <div className="brand-copy">
            <strong>EduGrade</strong>
          </div>
        </button>
        {desktopNavigation ? (
          <Tooltip title={navigationCollapsed ? "展开导航" : "收起导航"} placement="right">
            <button
              type="button"
              className="sidebar-toggle"
              aria-label={navigationCollapsed ? "展开导航" : "收起导航"}
              aria-expanded={!navigationCollapsed}
              aria-controls="primary-navigation"
              onClick={toggleNavigation}
            >
              <PanelLeft size={18} strokeWidth={1.8} />
            </button>
          </Tooltip>
        ) : null}
      </div>
      <div
        className={
          !desktopNavigation
            ? "sidebar-main is-scrollbar-visible"
            : navigationScrollbarVisible
              ? `sidebar-main is-scrollbar-visible${navigationScrollbarFading ? " is-scrollbar-fading" : ""}`
              : "sidebar-main"
        }
      >
        {availableExperiences.length > 1 && !(desktopNavigation && navigationCollapsed) ? (
          <div className="experience-switcher">
            <Segmented
              block
              size="small"
              aria-label="切换管理端或教师端"
              value={experience}
              options={availableExperiences.map((value) => ({ value, label: workspaceLabel(user, value) }))}
              onChange={(value) => { setNavigationOpen(false); onExperienceChange(value as ProductExperience); }}
            />
          </div>
        ) : null}
        <Menu
          mode="inline"
          inlineCollapsed={desktopNavigation && navigationCollapsed}
          selectedKeys={[selectedPath]}
          items={menuItems}
          onClick={(item) => { setNavigationOpen(false); onNavigate(item.key); }}
          className="side-menu"
        />
      </div>
      <div className="sidebar-footer">
        <Dropdown menu={accountMenu} placement="topLeft" trigger={["click"]}>
          <button type="button" className="sidebar-account" aria-label={`账户菜单：${user.displayName || user.username}`}>
            <Avatar size={28} icon={<UserRound size={16} />} />
            <span className="sidebar-account-copy">
              <strong>{user.displayName || user.username}</strong>
              <small>{productIdentityLabel(user)}</small>
            </span>
            <ChevronDown size={14} />
          </button>
        </Dropdown>
      </div>
    </div>
  );

  return (
    <ConfigProvider
      theme={{
        algorithm: theme.defaultAlgorithm,
        token: {
          fontSize: readingSize === "large" ? 16 : 14,
          fontSizeSM: readingSize === "large" ? 14 : 13,
          controlHeight: readingSize === "large" ? 44 : 40,
          controlHeightSM: 36,
          lineHeight: 1.6,
          colorPrimary: "#1677ff",
          colorText: "#172033",
          colorTextSecondary: "#667085",
          colorBorder: "#e4e7ec",
          colorSuccess: "#52c41a",
          colorWarning: "#ad6800",
          colorError: "#c9363e",
          colorInfo: "#13c2c2",
          borderRadius: 6,
          fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', 'Noto Sans CJK SC', Arial, sans-serif"
        }
      }}
    >
      <Layout className={shellMode === "grading" ? "app-frame immersive-frame" : focused ? "app-frame focused-frame" : "app-frame"}>
        <a className="skip-to-workspace" href="#main-workspace" onClick={(event) => { event.preventDefault(); document.getElementById("main-workspace")?.focus(); }}>跳到主要内容</a>
        {!immersive && desktopNavigation ? (
          <Sider width={DESKTOP_NAVIGATION_WIDTH} collapsedWidth={DESKTOP_NAVIGATION_COLLAPSED_WIDTH} collapsed={navigationCollapsed} trigger={null} className="sidebar">
            {navigation}
          </Sider>
        ) : null}
        {!immersive && !desktopNavigation ? (
          <Drawer
            className="mobile-navigation"
            title={null}
            placement="left"
            width={MOBILE_NAVIGATION_WIDTH}
            open={navigationOpen}
            onClose={() => setNavigationOpen(false)}
            styles={{ body: { padding: 0 } }}
          >
            {navigation}
          </Drawer>
        ) : null}
        <Layout>
          {user.publicComputer ? (
            <div className="public-computer-banner" role="status">
              <span><Tag color="orange">公共电脑</Tag>请勿让他人使用当前会话，离开前退出并清理本机数据。</span>
              <Button danger size="small" onClick={onLogout}>立即退出</Button>
            </div>
          ) : null}
          {focused ? (
            <Header className="topbar focused-topbar">
              <div className="topbar-left">
                <Button type="text" icon={<ArrowLeft size={17} />} aria-label={focusedContext.backLabel} onClick={() => onNavigate(focusedContext.backPath)}>{focusedContext.backLabel}</Button>
                <strong>{focusedContext.title}</strong>
              </div>
              <Space className="topbar-actions">
                <Dropdown menu={accountMenu} placement="bottomRight" trigger={["click"]}>
                  <Button className="user-button"><Avatar size={24} icon={<UserRound size={15} />} /><span>{user.username}</span><ChevronDown size={14} /></Button>
                </Dropdown>
              </Space>
            </Header>
          ) : shellMode === "grading" ? (
            <Header className="topbar immersive-topbar">
              <div className="topbar-left">
                <Space size="middle">
                <Button type="text" icon={<ArrowLeft size={17} />} aria-label="退出阅卷" onClick={() => onNavigate("/dashboard")}>退出阅卷</Button>
                <strong>{experienceLabel(experience)} · {currentPresentation.title}</strong>
                </Space>
                {currentRoute.mock ? <MockBadge compact={true} /> : null}
              </div>
              <Space className="topbar-actions">
                <Dropdown menu={accountMenu} placement="bottomRight" trigger={["click"]}>
                <Button className="user-button">
                  <Avatar size={24} icon={<UserRound size={15} />} />
                  <span>{user.username}</span>
                  <ChevronDown size={14} />
                </Button>
                </Dropdown>
              </Space>
            </Header>
          ) : !desktopNavigation ? (
            <Header className="mobile-shellbar">
              <Button className="mobile-nav-button" type="text" icon={<MenuIcon size={20} />} aria-label="打开主导航" onClick={() => setNavigationOpen(true)} />
              <div className="mobile-page-context"><strong>{currentPresentation.title}</strong><span>{user.school || workspaceLabel(user, experience)}</span></div>
              {currentRoute.mock ? <MockBadge compact={true} /> : null}
            </Header>
          ) : null}
          <Content
            id="main-workspace"
            tabIndex={-1}
            className={shellMode === "grading" ? "workspace immersive-workspace" : focused ? "workspace focused-workspace" : "workspace"}
            onMouseEnter={showWorkspaceScrollbar}
            onMouseLeave={scheduleWorkspaceScrollbarHide}
          >
            {!immersive && desktopNavigation && currentRoute.mock ? <div className="workspace-route-indicator"><MockBadge compact={true} /></div> : null}
            {children}
          </Content>
        </Layout>
        <Modal
          rootClassName="public-computer-lock-modal"
          open={user.publicComputer && workspaceLocked}
          title="公共电脑已锁定"
          closable={false}
          keyboard={false}
          maskClosable={false}
          footer={null}
          width={420}
        >
          <form className="public-computer-unlock" onSubmit={unlockWorkspace}>
            <p>为保护未完成的阅卷内容，工作区已在 15 分钟无操作后锁定。请使用 <strong>{user.displayName || user.username}</strong> 的密码解锁。</p>
            {unlockError ? <Alert type="error" showIcon message={unlockError} /> : null}
            <Input.Password
              value={unlockPassword}
              onChange={(event) => setUnlockPassword(event.target.value)}
              placeholder="当前账号密码"
              autoComplete="current-password"
              autoFocus
            />
            <div className="public-computer-unlock-actions">
              <Button danger onClick={onLogout}>退出并清理</Button>
              <Button type="primary" htmlType="submit" loading={unlocking || lockingSession} disabled={!unlockPassword || lockingSession}>解锁</Button>
            </div>
          </form>
        </Modal>
        <StepUpDialog accountLabel={user.displayName || user.username} hidden={workspaceLocked} />
      </Layout>
    </ConfigProvider>
  );
}
