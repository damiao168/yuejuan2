import { Alert, Button, List, Select, Spin } from "antd";
import { MousePointer2 } from "lucide-react";
import { StatusTag } from "../../../components/StatusTag";
import { calibrationSessionTitle, calibrationStatusLabel, calibrationStatusTone } from "./OMRCalibrationLabels";
import type { TemplateOMRView } from "./templateViewTypes";


export function OMRReferenceNotice({ view }: Readonly<{ view: TemplateOMRView }>) {
	const { layout } = view.editor;
	if (layout.omr_profile?.reference)
		return (
			<div
				className="template-help"
				title={`文件 ${layout.omr_profile.reference.file_asset_id} · 校验码 ${layout.omr_profile.reference.hash_sha256}`}
			>
				<MousePointer2 size={18} />
				<p>已绑定本答题卡的空白对照页</p>
			</div>
		);
	if (layout.omr_profile?.mode === "template_difference")
		return (
			<Alert
				type="info"
				showIcon
				message="保存后将绑定当前答题卡原件"
				description="锁定模板时会自动核对对照页与答题卡原件是否一致。"
			/>
		);
	return null;
}

export function OMRProfilePanel({ view }: Readonly<{ view: TemplateOMRView }>) {
	const { selectedTemplate } = view;
	const { layout, readonly, setOMRProfile } = view.editor;
	const { calibrations } = view.calibration;
	if (!selectedTemplate) return null;
	return (
		<section className="template-option-editor">
			<div className="template-pane-head">
				<strong>选择题识别方式</strong>
				{calibrations.some((item) => item.status === "approved") ? (
					<StatusTag tone="success">支持自动确认</StatusTag>
				) : (
					<StatusTag tone="neutral">结果需人工复核</StatusTag>
				)}
			</div>
			<Select
				value={layout.omr_profile?.mode ?? "manual_only"}
				disabled={readonly}
				onChange={setOMRProfile}
				options={[
					{ value: "manual_only", label: "直接识别填涂（每份需人工复核）" },
					{
						value: "template_difference",
						label: "对照空白卷识别（更准确，校准后可自动确认）"
					}
				]}
			/>
			{(layout.omr_profile?.mode ?? "manual_only") === "template_difference" ? (
				<p>
					用同版空白卡对照识别；校准通过前，结果仅供参考，不计入成绩。
				</p>
			) : null}
			<OMRReferenceNotice view={view} />
		</section>
	);
}

export function CalibrationSessions({ view }: Readonly<{ view: TemplateOMRView }>) {
	const { questions } = view;
	const {
		loading: calibrationLoading,
		calibrations,
		busy: calibrationBusy,
		open: openCalibration
	} = view.calibration;
	if (calibrationLoading) return <Spin size="small" />;
	if (!calibrations.length)
		return (
			<Alert
				type="info"
				showIcon
				message="尚无校准记录"
				description="整套模板累计足够的四类识别结果后，可开始人工校准。"
			/>
		);
	return (
		<List
			className="calibration-session-list"
			size="small"
			dataSource={calibrations.slice(0, 4)}
			renderItem={(item) => (
				<List.Item
					actions={[
						<Button
							key="open"
							type="link"
							size="small"
							loading={calibrationBusy}
							onClick={() => void openCalibration(item.id)}
						>
							查看
						</Button>
					]}
				>
					<div className="calibration-session-row">
						<div>
							<strong>{calibrationSessionTitle(item, questions)}</strong>
							<span>
								{item.summary.labeled_count}/{item.summary.total_count} 已标注 ·
								自动确认候选错误 {item.summary.eligible_mismatch_count}
							</span>
						</div>
						<StatusTag tone={calibrationStatusTone(item.status)}>
							{calibrationStatusLabel(item.status)}
						</StatusTag>
					</div>
				</List.Item>
			)}
		/>
	);
}

export function CalibrationPanel({ view }: Readonly<{ view: TemplateOMRView }>) {
	const { selectedTemplate, canCalibrate } = view;
	const {
		calibrations,
		busy: calibrationBusy,
		error: calibrationError,
		start: startCalibration,
		reload: loadOMRCalibrationList
	} = view.calibration;
	if (
		selectedTemplate?.status !== "locked" ||
		selectedTemplate.layout.omr_profile?.mode !== "template_difference"
	)
		return null;
	return (
		<section className="template-option-editor calibration-panel">
			<div className="template-pane-head">
				<strong>填涂识别校准</strong>
				<StatusTag
					tone={
						calibrations.some((item) => item.status === "approved")
							? "success"
							: "warning"
					}
				>
					{calibrations.some((item) => item.status === "approved")
						? "校准已通过"
						: "未校准，全部人工复核"}
				</StatusTag>
			</div>
			<p>
				一次校准覆盖整套锁定模板。系统按题目以及高置信、低置信、空白、模糊四类结果均衡抽样；标注前隐藏机器结果。
			</p>
			{!canCalibrate ? (
				<Alert
					type="info"
					showIcon
					message="需要“评分管理”权限才能创建、标注或审批校准"
				/>
			) : (
				<>
					<Button
						block
						type="primary"
						loading={calibrationBusy}
						onClick={() => void startCalibration()}
					>
						开始整套模板校准
					</Button>
					{calibrationError ? (
						<Alert
							type="warning"
							showIcon
							message="校准状态暂不可读取"
							description={calibrationError}
							action={
								<Button
									size="small"
									onClick={() =>
										selectedTemplate &&
										void loadOMRCalibrationList(selectedTemplate.id)
									}
								>
									重试
								</Button>
							}
						/>
					) : null}
					<CalibrationSessions view={view} />
				</>
			)}
		</section>
	);
}
