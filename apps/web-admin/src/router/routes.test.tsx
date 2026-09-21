import { describe, expect, it } from "vitest";
import {
  createRouteRegistry,
  hasRouteAccess,
  notFoundRoute,
  routeFromPath,
  shouldEnableMockRoutes
} from "./routes";
import type { SessionUser } from "../auth/session";

const mockPaths = ["/permissions", "/quality", "/review", "/settings"];

describe("route registration boundary", () => {
  it("registers only production-ready routes in the production registry", () => {
    const registry = createRouteRegistry(false);

    expect(registry.length).toBeGreaterThan(0);
    expect(registry.every((route) => route.productionReady && !route.mock)).toBe(true);
    expect(registry.some((route) => mockPaths.includes(route.path))).toBe(false);
  });

  it("keeps mock routes available only for explicit development and demo registries", () => {
    const registry = createRouteRegistry(true);

    expect(registry.filter((route) => route.mock).map((route) => route.path).sort()).toEqual(mockPaths);
  });

  it("does not resolve a mock deep link against the production registry", () => {
    const registry = createRouteRegistry(false);

    expect(routeFromPath("/review", registry)).toBe(notFoundRoute);
    expect(routeFromPath("/dashboard", registry).path).toBe("/dashboard");
  });

  it("keeps answer-sheet import reachable without duplicating it in the global navigation", () => {
    const capture = createRouteRegistry(false).find((route) => route.path === "/capture");
    expect(capture).toMatchObject({ productionReady: true, navigation: false });
  });

  it("shows AI chat only in the school administrator workspace", () => {
    const chat = createRouteRegistry(false).find((route) => route.path === "/ai-chat");
    expect(chat).toBeDefined();
    const base = {
      id: "user", username: "admin", displayName: "Admin", name: "Admin", role: "school_admin",
      roles: ["school_admin"], tenant: "demo", school: "Demo", currentExam: "",
      permissions: [], publicComputer: false,
      organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school"], gradeIds: [], classIds: [] }
    } satisfies SessionUser;
    expect(hasRouteAccess(base, chat!, "admin")).toBe(true);
    expect(hasRouteAccess({ ...base, roles: ["tenant_admin"], role: "tenant_admin" }, chat!, "admin")).toBe(false);
    expect(hasRouteAccess({ ...base, roles: ["platform_admin"], role: "platform_admin" }, chat!, "admin")).toBe(false);
  });

  it("cannot enable mock routes by setting the feature flag in production", () => {
    expect(shouldEnableMockRoutes({
      DEV: false,
      MODE: "production",
      VITE_ENABLE_MOCK_ROUTES: "true"
    })).toBe(false);
    expect(shouldEnableMockRoutes({ DEV: false, MODE: "demo" })).toBe(true);
  });
});
