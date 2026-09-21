import type { ReactNode } from "react";
import {
  Activity,
  BarChart3,
  BrainCircuit,
  MessageCircle,
  BookOpenCheck,
  Building2,
  Cable,
  ClipboardCheck,
  FileText,
  Gauge,
  Gavel,
  GraduationCap,
  Home,
  Inbox,
  Layers3,
  LockKeyhole,
  ScanLine,
  ScrollText,
  ServerCog,
  Settings,
  Rocket,
  Sparkles,
  UserCog,
  Users
} from "lucide-react";
import type { ViewKey } from "../types";
import { hasAnyPermission, hasEveryPermission, type SessionUser } from "../auth/session";
import type { ProductExperience } from "./experience";
import { canonicalPathFromPath } from "./experience";
import { workspacesForExperience, type Workspace } from "../workspaces/registry";

interface RoutePresentation {
  title: string;
  group: string;
}

export interface AppRoute {
  key: ViewKey | "permissions" | "settings";
  path: string;
  title: string;
  group: string;
  icon: ReactNode;
  permissions: string[];
  anyPermissions?: string[];
  allowedRoles?: string[];
  excludedRoles?: string[];
  navigation?: boolean;
  mock: boolean;
  productionReady: boolean;
  replacementPath?: string;
  experiences: Partial<Record<ProductExperience, RoutePresentation>>;
  navigationExperiences?: ProductExperience[];
  workspaces?: Workspace[];
}

const productionRouteDefinitions: AppRoute[] = [
  {
    key: "questionBank", path: "/question-bank", title: "题库", group: "题库与组卷", icon: <BookOpenCheck size={18} />, permissions: ["question_bank:read"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"], workspaces: ["tenant_admin", "school_admin", "teacher"],
    experiences: { admin: { title: "题库", group: "题库与组卷" }, teacher: { title: "题库", group: "教学工作" } }
  },
  {
    key: "dashboard", path: "/dashboard", title: "工作总览", group: "工作台", icon: <Home size={18} />, permissions: [], mock: false, productionReady: true,
    workspaces: ["platform", "tenant_admin", "school_admin", "exam_owner", "teacher", "grader", "arbitrator", "auditor", "student"],
    experiences: { admin: { title: "工作台", group: "工作台" }, teacher: { title: "我的工作", group: "工作台" }, auditor: { title: "审计总览", group: "工作台" }, student: { title: "我的学习", group: "工作台" } }
  },
  {
    key: "schoolAiChat", path: "/ai-chat", title: "AI 对话", group: "工作台", icon: <MessageCircle size={18} />, permissions: [],
    allowedRoles: ["school_admin"], workspaces: ["school_admin"], mock: false, productionReady: true,
    experiences: { admin: { title: "AI 对话", group: "工作台" } }
  },
  {
    key: "sessions", path: "/account/sessions", title: "账户安全", group: "账号", icon: <LockKeyhole size={18} />, permissions: [],
    navigation: false, mock: false, productionReady: true,
    workspaces: ["platform", "tenant_admin", "school_admin", "exam_owner", "teacher", "grader", "arbitrator", "auditor", "student"],
    experiences: { admin: { title: "账户安全", group: "账号" }, teacher: { title: "账户安全", group: "账号" }, auditor: { title: "账户安全", group: "账号" }, student: { title: "账户安全", group: "账号" } }
  },
  {
    key: "platformGettingStarted", path: "/platform/getting-started", title: "首次启用", group: "平台管理", icon: <Rocket size={18} />, permissions: ["tenant:manage"], allowedRoles: ["platform_admin"], navigation: false, mock: false, productionReady: true,
    workspaces: ["platform"], experiences: { admin: { title: "首次启用", group: "平台管理" } }
  },
  {
    key: "platformSchools", path: "/platform/schools", title: "学校管理", group: "平台管理", icon: <Building2 size={18} />, permissions: ["tenant:manage"], allowedRoles: ["platform_admin"], mock: false, productionReady: true,
    workspaces: ["platform"], experiences: { admin: { title: "学校管理", group: "平台管理" } }
  },
  {
    key: "platformModelConfig", path: "/platform/model-config", title: "模型配置", group: "平台管理", icon: <Cable size={18} />, permissions: ["model:provider:manage"], allowedRoles: ["platform_admin"], mock: false, productionReady: true,
    workspaces: ["platform"], experiences: { admin: { title: "模型配置", group: "平台管理" } }
  },
  {
    key: "exams", path: "/exams", title: "考试管理", group: "考试组织", icon: <ClipboardCheck size={18} />, permissions: ["exam:manage"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin", "exam_owner"], experiences: { admin: { title: "考试列表", group: "考试管理" } }
  },
  {
    key: "examCreate", path: "/exams/new", title: "新建考试", group: "考试组织", icon: <ClipboardCheck size={18} />, permissions: ["exam:manage"], navigation: false, mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin", "exam_owner"], experiences: { admin: { title: "新建考试", group: "考试管理" } }
  },
  {
    key: "papers", path: "/papers", title: "试卷管理", group: "考试组织", icon: <FileText size={18} />, permissions: ["exam:manage", "file:manage"], navigation: false, mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin", "exam_owner"], experiences: { admin: { title: "试卷管理", group: "考试管理" } }
  },
  {
    key: "capture",
    path: "/capture",
    title: "答卷采集",
    group: "业务",
    icon: <ScanLine size={18} />,
    permissions: ["submission:manage", "file:manage", "ocr:manage", "segment:manage"],
    excludedRoles: ["platform_admin"],
    navigation: false,
    mock: false,
    productionReady: true,
    workspaces: ["tenant_admin", "school_admin", "exam_owner"], experiences: { admin: { title: "答题卡导入", group: "考试管理" } }
  },
  {
    key: "grading",
    path: "/grading",
    title: "阅卷",
    group: "业务",
    icon: <Sparkles size={18} />,
    permissions: [],
    anyPermissions: ["review:manage", "review:work"],
    excludedRoles: ["platform_admin"],
    mock: false,
    productionReady: true,
    workspaces: ["tenant_admin", "school_admin", "exam_owner", "teacher", "grader"],
    experiences: { admin: { title: "阅卷管理", group: "阅卷与成绩" }, teacher: { title: "我的阅卷", group: "阅卷工作" } }
  },
  {
    key: "arbitration", path: "/arbitration", title: "质量与仲裁", group: "阅卷与质量", icon: <Gavel size={18} />, permissions: [], anyPermissions: ["arbitration:manage", "arbitration:work"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin", "exam_owner", "arbitrator"],
    experiences: { admin: { title: "复核与异常", group: "阅卷与成绩" }, teacher: { title: "我的仲裁", group: "阅卷工作" } }
  },
  {
    key: "scores", path: "/scores", title: "成绩发布", group: "结果管理", icon: <Gauge size={18} />, permissions: ["score:manage", "exam:manage", "submission:manage"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin", "exam_owner"], experiences: { admin: { title: "成绩发布", group: "阅卷与成绩" } }
  },
  {
    key: "reports", path: "/reports", title: "统计报告", group: "结果管理", icon: <BarChart3 size={18} />, permissions: ["report:read"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin", "exam_owner", "teacher"],
    experiences: { admin: { title: "成绩分析", group: "阅卷与成绩" }, teacher: { title: "班级成绩", group: "教学工作" } }
  },
  {
    key: "appeals", path: "/appeals", title: "申诉管理", group: "结果管理", icon: <Inbox size={18} />, permissions: ["appeal:read"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin", "exam_owner", "teacher"],
    experiences: { admin: { title: "申诉处理", group: "阅卷与成绩" }, teacher: { title: "学生反馈", group: "教学工作" } }
  },
  {
    key: "membersStudents", path: "/members/students", title: "学生管理", group: "学校管理", icon: <Users size={18} />, permissions: ["org:manage"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin"], experiences: { admin: { title: "学生管理", group: "成员管理" } }
  },
  {
    key: "membersClasses", path: "/members/classes", title: "年级与班级", group: "学校管理", icon: <GraduationCap size={18} />, permissions: ["org:manage"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin"], experiences: { admin: { title: "年级与班级", group: "成员管理" } }
  },
  {
    key: "membersTeachers", path: "/members/teachers", title: "阅卷教师", group: "学校管理", icon: <UserCog size={18} />, permissions: ["org:manage"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin"], experiences: { admin: { title: "阅卷教师", group: "成员管理" } }
  },
  {
    key: "organization", path: "/organization/setup", title: "学校初始化", group: "学校管理", icon: <Settings size={18} />, permissions: ["org:manage"], navigation: false, mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    workspaces: ["tenant_admin", "school_admin"], experiences: { admin: { title: "学校初始化", group: "学校管理" } }
  },
  {
    key: "modelGovernance", path: "/system/models", title: "模型治理", group: "系统管理", icon: <BrainCircuit size={18} />, permissions: ["model:read"], mock: false, productionReady: true,
    allowedRoles: ["platform_admin"],
    workspaces: ["platform"], experiences: { admin: { title: "模型治理", group: "系统管理" } }
  },
  {
    key: "subjectiveGradingBatches", path: "/grading/subjective-batches", title: "主观题批次", group: "阅卷与质量", icon: <Sparkles size={18} />, permissions: ["grading:manage"], mock: false, productionReady: true,
    excludedRoles: ["platform_admin"],
    navigation: false,
    experiences: { admin: { title: "主观题批次", group: "阅卷与质量" } }
  },
  {
    key: "systemStatus", path: "/system/status", title: "系统运维", group: "系统管理", icon: <ServerCog size={18} />, permissions: ["system:read"], allowedRoles: ["platform_admin"], mock: false, productionReady: true,
    workspaces: ["platform"], experiences: { admin: { title: "系统运维", group: "系统管理" } }
  },
  {
    key: "audit", path: "/audit", title: "操作审计", group: "系统管理", icon: <ScrollText size={18} />, permissions: ["audit:read"], mock: false, productionReady: true,
    workspaces: ["platform", "tenant_admin", "school_admin", "auditor"], experiences: { admin: { title: "操作审计", group: "设置" }, auditor: { title: "操作审计", group: "审计" } }
  }
];

const mockRouteDefinitions: AppRoute[] = [
  {
    key: "review",
    path: "/review",
    title: "人工复核",
    group: "阅卷",
    icon: <BookOpenCheck size={18} />,
    permissions: ["review:manage"],
    navigation: false,
    mock: true,
    productionReady: false,
    replacementPath: "/grading",
    experiences: { teacher: { title: "我的复核", group: "阅卷工作" } }
  },
  {
    key: "quality", path: "/quality", title: "质量控制", group: "阅卷与质量", icon: <Activity size={18} />, permissions: ["quality:read"], navigation: false, mock: true, productionReady: false,
    experiences: { admin: { title: "质量控制", group: "阅卷与质量" } }
  },
  {
    key: "permissions", path: "/permissions", title: "用户权限", group: "系统管理", icon: <Users size={18} />, permissions: ["org:manage"], mock: true, productionReady: false,
    experiences: { admin: { title: "用户权限", group: "系统管理" } }
  },
  {
    key: "settings", path: "/settings", title: "系统设置", group: "系统管理", icon: <Settings size={18} />, permissions: ["system:manage"], mock: true, productionReady: false,
    experiences: { admin: { title: "系统设置", group: "系统管理" } }
  }
];

export function createRouteRegistry(includeMockRoutes: boolean): AppRoute[] {
  return includeMockRoutes
    ? [...productionRouteDefinitions, ...mockRouteDefinitions]
    : [...productionRouteDefinitions];
}

export const examWorkspaceRoute: AppRoute = {
  key: "examWorkspace",
  path: "/exams/:examId/overview",
  title: "考试工作区",
  group: "工作",
  icon: <ClipboardCheck size={18} />,
  permissions: ["exam:manage"],
  excludedRoles: ["platform_admin"],
  navigation: false,
  mock: false,
  productionReady: true,
  experiences: { admin: { title: "考试工作区", group: "考试组织" } }
};

export const examWorkspaceSections: Record<ProductExperience, readonly string[]> = {
  admin: ["overview", "students", "paper", "questions", "template", "capture", "processing", "grading", "quality", "scores", "appeals", "reports", "settings"],
  teacher: [], auditor: [], student: []
};

export function hasExamWorkspaceSectionAccess(experience: ProductExperience, section: string): boolean {
  return examWorkspaceSections[experience].includes(section);
}

export const forbiddenRoute: AppRoute = {
  key: "system",
  path: "/forbidden",
  title: "无权限",
  group: "系统",
  icon: <LockKeyhole size={18} />,
  permissions: [],
  mock: false,
  productionReady: true,
  experiences: { admin: { title: "无权限", group: "系统" }, teacher: { title: "无权限", group: "系统" }, auditor: { title: "无权限", group: "系统" }, student: { title: "无权限", group: "系统" } }
};

export const notFoundRoute: AppRoute = {
  key: "system",
  path: "/not-found",
  title: "未找到",
  group: "系统",
  icon: <Layers3 size={18} />,
  permissions: [],
  mock: false,
  productionReady: true,
  experiences: { admin: { title: "未找到", group: "系统" }, teacher: { title: "未找到", group: "系统" }, auditor: { title: "未找到", group: "系统" }, student: { title: "未找到", group: "系统" } }
};

export interface RouteBuildEnvironment {
  DEV: boolean;
  MODE: string;
  VITE_ENABLE_MOCK_ROUTES?: string;
}

export function shouldEnableMockRoutes(environment: RouteBuildEnvironment): boolean {
  if (environment.MODE === "production") {
    return false;
  }
  return environment.DEV
    || environment.MODE === "demo"
    || environment.VITE_ENABLE_MOCK_ROUTES === "true";
}

const includeMockRoutes = import.meta.env.MODE !== "production"
  && (import.meta.env.DEV
    || import.meta.env.MODE === "demo"
    || import.meta.env.VITE_ENABLE_MOCK_ROUTES === "true");

export function mockRoutesEnabled(): boolean {
  return includeMockRoutes;
}

// This is the runtime registry, not a visibility filter over every route.
// Production mode therefore cannot resolve or navigate to mock-only pages.
export const routes = includeMockRoutes
  ? createRouteRegistry(true)
  : productionRouteDefinitions;

export function routePresentation(route: AppRoute, experience: ProductExperience): RoutePresentation {
  return route.experiences[experience] ?? { title: route.title, group: route.group };
}

export function visibleRoutes(experience: ProductExperience): AppRoute[] {
  return routes.filter((route) => Boolean(route.experiences[experience])
    && (route.navigation !== false || route.navigationExperiences?.includes(experience)));
}

export function hasRouteAccess(user: SessionUser | null, route: AppRoute, experience: ProductExperience): boolean {
  const workspaces = user ? workspacesForExperience(user, experience) : [];
  return Boolean(route.experiences[experience])
    && hasEveryPermission(user, route.permissions)
    && (!route.anyPermissions || hasAnyPermission(user, route.anyPermissions))
    && (!route.allowedRoles || Boolean(user && route.allowedRoles.some((role) => user.roles.includes(role))))
    && (!route.excludedRoles || !user || !route.excludedRoles.some((role) => user.roles.includes(role)))
    && (!route.workspaces || workspaces.some((workspace) => route.workspaces?.includes(workspace)));
}

export function routeFromPath(pathname: string, registry: readonly AppRoute[] = routes): AppRoute {
  const canonicalPath = canonicalPathFromPath(pathname);
  if (examWorkspaceFromPath(canonicalPath)) {
    return examWorkspaceRoute;
  }
  return registry.find((route) => route.path === canonicalPath) ?? notFoundRoute;
}

export function examWorkspaceFromPath(pathname: string): { examId: string; section: string } | null {
  const match = canonicalPathFromPath(pathname).match(/^\/exams\/([^/]+)\/([^/?]+)$/);
  if (!match) {
    return null;
  }
  try {
    return { examId: decodeURIComponent(match[1]), section: match[2] };
  } catch {
    return null;
  }
}

export function routeGroups(experience: ProductExperience) {
  const groups = Array.from(new Set(visibleRoutes(experience).map((route) => routePresentation(route, experience).group)));
  if (experience !== "admin") {
    return groups;
  }
  const preferredOrder = ["工作台", "成员管理", "考试管理", "阅卷与成绩", "设置"];
  return [
    ...preferredOrder.filter((group) => groups.includes(group)),
    ...groups.filter((group) => !preferredOrder.includes(group))
  ];
}

export function pathFromHash(): string {
  const hash = window.location.hash.replace(/^#/, "");
  return hash || "/dashboard";
}
