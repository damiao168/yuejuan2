import { Avatar, Button, Dropdown, Layout, Menu, Segmented, Space, Tooltip, type MenuProps } from "antd";
import { ArrowLeft, ChevronDown, Menu as MenuIcon, PanelLeft, UserRound } from "lucide-react";
import { accountIdentityLabel, productIdentityLabel, type SessionUser } from "../auth/session";
import { experienceLabel, type ProductExperience } from "../router/experience";
import { examWorkspaceNavigationPath } from "../router/navigation";
import type { AppRoute } from "../router/routes";
import { workspaceLabel } from "../workspaces/registry";
import type { FocusedShellContext, ShellMode } from "./AppLayout";
import { MockBadge } from "./MockBadge";

const frameClasses: Record<ShellMode, string> = {
  standard: "app-frame", focused: "app-frame focused-frame", grading: "app-frame immersive-frame"
};
const workspaceClasses: Record<ShellMode, string> = {
  standard: "workspace", focused: "workspace focused-workspace", grading: "workspace immersive-workspace"
};

export function selectedNavigationPath(route: AppRoute, examWorkspaceSection?: string): string {
  if (route.key === "examWorkspace") return examWorkspaceNavigationPath(examWorkspaceSection ?? "overview");
  if (route.key === "examCreate") return "/exams";
  return route.path;
}

export function shellClassNames(mode: ShellMode, routeKey: AppRoute["key"]) {
  // 沉浸阅卷和新建考试优先；只有标准外壳使用考试工作区布局。
  const workspace = mode === "standard" && routeKey === "examWorkspace"
    ? "workspace exam-workspace-shell" : workspaceClasses[mode];
  return { frame: frameClasses[mode], workspace };
}

export function navigationScrollClass(desktop: boolean, visible: boolean, fading: boolean): string {
  if (!desktop) return "sidebar-main is-scrollbar-visible";
  if (!visible) return "sidebar-main";
  return `sidebar-main is-scrollbar-visible${fading ? " is-scrollbar-fading" : ""}`;
}

type ShellNavigationProps = Readonly<{
  desktop: boolean;
  collapsed: boolean;
  scrollbarVisible: boolean;
  scrollbarFading: boolean;
  onShowScrollbar: () => void;
  onHideScrollbar: () => void;
  onCloseNavigation: () => void;
  onToggleNavigation: () => void;
  onNavigate: (path: string) => void;
  onExperienceChange: (experience: ProductExperience) => void;
  user: SessionUser;
  experience: ProductExperience;
  availableExperiences: ProductExperience[];
  selectedPath: string;
  menuItems: MenuProps["items"];
  accountMenu: MenuProps;
}>;

export function ShellNavigation({ desktop, collapsed, scrollbarVisible, scrollbarFading, onShowScrollbar, onHideScrollbar, onCloseNavigation, onToggleNavigation, onNavigate, onExperienceChange, user, experience, availableExperiences, selectedPath, menuItems, accountMenu }: ShellNavigationProps) {
  const collapseLabel = collapsed ? "展开导航" : "收起导航";
  return <div id="primary-navigation" className={desktop && collapsed ? "sidebar-inner is-collapsed" : "sidebar-inner"}
    onMouseEnter={desktop ? onShowScrollbar : undefined} onMouseLeave={desktop ? onHideScrollbar : undefined}>
    <div className="sidebar-header">
      <button type="button" className="brand-block brand-button" onClick={() => { onCloseNavigation(); onNavigate("/dashboard"); }} aria-label={`返回${experienceLabel(experience)}工作台`}>
        <div className="brand-copy"><strong>EduGrade</strong></div>
      </button>
      {desktop ? <Tooltip title={collapseLabel} placement="right">
        <button type="button" className="sidebar-toggle" aria-label={collapseLabel} aria-expanded={!collapsed}
          aria-controls="primary-navigation" onClick={onToggleNavigation}><PanelLeft size={18} strokeWidth={1.8} /></button>
      </Tooltip> : null}
    </div>
    <div className={navigationScrollClass(desktop, scrollbarVisible, scrollbarFading)}>
      {availableExperiences.length > 1 && !(desktop && collapsed) ? <div className="experience-switcher">
        <Segmented block size="small" aria-label="切换管理端或教师端" value={experience}
          options={availableExperiences.map((value) => ({ value, label: workspaceLabel(user, value) }))}
          onChange={(value) => { onCloseNavigation(); onExperienceChange(value as ProductExperience); }} />
      </div> : null}
      <Menu mode="inline" inlineCollapsed={desktop && collapsed} selectedKeys={[selectedPath]} items={menuItems}
        onClick={(item) => { onCloseNavigation(); onNavigate(item.key); }} className="side-menu" />
    </div>
    <div className="sidebar-footer">
      <Dropdown menu={accountMenu} placement="topLeft" trigger={["click"]}>
        <button type="button" className="sidebar-account" title={accountIdentityLabel(user)} aria-label={`账户菜单：${accountIdentityLabel(user)}`}>
          <Avatar size={28} icon={<UserRound size={16} />} />
          <span className="sidebar-account-copy"><strong>{user.username}</strong><small>{productIdentityLabel(user)}</small></span>
          <ChevronDown size={14} />
        </button>
      </Dropdown>
    </div>
  </div>;
}

type ShellTopbarProps = Readonly<{
  mode: ShellMode;
  desktop: boolean;
  focusedContext: FocusedShellContext;
  user: SessionUser;
  experience: ProductExperience;
  title: string;
  mock: boolean;
  accountMenu: MenuProps;
  onNavigate: (path: string) => void;
  onOpenNavigation: () => void;
}>;

function ShellAccount({ user, accountMenu }: Readonly<Pick<ShellTopbarProps, "user" | "accountMenu">>) {
  return <Space className="topbar-actions">
    <Dropdown menu={accountMenu} placement="bottomRight" trigger={["click"]}>
      <Button className="user-button"><Avatar size={24} icon={<UserRound size={15} />} /><span>{user.username}</span><ChevronDown size={14} /></Button>
    </Dropdown>
  </Space>;
}

export function ShellTopbar({ mode, desktop, focusedContext, user, experience, title, mock, accountMenu, onNavigate, onOpenNavigation }: ShellTopbarProps) {
  if (mode === "focused") {
    return <Layout.Header className="topbar focused-topbar">
      <div className="topbar-left">
        <Button type="text" icon={<ArrowLeft size={17} />} aria-label={focusedContext.backLabel} onClick={() => onNavigate(focusedContext.backPath)}>{focusedContext.backLabel}</Button>
        <strong>{focusedContext.title}</strong>
      </div>
      <ShellAccount user={user} accountMenu={accountMenu} />
    </Layout.Header>;
  }
  if (mode === "grading") {
    return <Layout.Header className="topbar immersive-topbar">
      <div className="topbar-left">
        <Space size="middle">
          <Button type="text" icon={<ArrowLeft size={17} />} aria-label="退出阅卷" onClick={() => onNavigate("/dashboard")}>退出阅卷</Button>
          <strong>{experienceLabel(experience)} · {title}</strong>
        </Space>
        {mock ? <MockBadge compact={true} /> : null}
      </div>
      <ShellAccount user={user} accountMenu={accountMenu} />
    </Layout.Header>;
  }
  if (desktop) return null;
  return <Layout.Header className="mobile-shellbar">
    <Button className="mobile-nav-button" type="text" icon={<MenuIcon size={20} />} aria-label="打开主导航" onClick={onOpenNavigation} />
    <div className="mobile-page-context"><strong>{title}</strong><span>{user.school || workspaceLabel(user, experience)}</span></div>
    {mock ? <MockBadge compact={true} /> : null}
  </Layout.Header>;
}
