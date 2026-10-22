// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GradingManagementNav } from "./GradingManagementNav";
import { ReviewerProgressView } from "./ReviewerProgressView";

afterEach(cleanup);

describe("阅卷管理常驻入口", () => {
  it("在安排教师页直接打开答卷结果，并保留教师进度入口", () => {
    const onViewChange = vi.fn();
    const onOpenResults = vi.fn();
    render(<GradingManagementNav view="assignment" hasExamOverview canViewResults canOpenResults resultsOpen={false} resultsLoading={false} hasOwnWork onViewChange={onViewChange} onOpenResults={onOpenResults} />);
    expect(screen.getByRole("button", { name: "安排教师" }).getAttribute("aria-current")).toBe("page");
    for (const name of ["阅卷概览", "答卷结果", "教师进度", "我的阅卷"]) expect(screen.getByRole("button", { name })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "答卷结果" }));
    expect(onOpenResults).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "教师进度" }));
    expect(onViewChange).toHaveBeenCalledWith("progress");
  });

  it("没有本场考试或评分权限时只展示可用入口", () => {
    render(<GradingManagementNav view="assignment" hasExamOverview={false} canViewResults={false} canOpenResults={false} resultsOpen={false} resultsLoading={false} hasOwnWork={false} onViewChange={vi.fn()} onOpenResults={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "阅卷概览" })).toBeNull();
    expect(screen.queryByRole("button", { name: "答卷结果" })).toBeNull();
    expect(screen.queryByRole("button", { name: "我的阅卷" })).toBeNull();
    expect(screen.queryByRole("button", { name: "阅卷设置" })).toBeNull();
    expect(screen.getByRole("button", { name: "安排教师" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "教师进度" })).toBeTruthy();
  });

  it("尚未开始评分时保留结果入口并说明可用条件", () => {
    const onOpenResults = vi.fn();
    render(<GradingManagementNav view="overview" hasExamOverview canViewResults canOpenResults={false} resultsOpen={false} resultsLoading={false} hasOwnWork={false} onViewChange={vi.fn()} onOpenResults={onOpenResults} />);
    const results = screen.getByRole("button", { name: "答卷结果" }) as HTMLButtonElement;
    expect(results.disabled).toBe(true);
    expect(results.title).toBe("开始评分后可查看答卷结果");
    fireEvent.click(results);
    expect(onOpenResults).not.toHaveBeenCalled();
  });

  it("答卷结果作为独立页面标记当前入口", () => {
    render(<GradingManagementNav view="results" hasExamOverview canViewResults canOpenResults resultsOpen resultsLoading={false} hasOwnWork={false} onViewChange={vi.fn()} onOpenResults={vi.fn()} />);
    const results = screen.getByRole("button", { name: "答卷结果" });
    expect(results.getAttribute("aria-current")).toBe("page");
    expect(results.hasAttribute("aria-expanded")).toBe(false);
    expect(screen.getByRole("button", { name: "阅卷概览" }).hasAttribute("aria-current")).toBe(false);
  });

  it("有阅卷设置权限时展示专页入口并切换到settings", () => {
    const onViewChange = vi.fn();
    render(<GradingManagementNav view="settings" hasExamOverview canViewResults canOpenResults resultsOpen={false} resultsLoading={false} hasOwnWork={false} canManageSettings onViewChange={onViewChange} onOpenResults={vi.fn()} />);
    const settings = screen.getByRole("button", { name: "阅卷设置" });
    expect(settings.getAttribute("aria-current")).toBe("page");
    fireEvent.click(settings);
    expect(onViewChange).toHaveBeenCalledWith("settings");
  });
});

describe("教师进度专页", () => {
  const reviewers = [{ id: "teacher", name: "张老师", total: 10, completed: 4, active: 6, percent: 40 }];
  it("复用已有汇总展示教师完成数和待办，并允许刷新", () => {
    const onRefresh = vi.fn(async () => undefined);
    render(<ReviewerProgressView reviewers={reviewers} progressComplete loading={false} error={null} gradersError={null} onRefresh={onRefresh} />);
    expect(screen.getByRole("region", { name: "教师进度" })).toBeTruthy();
    expect(screen.getByText("张老师")).toBeTruthy();
    expect(screen.getByText("已完成 4 / 10 项")).toBeTruthy();
    expect(screen.getByText("待完成 6 项")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    expect(onRefresh).toHaveBeenCalledOnce();
  });

  it("汇总未完整时不把空数据呈现为没有待办", () => {
    render(<ReviewerProgressView reviewers={[]} progressComplete={false} loading={false} error={null} gradersError={null} onRefresh={vi.fn(async () => undefined)} />);
    expect(screen.getByRole("status").textContent).toContain("尚未完整加载");
    expect(screen.queryByText(/暂无阅卷教师/)).toBeNull();
  });
});
