// @vitest-environment jsdom
import type { ChangeEvent, ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AddGradingEvaluationObservationRequest, GradingEvaluationRun } from "../../api/scoringAssurance";
import { ScoringAssuranceWorkspace } from "./ScoringAssuranceWorkspace";

const mocks = vi.hoisted(() => ({
  list: vi.fn(), write: vi.fn(), school: vi.fn(), success: vi.fn(), error: vi.fn()
}));
const appContext = vi.hoisted(() => ({ message: { success: mocks.success, error: mocks.error } }));
vi.mock("../../api/scoringAssurance", async () => ({
  ...await vi.importActual<typeof import("../../api/scoringAssurance")>("../../api/scoringAssurance"),
  setScoringAssuranceSchool: mocks.school,
  listGradingEvaluations: mocks.list,
  listModelCalibrations: async () => ({ calibrations: [] }),
  listGradingEvaluationSliceMetrics: async () => ({ slice_metrics: [] }),
  listGradingEvaluationResponseDifficulty: async () => ({ response_difficulty: [] }),
  getGradingEvaluationQualitySummary: async () => ({ quality_summary: undefined }),
  addGradingEvaluationObservation: mocks.write
}));
vi.mock("./ScoringAssuranceViews", () => ({
  CalibrationDetail: () => null, DisagreementQueue: () => null,
  EligibilityPolicyPanel: () => null, EvaluationDetail: () => null
}));
vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  const Input = Object.assign(() => <input />, {
    TextArea: ({ value, onChange, disabled }: { value?: string; onChange?: (event: ChangeEvent<HTMLTextAreaElement>) => void; disabled?: boolean }) => <textarea value={value} onChange={onChange} disabled={disabled} />
  });
  return {
    App: { useApp: () => appContext },
    Button: ({ children, disabled, onClick }: { children?: ReactNode; disabled?: boolean; onClick?: () => void }) => <button type="button" disabled={disabled} onClick={onClick}>{children}</button>,
    Drawer: ({ title, children, open }: { title: string; children?: ReactNode; open: boolean }) => open ? <section aria-label={title}>{children}</section> : null,
    Alert: ({ message, description }: { message?: ReactNode; description?: ReactNode }) => <div>{message}{description}</div>,
    Space: Object.assign(Box, { Compact: Box }), Card: Box, Divider: Box,
    Empty: Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: "simple" }),
    Form: Object.assign(Box, { Item: Box, useForm: () => [{ setFieldsValue: vi.fn(), validateFields: vi.fn(), resetFields: vi.fn() }] }),
    Input, InputNumber: () => null, Select: () => null, Table: () => null,
    Tag: Box, Typography: { Text: Box }, Descriptions: () => null
  };
});

function observation(response_key: string): AddGradingEvaluationObservationRequest {
  return {
    response_key, response_fingerprint: "a".repeat(64), reference_kind: "gold",
    subject: "mathematics", archetype: "short_answer", reference_score: 1, model_score: 1, max_score: 2,
    ocr_quality: "high", answer_length: "short", rubric_complexity: "low", error_source: "none",
    needs_human_review: false, reference_reviewer_count: 1, reference_adjudicated: false
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  const run: GradingEvaluationRun = {
    id: "evaluation-a", key: "evaluation-a", display_name: "评分验证报告",
    model_reference: "model-v1", prompt_version: "prompt-v1", rubric_version: "rubric-v1",
    dataset_reference: "dataset", dataset_sha256: "b".repeat(64), status: "draft",
    observation_count: 0, created_at: ""
  };
  mocks.list.mockResolvedValue({ evaluation_runs: [run] });
});
afterEach(cleanup);

async function openImport() {
  render(<ScoringAssuranceWorkspace tenantID="school-a" canReadEligibility={false}
    canManageEligibility={false} canManageEvaluations canReadDisagreements={false} canManageDisagreements={false} />);
  const openButton = screen.getByRole("button", { name: "导入对齐评分记录" }) as HTMLButtonElement;
  await waitFor(() => expect(openButton.disabled).toBe(false));
  fireEvent.click(openButton);
  return screen.getByLabelText("JSON 观察记录");
}

function preview(input: HTMLElement, rows: unknown[]) {
  fireEvent.change(input, { target: { value: JSON.stringify(rows) } });
  fireEvent.click(screen.getByRole("button", { name: "校验并预览" }));
}

describe("评分观察导入的真实预览入口", () => {
  it("合法首行后多字段错误只报分数首错，修正同键重预览不继承中途去重记录", async () => {
    const input = await openImport();
    // 首行已进入本轮去重集合，第二行同时破坏分数和标签；失败不能发布部分预览或写入。
    preview(input, [observation("first"), { ...observation("second"), model_score: 3, ocr_quality: "invalid" }]);
    expect(screen.getByText("请核对参考分、模型分和满分")).toBeTruthy();
    expect(screen.queryByText("请核对真值来源与切片标签")).toBeNull();
    expect(screen.queryByRole("button", { name: "确认导入" })).toBeNull();
    expect(screen.queryByText(/共 2 条/)).toBeNull();
    expect(mocks.write).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();

    preview(input, [observation("first"), observation("second")]);
    expect(screen.getByText(/共 2 条，已保存 0 条。首条：first/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "确认导入" })).toBeTruthy();
    expect(screen.queryByText("请核对参考分、模型分和满分")).toBeNull();
    expect(screen.queryByText("答卷键须唯一，指纹须为真实64位SHA256")).toBeNull();
    expect(mocks.write).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("可选质量比率缺省、0和1均可预览，预览不写入观察", async () => {
    const input = await openImport();
    preview(input, [
      observation("missing"),
      { ...observation("zero"), crop_iou: 0, transcription_cer: 0, rubric_criterion_agreement: 0 },
      { ...observation("one"), crop_iou: 1, transcription_cer: 1, rubric_criterion_agreement: 1 }
    ]);
    expect(screen.getByText(/共 3 条，已保存 0 条。首条：missing/)).toBeTruthy();
    expect(screen.queryByText("可选质量比率须在0—1之间")).toBeNull();
    expect(mocks.write).not.toHaveBeenCalled();
  });

  it.each(["crop_iou", "transcription_cer", "rubric_criterion_agreement"])("%s低于0或高于1沿用原错误且无成功预览", async (field) => {
    const input = await openImport();
    for (const value of [-0.01, 1.01]) {
      preview(input, [{ ...observation("out-of-range"), [field]: value }]);
      expect(screen.getByText("可选质量比率须在0—1之间")).toBeTruthy();
      expect(screen.queryByRole("button", { name: "确认导入" })).toBeNull();
      expect(screen.queryByText(/共 1 条/)).toBeNull();
      expect(mocks.write).not.toHaveBeenCalled();
    }
  });
});
