// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { ScoringSetupChecklist } from "./ScoringSetupChecklist";
import { DataPolicyCard } from "../../features/onboarding/DataPolicyCard";
const api = vi.hoisted(() => ({
	runs: vi.fn(),
	approvals: vi.fn(),
	policy: vi.fn(),
}));
vi.mock("../../api/modelGovernance", () => ({
	listModelEvaluationRuns: api.runs,
	listModelApprovals: api.approvals,
	getModelPolicy: api.policy,
}));
vi.mock("antd", () => ({
	Button: ({
		children,
		onClick,
	}: {
		children: ReactNode;
		onClick: () => void;
	}) => (
		<button type="button" onClick={onClick}>
			{children}
		</button>
	),
}));
describe("评分准备事实与学校策略入口", () => {
	it("仅同模型授权冻结报告和未过期未撤销批准计数，不声称已通过当前题型准入", async () => {
		api.runs.mockResolvedValue({
			evaluation_runs: [
				{
					id: "real",
					status: "completed",
					evidence_class: "authorized_frozen_set",
					candidates: [{ model_config_id: "model" }],
				},
				{
					id: "sample",
					status: "completed",
					evidence_class: "protocol_fixture",
					candidates: [{ model_config_id: "model" }],
				},
				{
					id: "stale",
					status: "invalidated",
					evidence_class: "authorized_frozen_set",
					candidates: [{ model_config_id: "model" }],
				},
				{
					id: "other",
					status: "completed",
					evidence_class: "authorized_frozen_set",
					candidates: [{ model_config_id: "other" }],
				},
			],
		});
		api.approvals.mockResolvedValue({
			model_approvals: [
				{
					model_config_id: "model",
					evaluation_run_id: "real",
					expires_at: "2030-01-01T00:00:00Z",
				},
				{
					model_config_id: "model",
					evaluation_run_id: "sample",
					expires_at: "2030-01-01T00:00:00Z",
				},
				{
					model_config_id: "model",
					evaluation_run_id: "real",
					expires_at: "2020-01-01T00:00:00Z",
				},
				{
					model_config_id: "model",
					evaluation_run_id: "real",
					expires_at: "2030-01-01T00:00:00Z",
					revoked_at: "2026-01-01T00:00:00Z",
				},
			],
		});
		api.policy.mockResolvedValue({
			policy: {
				external_enabled: false,
				max_cost_micros_per_question: 0,
				max_cost_micros_per_exam: 0,
			},
		});
		const onOpen = vi.fn();
		render(
			<ScoringSetupChecklist
				tenantID="school"
				modelIDs={["model"]}
				capabilityReady
				bindingReady
				subject="数学"
				onOpen={onOpen}
			/>,
		);
		await screen.findByText(/相关模型已有 1 组授权冻结证据/);
		expect(screen.getByText(/相关模型有 1 条有效批准/)).toBeTruthy();
		expect(screen.getByText(/预算尚未配置/)).toBeTruthy();
		expect(screen.getByText(/不代替实际答卷的评分准入决策/)).toBeTruthy();
		fireEvent.click(screen.getByRole("button", { name: "核对批准" }));
		expect(onOpen).toHaveBeenCalledWith("approvals");
		expect(api.runs).toHaveBeenCalledWith("school");
		expect(api.policy).toHaveBeenCalledWith("school");
	});
	it("默认政策不能冒充学校授权，未核实字段不标允许且入口要求选校", () => {
		const navigate = vi.fn();
		render(
			<DataPolicyCard
				check={{
					key: "data_policy",
					title: "平台默认数据政策",
					state: "warning",
					severity: "recommended",
					metadata: { external_enabled: true },
				}}
				onNavigate={navigate}
			/>,
		);
		expect(screen.getByText(/不代表任何学校已完成数据外发授权/)).toBeTruthy();
		expect(screen.getAllByText("尚未核实")).toHaveLength(3);
		fireEvent.click(
			screen.getByRole("button", { name: /选择学校检查使用策略/ }),
		);
		expect(navigate).toHaveBeenCalledWith(
			"/platform/model-config?tab=governance&view=policy&choose_school=1",
		);
	});
});
