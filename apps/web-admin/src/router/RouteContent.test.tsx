// @vitest-environment jsdom
import { act, lazy, StrictMode, useEffect, useState, type ComponentType, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { RouteContent } from "./RouteContent";
import { LoadingState, ErrorState } from "../components/PageState";
import { usePageLoading, usePageVisible } from "../components/pageReadiness";

vi.mock("antd", async () => {
  const { createElement } = await import("react");
  return {
    Alert: () => createElement("div", { role: "alert" }, "加载失败"), Empty: () => null, Spin: () => createElement("span", { "data-loading-spinner": true }),
    Button: ({ children, onClick }: { children: ReactNode; onClick?: () => void }) => createElement("button", { onClick }, children),
    Result: ({ title, subTitle, extra }: { title: ReactNode; subTitle: ReactNode; extra: ReactNode }) =>
      createElement("section", { role: "alert" }, title, subTitle, extra)
  };
});

function controlledPage(label: string) {
  let resolve!: (module: { default: ComponentType }) => void;
  let reject!: (error: Error) => void;
  const module = new Promise<{ default: ComponentType }>((accept, fail) => { resolve = accept; reject = fail; });
  const Page = lazy(() => module);
  return { Page, resolve: () => resolve({ default: () => <section>{label}</section> }), reject };
}

describe("route content loading continuity", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks();
  });

  const visibleText = () => host.querySelector(".route-content-stage:not(.route-content-stage-pending)")?.textContent;
  const settle = () => act(async () => { await new Promise(resolve => setTimeout(resolve, 5)); });

  async function render(children: ReactNode, locationKey: string, title = "新栏目", scopeKey = "school:user:permissions:admin") {
    await act(async () => root.render(<RouteContent locationKey={locationKey} title={title} scopeKey={scopeKey}>{children}</RouteContent>));
    await settle();
  }

  it("保留旧页面节点和编辑内容直到新页面就绪，等待期间禁止操作旧页面", async () => {
    const next = controlledPage("新页面");
    await render(<section>旧页面<input defaultValue="原草稿" /></section>, "/old");
    const oldPage = host.querySelector("section");
    const draft = host.querySelector("input")!;
    draft.value = "尚未提交的草稿";

    await render(<next.Page />, "/next", "学生管理");
    expect(host.querySelector("section")).toBe(oldPage);
    expect(draft.value).toBe("尚未提交的草稿");
    expect(host.querySelector(".route-content-stage")?.hasAttribute("inert")).toBe(true);
    expect(host.querySelector(".route-content")?.getAttribute("aria-busy")).toBe("true");
    expect(host.querySelector("output")?.textContent).toBe("正在打开学生管理");
    expect(visibleText()).not.toContain("正在加载页面");

    await act(async () => next.resolve());
    await settle();
    expect(visibleText()).toBe("新页面");
    expect(host.querySelector(".route-content-stage")?.hasAttribute("inert")).toBe(false);
    expect(host.querySelector(".route-content")?.getAttribute("aria-busy")).toBe("false");
    expect(host.querySelector("output")).toBeNull();
  });

  it("首次进入没有旧页面时显示正常加载状态", async () => {
    const first = controlledPage("首个页面");
    await render(<first.Page />, "/first");
    expect(visibleText()).toContain("正在加载页面");
    expect(host.querySelector("output")).toBeNull();
    await act(async () => first.resolve());
    await settle();
    expect(visibleText()).toBe("首个页面");
  });

  it.each([true, false])("快速切换后只显示最新目标，前目标先返回=%s", async (previousResolvesFirst) => {
    const second = controlledPage("第二页");
    const third = controlledPage("第三页");
    await render(<section>第一页</section>, "/first");
    await render(<second.Page />, "/second", "第二页");
    await render(<third.Page />, "/third", "第三页");
    expect(visibleText()).toContain("第一页");
    expect(host.querySelector("output")?.textContent).toBe("正在打开第三页");
    if (previousResolvesFirst) {
      await act(async () => second.resolve());
      expect(visibleText()).toContain("第一页");
      expect(visibleText()).not.toContain("第二页");
    }
    await act(async () => third.resolve());
    await settle();
    expect(visibleText()).toBe("第三页");
    if (!previousResolvesFirst) await act(async () => second.resolve());
    expect(visibleText()).toBe("第三页");
  });

  it("身份或授权作用域变化时立即移除旧数据，即使新页面尚未加载", async () => {
    const next = controlledPage("新作用域页面");
    await render(<section>旧作用域业务数据</section>, "/old");
    await render(<next.Page />, "/next", "新栏目", "school:another-user:permissions:admin");
    expect(visibleText()).not.toContain("旧作用域业务数据");
    expect(visibleText()).toContain("正在加载页面");
    expect(host.querySelector("output")).toBeNull();
    await act(async () => next.resolve());
    await settle();
    expect(visibleText()).toBe("新作用域页面");
  });

  it("模块加载失败进入正常错误边界，后续导航仍能恢复", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const broken = controlledPage("不会显示的页面");
    await render(<section>旧页面</section>, "/old");
    await render(<broken.Page />, "/broken");
    await act(async () => broken.reject(new Error("module unavailable")));
    await settle();
    expect(visibleText()).toContain("页面出现异常");
    expect(visibleText()).not.toContain("旧页面");
    expect(host.querySelector(".route-content-stage")?.hasAttribute("inert")).toBe(false);
    expect(host.querySelector("output")).toBeNull();
    await render(<section>已恢复页面</section>, "/recovered");
    expect(visibleText()).toBe("已恢复页面");
  });

  function dataPage(label: string) {
    let finish!: (failed?: boolean) => void;
    let refresh!: () => void;
    const mounted = vi.fn();
    const Page = () => {
      const [status, setStatus] = useState("loading");
      finish = failed => setStatus(failed ? "error" : "ready");
      refresh = () => setStatus("loading");
      useEffect(() => { mounted(); }, []);
      if (status === "loading") return <LoadingState label={`读取${label}`} />;
      if (status === "error") return <ErrorState message="读取失败" />;
      return <section>{label}<input defaultValue="目标草稿" /></section>;
    };
    return { Page, finish: (failed = false) => finish(failed), refresh: () => refresh(), mounted };
  }

  it("模块完成后继续保留旧画面，等首屏数据完成再展示同一挂载节点", async () => {
    const data = dataPage("学生数据");
    let finishModule!: () => void;
    const Next = lazy(() => new Promise<{ default: ComponentType }>(resolve => {
      finishModule = () => resolve({ default: data.Page });
    }));
    await render(<section>原页面</section>, "/old");
    await render(<Next />, "/next");
    await act(async () => finishModule());
    await settle();
    expect(visibleText()).toBe("原页面");
    expect(host.querySelector(".route-content-stage-pending")?.textContent).toContain("读取学生数据");
    const candidate = host.querySelector(".route-content-stage-pending");
    await act(async () => data.finish());
    await settle();
    expect(visibleText()).toBe("学生数据");
    expect(host.querySelector(".route-content-stage")).toBe(candidate);
    expect(data.mounted).toHaveBeenCalledTimes(1);
    await act(async () => data.refresh());
    await settle();
    expect(visibleText()).toContain("读取学生数据");
    expect(host.textContent).not.toContain("原页面");
  });

  it("父加载状态交给子组件及effect发起的加载时不会提前切页", async () => {
    const child = dataPage("子数据");
    let finishParent!: () => void;
    function Parent() {
      const [ready, setReady] = useState(false);
      finishParent = () => setReady(true);
      return ready ? <child.Page /> : <LoadingState label="父数据" />;
    }
    await render(<section>原页面</section>, "/old");
    await render(<Parent />, "/parent");
    await act(async () => finishParent());
    await settle();
    expect(visibleText()).toBe("原页面");
    await act(async () => child.finish());
    await settle();
    expect(visibleText()).toBe("子数据");

    function EffectLoader() {
      const [loading, setLoading] = useState(false);
      useEffect(() => { setLoading(true); }, []);
      return loading ? <LoadingState /> : <section>取数前一帧</section>;
    }
    await render(<EffectLoader />, "/effect");
    expect(visibleText()).toBe("子数据");
  });

  it("加载期间返回旧栏目保留草稿，之后晚到结果不影响显示", async () => {
    const next = dataPage("下一页");
    await render(<section>原页面<input defaultValue="草稿" /></section>, "/old");
    const input = host.querySelector("input")!;
    input.value = "编辑中";
    await render(<next.Page />, "/next");
    await render(<section>原页面<input defaultValue="草稿" /></section>, "/old");
    expect(host.querySelector("input")).toBe(input);
    expect(input.value).toBe("编辑中");
    expect(host.querySelectorAll(".route-content-stage")).toHaveLength(1);
    await act(async () => next.finish());
    await settle();
    expect(visibleText()).toBe("原页面");
  });

  it("首屏请求失败后显示错误，连续导航只保留当前候选页", async () => {
    const second = dataPage("第二页");
    const third = dataPage("第三页");
    await render(<section>原页面</section>, "/old");
    await render(<second.Page />, "/second");
    await render(<third.Page />, "/third");
    expect(host.querySelectorAll(".route-content-stage")).toHaveLength(2);
    await act(async () => second.finish());
    await settle();
    expect(visibleText()).toBe("原页面");
    await act(async () => third.finish(true));
    await settle();
    expect(visibleText()).toBe("加载失败");
  });

  it("同组件查询变化和普通重渲染保留编辑内容及挂载次数", async () => {
    const mounted = vi.fn();
    function Editor({ query }: { query: string }) {
      useEffect(() => { mounted(); }, []);
      return <section>{query}<input defaultValue="草稿" /></section>;
    }
    await render(<Editor query="甲" />, "/editor?q=a");
    const input = host.querySelector("input")!;
    input.value = "编辑中";
    await render(<Editor query="乙" />, "/editor?q=b");
    await render(<Editor query="丙" />, "/editor?q=b");
    expect(visibleText()).toBe("丙");
    expect(host.querySelector("input")).toBe(input);
    expect(input.value).toBe("编辑中");
    expect(mounted).toHaveBeenCalledTimes(1);
  });

  it("共用考试外壳但子栏目不同仍等待新栏目的数据", async () => {
    const data = dataPage("新栏目");
    function Workspace({ next = false }: { next?: boolean }) {
      return next ? <data.Page /> : <section>旧栏目</section>;
    }
    await render(<Workspace />, "/exams/1/students");
    await render(<Workspace next />, "/exams/1/settings");
    expect(visibleText()).toBe("旧栏目");
    await act(async () => data.finish());
    await settle();
    expect(visibleText()).toBe("新栏目");
  });

  it("StrictMode重复挂载清理不会漏计加载，隐藏候选页不激活浮层", async () => {
    let finish!: () => void;
    function Candidate() {
      const [loading, setLoading] = useState(true);
      finish = () => setLoading(false);
      usePageLoading(loading);
      return <section>{usePageVisible() ? "浮层可见" : "候选隐藏"}</section>;
    }
    await render(<section>原页面</section>, "/old");
    await render(<StrictMode><Candidate /></StrictMode>, "/next");
    expect(visibleText()).toBe("原页面");
    expect(host.querySelector(".route-content-stage-pending")?.textContent).toBe("候选隐藏");
    await act(async () => finish());
    await settle();
    expect(visibleText()).toBe("浮层可见");
  });

});
