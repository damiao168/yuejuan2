// @vitest-environment jsdom
import { act, type ComponentProps } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RegradeJobsPanel } from "../features/score-management/components/RegradeJobsPanel";
import { QualityDashboardPage } from "./QualityDashboardPage";

const mocks = vi.hoisted(() => ({ dashboard: vi.fn(), panel: vi.fn() }));
vi.mock("../api/qualityDashboard", () => ({ getExamQualityDashboard: mocks.dashboard }));
vi.mock("../features/score-management/components/RegradeJobsPanel", () => ({
  RegradeJobsPanel: (props: ComponentProps<typeof RegradeJobsPanel>) => { mocks.panel(props); return <section id="regrade-jobs" />; }
}));
// 本文件只核对复评面板的挂载条件和定位参数；质量看板的其余内容由 batch11 测试覆盖。
vi.mock("./QualityDashboardViews", () => ({
  QualityDashboardOverview: () => null, BackmarkBatchHistory: () => null, BackmarkSummaryContent: () => null,
  workflowActorLabel: () => "", workflowActorOptions: () => []
}));
vi.mock("../features/gold-papers", () => ({ GoldPaperManagerDrawer: () => null }));
vi.mock("../features/calibration", () => ({ CalibrationDrawer: () => null }));
vi.mock("../features/answer-groups", () => ({ AnswerGroupingDrawer: () => null }));
vi.mock("../components/PageState", () => ({ LoadingState: () => null, ErrorState: () => null }));
vi.mock("antd", () => {
  const Stub = () => null;
  return {
    App: { useApp: () => ({ message: { success: () => undefined, error: () => undefined, warning: () => undefined } }) },
    Alert: Stub, Button: Stub, Descriptions: Stub, Drawer: Stub, Empty: Stub, InputNumber: Stub, Select: Stub, Switch: Stub, Table: Stub, Tag: Stub,
    DatePicker: { RangePicker: Stub },
    Form: Object.assign(Stub, { useForm: () => [{}], Item: Stub }),
    Space: Object.assign(Stub, { Compact: Stub })
  };
});

describe("复核与异常页挂载题目级复评面板", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.dashboard.mockResolvedValue({ dashboard: { exam_id: "exam", gate: "ready", blocking: [], warnings: [], generated_at: "", questions: [] } });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    window.history.replaceState(null, "", "/");
  });

  it.each([
    { canManage: true, canManageScores: true, mounted: true },
    { canManage: true, canManageScores: false, mounted: false },
    { canManage: false, canManageScores: true, mounted: false }
  ])("review:manage=$canManage、score:manage=$canManageScores 时挂载=$mounted", async ({ canManage, canManageScores, mounted }) => {
    await act(async () => root.render(<QualityDashboardPage examId="exam" canManage={canManage} canManageScores={canManageScores} />));
    expect(mocks.panel).toHaveBeenCalledTimes(mounted ? 1 : 0);
    expect(Boolean(host.querySelector("#regrade-jobs"))).toBe(mounted);
  });

  it.each([
    { path: "/admin/exams/exam/quality", focused: undefined },
    { path: "/admin/exams/exam/quality?regrade=", focused: "" },
    { path: "/admin/exams/exam/quality?regrade=job-1", focused: "job-1" }
  ])("$path 交给面板的定位任务为 $focused", async ({ path, focused }) => {
    window.history.replaceState(null, "", path);
    const navigate = vi.fn();
    await act(async () => root.render(<QualityDashboardPage examId="exam" canManage canManageScores onNavigate={navigate} />));
    expect(mocks.panel).toHaveBeenCalledWith({ examId: "exam", focusedRegradeId: focused, onNavigate: navigate });
  });
});
