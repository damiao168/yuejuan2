import {
  createHashHistory,
  createRootRoute,
  createRoute,
  createRouter
} from "@tanstack/react-router";
import { AppShell } from "../AppShell";

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

// 旧入口由 AppShell 的路由注册表继续解析；通配匹配本身不授予页面访问权限。
const compatibilityRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/$"
});

const routeTree = rootRoute.addChildren([
  dashboardRoute,
  examListRoute,
  examWorkspaceRoute,
  compatibilityRoute
]);

export const appRouter = createRouter({
  routeTree,
  history: createHashHistory(),
  defaultPreload: "intent"
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof appRouter;
  }
}
