import {
  createBrowserHistory,
  createRootRoute,
  createRoute,
  createRouter
} from "@tanstack/react-router";
import { AppShell } from "../AppShell";
import { listenForLegacyHashRoutes, migrateLegacyHashRoute } from "./location";

const rootRoute = createRootRoute({
  component: AppShell,
  notFoundComponent: () => null
});

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/$experience/dashboard"
});

const examListRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/$experience/exams"
});

const examWorkspaceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/$experience/exams/$examId/$section"
});

const managementExamRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/$experience/exams/overview/$kind/$examId"
});

// 旧入口由 AppShell 的路由注册表继续解析；通配匹配本身不授予页面访问权限。
const compatibilityRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/$"
});

const routeTree = rootRoute.addChildren([
  dashboardRoute,
  examListRoute,
  managementExamRoute,
  examWorkspaceRoute,
  compatibilityRoute
]);

migrateLegacyHashRoute();

export const appRouter = createRouter({
  routeTree,
  history: createBrowserHistory(),
  defaultPreload: "intent"
});

// 监听随模块生效，不受 React StrictMode 重挂载影响；热更新前注销，避免重复迁移同一次导航。
const stopLegacyHashListener = listenForLegacyHashRoutes();
import.meta.hot?.dispose(stopLegacyHashListener);

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof appRouter;
  }
}
