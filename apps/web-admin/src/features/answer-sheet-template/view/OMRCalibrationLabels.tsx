import type { OMRCalibrationCase, OMRCalibrationSession, OMRCalibrationStatus } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import type { TemplateOMRView } from "./templateViewTypes";



export function calibrationStatusLabel(status: OMRCalibrationStatus) {
	return {
		draft: "待标注",
		approved: "已批准",
		revoked: "已撤销",
		discarded: "已弃用"
	}[status];
}

export function calibrationStatusTone(
	status: OMRCalibrationStatus
): "warning" | "success" | "danger" | "neutral" {
	switch (status) {
		case "approved":
			return "success";
		case "revoked":
			return "danger";
		case "discarded":
			return "neutral";
		default:
			return "warning";
	}
}

export function calibrationBlockerLabel(code: string) {
	return (
		{
			sample_count_below_minimum: "合格历史样本不足 100 份",
			calibration_labels_pending: "仍有样本尚未人工标注",
			calibration_mismatch_detected: "人工标注与识别结果出现不一致",
			eligible_calibration_mismatch_detected:
				"自动确认候选存在识别错误，不能开放自动确认",
			calibration_result_gate_missing:
				"样本缺少完整的识别质量记录，请重新建立校准",
			option_coverage_incomplete: "各选项的人工样本覆盖不足 10 份",
			question_coverage_incomplete: "模板内仍有选择题未被样本覆盖",
			stratum_coverage_incomplete: "高置信、低置信、空白或模糊样本覆盖不足",
			calibration_not_draft: "该校准已不处于可审批状态"
		}[code] ?? "存在未满足的校准条件"
	);
}

export function calibrationStratumLabel(value: OMRCalibrationCase["sample_stratum"]) {
	return (
		{
			selected_high: "高置信候选",
			selected_low: "低置信候选",
			blank: "机器判空白",
			ambiguous: "模糊或多选",
			"": "待标注"
		}[value] ?? "其他"
	);
}

export function calibrationStatusNotice(status: OMRCalibrationStatus) {
	if (status === "approved")
		return {
			type: "success" as const,
			description:
				"校准已生效：识别把握≥98%且满足自动确认条件的结果可自动确认，其余转人工复核。"
		};
	if (status === "revoked")
		return {
			type: "error" as const,
			description: "撤销已生效：尚未处理完的识别任务将全部转入人工复核。"
		};
	return {
		type: "info" as const,
		description: "只按答题图片标注；提交后才显示机器结果，标注不能修改。"
	};
}

export function calibrationProgressStatus(
	session: OMRCalibrationSession,
	progress: number
) {
	if (session.summary.eligible_mismatch_count > 0) return "exception";
	if (progress === 100) return "success";
	return "active";
}

export function calibrationSessionTitle(
	session: OMRCalibrationSession,
	questions: Question[]
) {
	if (session.scope_type !== "template")
		return (
			questions.find((question) => question.id === session.question_id)
				?.question_no ?? "历史题目级校准"
		);
	const inheritance = session.inherited_from_session_id ? " · 继承校准" : "";
	return `整套模板 · ${session.question_ids.length} 道题${inheritance}`;
}

export function calibrationMismatchDescription(sample: OMRCalibrationCase) {
	if (sample.matches) return "一致";
	// 抽样分层不等于自动确认资格；空白和清晰多选也可能进入自动分支，是否阻断由服务端冻结证据判断。
	return "识别不一致；自动确认候选的错例将阻断审批";
}

export function calibrationActionNotice(
	action: TemplateOMRView["calibration"]["action"]
) {
	switch (action) {
		case "approve":
			return {
				title: "批准自动确认校准",
				okText: "确认批准",
				message: "审批人不能是参与标注的人，系统会自动核验。"
			};
		case "revoke":
			return {
				title: "撤销自动确认校准",
				okText: "立即撤销",
				message:
					"撤销后，识别结果不再自动写入成绩，未完成的任务将转入人工复核。"
			};
		default:
			return {
				title: "弃用校准草稿",
				okText: "弃用草稿",
				message: "弃用不会删除已写入的标注记录，只会终止这份草稿。"
			};
	}
}
