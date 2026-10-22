// @vitest-environment jsdom
import { createBrowserHistory, type RouterHistory } from "@tanstack/react-router";
import { afterEach, describe, expect, it, vi } from "vitest";
import { currentRoutePath, legacyHashRoute, listenForLegacyHashRoutes, migrateLegacyHashRoute } from "./location";
import { hashQueryParam, replaceRouteQuery, routeQueryParam, routeSearchParams } from "./query";

let stopLegacyHashListener: (() => void) | undefined;
let browserHistory: RouterHistory | undefined;
afterEach(() => {
  stopLegacyHashListener?.();
  stopLegacyHashListener = undefined;
  browserHistory?.destroy();
  browserHistory = undefined;
  window.history.replaceState(null, "", "/");
});

describe("browser route entry and query compatibility", () => {
  it.each(["admin", "teacher", "auditor", "student"])("converts old %s links without losing route parameters", experience => {
    window.history.replaceState({ saved: true }, "", `/?tenant=demo#/${experience}/exams/exam%201/scores?task=task-1&status=pending`);
    migrateLegacyHashRoute();
    expect(window.location.pathname).toBe(`/${experience}/exams/exam%201/scores`);
    expect(window.location.hash).toBe("");
    expect(routeQueryParam("tenant")).toBe("demo");
    expect(routeQueryParam("task")).toBe("task-1");
    expect(window.history.state).toEqual({ saved: true });
  });

  it("preserves clean page paths, filters and history state when updating query", () => {
    window.history.replaceState({ key: "unchanged" }, "", "/admin/members/students?class=c1&status=active");
    replaceRouteQuery(params => { params.set("class", "c2"); params.delete("status"); });
    expect(currentRoutePath()).toBe("/admin/members/students?class=c2");
    expect(window.location.hash).toBe("");
    expect(routeSearchParams().get("class")).toBe("c2");
    expect(hashQueryParam("class")).toBe("c2");
    expect(window.history.state).toEqual({ key: "unchanged" });
  });

  it("reads an old entry before router initialization and writes back a clean path", () => {
    window.history.replaceState(null, "", "/#/admin/platform/model-config?tenant_id=school&tab=bindings");
    expect(routeQueryParam("tenant_id")).toBe("school");
    replaceRouteQuery(params => params.set("tab", "governance"));
    expect(window.location.pathname).toBe("/admin/platform/model-config");
    expect(window.location.hash).toBe("");
    expect(routeQueryParam("tab")).toBe("governance");
  });

  it.each(["#footnote-1", "#page=3", "#wechat_redirect", "#//external.test/admin", "#/\\external.test"])("does not interpret %s as a legacy page", hash => {
    expect(legacyHashRoute({ pathname: "/", search: "", hash, origin: window.location.origin })).toBeUndefined();
  });

  it.each(["/activate?token=one-time", "/recover?token=recovery", "/school/demo?tenant=demo"])("leaves authentication entry %s intact", path => {
    window.history.replaceState(null, "", `${path}#/admin/dashboard`);
    migrateLegacyHashRoute();
    expect(`${window.location.pathname}${window.location.search}`).toBe(path);
  });

  it("migrates a same-document old link after Browser History starts and notifies its subscribers", async () => {
    window.history.replaceState(null, "", "/?tenant=demo&tab=models");
    browserHistory = createBrowserHistory();
    const state = window.history.state;
    const notified = vi.fn();
    browserHistory.subscribe(notified);
    stopLegacyHashListener = listenForLegacyHashRoutes();
    // 浏览器片段跳转先发生，再由 hashchange 兼容层替换；用现有 Router state 模拟它保留的历史位置。
    window.history.replaceState(state, "", "/?tenant=demo&tab=models#/admin/platform/model-config?tab=bindings");
    const length = window.history.length;
    notified.mockClear();
    window.dispatchEvent(new HashChangeEvent("hashchange"));
    expect(window.location.pathname).toBe("/admin/platform/model-config");
    expect(window.location.hash).toBe("");
    expect(routeQueryParam("tenant")).toBe("demo");
    expect(routeQueryParam("tab")).toBe("bindings");
    expect(window.history.state).toEqual(state);
    expect(window.history.length).toBe(length);
    expect(browserHistory.location.href).toBe("/admin/platform/model-config?tenant=demo&tab=bindings");
    expect(notified).toHaveBeenCalledTimes(1);
  });

  it.each(["/#footnote-1", "/#page=3", "/#wechat_redirect", "/#//external.test/admin", "/activate?token=one-time#/admin/dashboard", "/admin/dashboard#/teacher/grading"])("leaves non-legacy same-document location %s intact", path => {
    stopLegacyHashListener = listenForLegacyHashRoutes();
    window.history.replaceState({ saved: true }, "", path);
    window.dispatchEvent(new HashChangeEvent("hashchange"));
    expect(`${window.location.pathname}${window.location.search}${window.location.hash}`).toBe(path);
    expect(window.history.state).toEqual({ saved: true });
  });

  it("removes the same-document listener before hot module replacement", () => {
    stopLegacyHashListener = listenForLegacyHashRoutes();
    stopLegacyHashListener();
    window.history.replaceState(null, "", "/#/admin/dashboard");
    window.dispatchEvent(new HashChangeEvent("hashchange"));
    expect(window.location.pathname).toBe("/");
    expect(window.location.hash).toBe("#/admin/dashboard");
  });
});
