import { useEffect, useState } from "react";
import { Button } from "antd";
import {
	getModelPolicy,
	listModelApprovals,
	listModelEvaluationRuns,
	type EvaluationRun,
	type ModelApproval,
	type TenantModelPolicy,
} from "../../api/modelGovernance";

export function ScoringSetupChecklist({
	tenantID,
	modelIDs,
	capabilityReady,
	bindingReady,
	subject,
	onOpen,
}: Readonly<{
	tenantID: string;
	modelIDs: string[];
	capabilityReady: boolean;
	bindingReady: boolean;
	subject: string;
	onOpen: (view: string) => void;
}>) {
	const [evidence, setEvidence] = useState<{
		runs: EvaluationRun[];
		approvals: ModelApproval[];
		policy: TenantModelPolicy;
	}>();
	const [failed, setFailed] = useState(false);
	useEffect(() => {
		// 切换学校或卸载后只阻止旧请求回填，不会取消已发出的请求。
		let active = true;
		setEvidence(undefined);
		setFailed(false);
		void (async () => {
			try {
				const [runs, approvals, policy] = await Promise.all([
					listModelEvaluationRuns(tenantID),
					listModelApprovals(tenantID),
					getModelPolicy(tenantID),
				]);
				if (active)
					setEvidence({
						runs: runs.evaluation_runs,
						approvals: approvals.model_approvals,
						policy: policy.policy,
					});
			} catch {
				if (active) setFailed(true);
			}
		})();
		return () => {
			active = false;
		};
	}, [tenantID]);
	const frozen =
		evidence?.runs.filter(
			(run) =>
				run.status === "completed" &&
				run.evidence_class === "authorized_frozen_set" &&
				run.candidates.some((candidate) =>
					modelIDs.includes(candidate.model_config_id ?? ""),
				),
		) ?? [];
	const approved =
		evidence?.approvals.filter(
			(approval) =>
				!approval.revoked_at &&
				new Date(approval.expires_at).getTime() > Date.now() &&
				modelIDs.includes(approval.model_config_id ?? "") &&
				frozen.some((run) => run.id === approval.evaluation_run_id),
		) ?? [];
	const policy = evidence?.policy;
	// 已登记的证据和批准只用于核对范围，不能据此宣称实际答卷已获准入。
	let evaluationLabel = "正在核对证据";
	if (evidence) {
		evaluationLabel = `相关模型已有 ${frozen.length} 组授权冻结证据，须核对${subject}的年级、题型、提示词和细则版本`;
	} else if (failed) {
		evaluationLabel = "读取失败，需重新核对";
	}
	let policyLabel = "尚未核实";
	if (policy) {
		let externalLabel = "外部模型关闭";
		if (policy.external_enabled) externalLabel = "允许外部模型";
		let budgetLabel = "预算尚未配置";
		if (policy.max_cost_micros_per_question > 0 && policy.max_cost_micros_per_exam > 0) {
			budgetLabel = "已配置题目与考试预算";
		}
		policyLabel = `${externalLabel}；${budgetLabel}；请核对当前模型白名单及文本/图片授权`;
	}
	return (
		<section aria-label="正式评分准备检查">
			<h3>正式评分准备</h3>
			<ol>
				<li>
					回复与结构化能力：
					{capabilityReady ? "当前绑定模型已验证" : "先在学校模型库检测"}
				</li>
				<li>
					三角色绑定：
					{bindingReady
						? "已保存并满足配置门槛"
						: "需完成三个角色及不同复核模型约束"}
				</li>
				<li>
					真实评测：
					{evaluationLabel}{" "}
					<Button onClick={() => onOpen("evaluations")}>对比候选模型</Button>
				</li>
				<li>
					人工批准：
					{evidence
						? `相关模型有 ${approved.length} 条有效批准，须核对本范围及有效期`
						: "尚未核实"}{" "}
					<Button onClick={() => onOpen("approvals")}>核对批准</Button>
				</li>
				<li>
					数据与预算策略：
					{policyLabel}{" "}
					<Button onClick={() => onOpen("policy")}>查看学校使用策略</Button>
				</li>
			</ol>
			<p>
				此清单展示已登记事实，不代替实际答卷的评分准入决策；路由强度不是质量分数。
			</p>
			<Button onClick={() => onOpen("assurance")}>验证评分准入</Button>
		</section>
	);
}
