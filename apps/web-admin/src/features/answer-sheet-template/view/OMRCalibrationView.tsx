import {
	Alert,
	Button,
	Checkbox,
	Collapse,
	Drawer,
	Input,
	List,
	Modal,
	Progress,
	Select,
	Space,
	Spin
} from "antd";
import { MousePointer2 } from "lucide-react";
import { EmptyState } from "../../../components/PageState";
import { StatusTag } from "../../../components/StatusTag";
import type {
	OMRCalibrationCase,
	OMRCalibrationSession,
	OMRCalibrationStatus
} from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import type { TemplatePageViewProps } from "./templateViewTypes";

function calibrationStatusLabel(status: OMRCalibrationStatus) {
	return {
		draft: "待标注",
		approved: "已批准",
		revoked: "已撤销",
		discarded: "已弃用"
	}[status];
}

function calibrationStatusTone(
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

function calibrationBlockerLabel(code: string) {
	return (
		{
			sample_count_below_minimum: "合格历史样本不足 100 份",
			calibration_labels_pending: "仍有样本尚未人工标注",
			calibration_mismatch_detected: "人工标注与识别结果出现不一致",
			eligible_calibration_mismatch_detected:
				"高置信候选存在识别错误，不能开放自动确认",
			option_coverage_incomplete: "各选项的人工样本覆盖不足 10 份",
			question_coverage_incomplete: "模板内仍有选择题未被样本覆盖",
			stratum_coverage_incomplete: "高置信、低置信、空白或模糊样本覆盖不足",
			calibration_not_draft: "该校准已不处于可审批状态"
		}[code] ?? "存在未满足的校准条件"
	);
}

function calibrationStratumLabel(value: OMRCalibrationCase["sample_stratum"]) {
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

function calibrationStatusNotice(status: OMRCalibrationStatus) {
	if (status === "approved")
		return {
			type: "success" as const,
			description:
				"校准已生效：整套模板中识别把握不低于 98% 且符合自动确认条件的结果可自动确认，其余仍转人工复核。"
		};
	if (status === "revoked")
		return {
			type: "error" as const,
			description: "撤销已生效：尚未处理完的识别任务将全部转入人工复核。"
		};
	return {
		type: "info" as const,
		description: "请只依据答题图片盲标；提交后才显示机器结果，且标注不可修改。"
	};
}

function calibrationProgressStatus(
	session: OMRCalibrationSession,
	progress: number
) {
	if (session.summary.eligible_mismatch_count > 0) return "exception";
	if (progress === 100) return "success";
	return "active";
}

function calibrationSessionTitle(
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

function OMRReferenceNotice({ view }: TemplatePageViewProps) {
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

export function OMRProfilePanel({ view }: TemplatePageViewProps) {
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
					该方式用同一版空白答题卡作对照，排除印刷文字干扰。校准通过前，识别结果仅供参考，不会计入成绩。
				</p>
			) : null}
			<OMRReferenceNotice view={view} />
		</section>
	);
}

function CalibrationSessions({ view }: TemplatePageViewProps) {
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
								高置信错误 {item.summary.eligible_mismatch_count}
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

export function CalibrationPanel({ view }: TemplatePageViewProps) {
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

function CalibrationSummary({ view }: TemplatePageViewProps) {
	const { detail: calibrationDetail } = view.calibration;
	if (!calibrationDetail) return null;
	const calibrationProgress = Math.round(
		(calibrationDetail.session.summary.labeled_count /
			Math.max(calibrationDetail.session.summary.total_count, 1)) *
			100
	);
	const notice = calibrationStatusNotice(calibrationDetail.session.status);
	return (
		<>
			<Alert
				type={notice.type}
				showIcon
				message={`状态：${calibrationStatusLabel(calibrationDetail.session.status)}`}
				description={notice.description}
			/>
			<div className="calibration-metrics">
				<div>
					<span>已标注</span>
					<strong>
						{calibrationDetail.session.summary.labeled_count}/
						{calibrationDetail.session.summary.total_count}
					</strong>
				</div>
				<div>
					<span>高置信正确</span>
					<strong>
						{calibrationDetail.session.summary.eligible_match_count}/
						{calibrationDetail.session.summary.eligible_count}
					</strong>
				</div>
				<div>
					<span>高置信错误</span>
					<strong>
						{calibrationDetail.session.summary.eligible_mismatch_count}
					</strong>
				</div>
				<div>
					<span>识别把握下限</span>
					<strong>
						{Math.round(calibrationDetail.session.minimum_confidence * 100)}%
					</strong>
				</div>
			</div>
			<Progress
				percent={calibrationProgress}
				status={calibrationProgressStatus(
					calibrationDetail.session,
					calibrationProgress
				)}
			/>
			{calibrationDetail.session.status === "draft" &&
			calibrationDetail.session.summary.blockers.length ? (
				<Alert
					type="warning"
					showIcon
					message="尚不能批准自动确认"
					description={
						<ul className="calibration-blockers">
							{calibrationDetail.session.summary.blockers.map((item) => (
								<li key={item} title={item}>
									{calibrationBlockerLabel(item)}
								</li>
							))}
						</ul>
					}
				/>
			) : null}
			<p className="muted">
				分层样本覆盖（每类至少{" "}
				{calibrationDetail.session.minimum_samples_per_stratum} 份）
			</p>
			<div className="calibration-coverage">
				{Object.entries(calibrationDetail.session.summary.stratum_coverage).map(
					([stratum, count]) => (
						<span key={stratum}>
							{calibrationStratumLabel(
								stratum as OMRCalibrationCase["sample_stratum"]
							)}
							: {count}/{calibrationDetail.session.minimum_samples_per_stratum}
						</span>
					)
				)}
			</div>
			<p className="muted">
				各选项已标注样本数（每个选项至少需{" "}
				{calibrationDetail.session.minimum_samples_per_option} 份）
			</p>
			<div className="calibration-coverage">
				{Object.entries(calibrationDetail.session.summary.option_coverage).map(
					([option, count]) => (
						<span key={option}>
							{option}: {count}/
							{calibrationDetail.session.minimum_samples_per_option}
						</span>
					)
				)}
			</div>
		</>
	);
}

function CalibrationImage({ view }: TemplatePageViewProps) {
	const {
		imageLoading: calibrationImageLoading,
		imageUrl: calibrationImageUrl,
		detail,
		selectedCase
	} = view.calibration;
	const index =
		detail && selectedCase
			? detail.cases.findIndex((item) => item.id === selectedCase.id)
			: -1;
	if (calibrationImageLoading)
		return (
			<div className="calibration-crop">
				<Spin tip="正在加载答题图片" />
			</div>
		);
	if (calibrationImageUrl)
		return (
			<div className="calibration-crop">
				<img src={calibrationImageUrl} alt={`校准样本 ${index + 1}`} />
			</div>
		);
	return (
		<div className="calibration-crop">
			<Alert type="error" showIcon message="图片加载失败" />
		</div>
	);
}

function calibrationMismatchDescription(sample: OMRCalibrationCase) {
	if (sample.matches) return "一致";
	if (sample.sample_stratum === "selected_high") return "高置信错误，阻断审批";
	return "不一致，但该分支仍保持人工复核";
}

function CalibrationLabelActions({ view }: TemplatePageViewProps) {
	const {
		detail: calibrationDetail,
		selectedCase: selectedCalibrationCase,
		busy: calibrationBusy,
		expectedOptions: calibrationExpectedOptions,
		setExpectedOptions: setCalibrationExpectedOptions,
		labelCase: labelCalibrationCase
	} = view.calibration;
	if (!calibrationDetail || !selectedCalibrationCase) return null;
	if (selectedCalibrationCase.question_type === "multiple_choice")
		return (
			<>
				<Checkbox.Group
					options={selectedCalibrationCase.option_labels}
					value={calibrationExpectedOptions}
					onChange={(values) =>
						setCalibrationExpectedOptions(values as string[])
					}
				/>
				<Space wrap className="calibration-label-actions">
					<Button
						type="primary"
						disabled={!calibrationExpectedOptions.length || calibrationBusy}
						loading={calibrationBusy}
						onClick={() =>
							void labelCalibrationCase(calibrationExpectedOptions)
						}
					>
						提交所选项
					</Button>
					<Button
						disabled={calibrationBusy}
						onClick={() => void labelCalibrationCase([])}
					>
						标注为空白
					</Button>
				</Space>
			</>
		);
	return (
		<Space wrap className="calibration-label-actions">
			{selectedCalibrationCase.option_labels.map((option) => (
				<Button
					key={option}
					type="primary"
					disabled={
						calibrationDetail.session.status !== "draft" || calibrationBusy
					}
					loading={calibrationBusy}
					onClick={() => void labelCalibrationCase([option])}
				>
					标注为 {option}
				</Button>
			))}
			<Button
				disabled={
					calibrationDetail.session.status !== "draft" || calibrationBusy
				}
				onClick={() => void labelCalibrationCase([])}
			>
				标注为空白
			</Button>
		</Space>
	);
}

function CalibrationLabelResult({ view }: TemplatePageViewProps) {
	const { selectedCase: sample } = view.calibration;
	if (!sample) return null;
	if (sample.matches !== undefined) {
		const expected = sample.expected_options?.length
			? sample.expected_options.join(", ")
			: "空白";
		const observed = sample.observed_options?.length
			? sample.observed_options.join(", ")
			: sample.observed_decision;
		return (
			<Alert
				type={sample.matches ? "success" : "warning"}
				showIcon
				message={`人工标注：${expected}`}
				description={`机器识别：${observed} · 识别把握 ${Math.round(sample.observed_confidence * 100)}% · ${calibrationMismatchDescription(sample)}`}
			/>
		);
	}
	return (
		<>
			<Alert
				type="info"
				showIcon
				message={
					sample.question_type === "multiple_choice"
						? "请只根据图片选择所有实际填涂项"
						: "请只根据图片判断实际填涂项"
				}
				description="提交前不显示机器识别选项与置信度，避免影响人工判断。"
			/>
			<CalibrationLabelActions view={view} />
		</>
	);
}

function CalibrationCaseView({ view }: TemplatePageViewProps) {
	const {
		detail: calibrationDetail,
		selectedCase: selectedCalibrationCase,
		busy: calibrationBusy,
		setCaseId: setCalibrationCaseId
	} = view.calibration;
	if (!calibrationDetail) return null;
	if (!selectedCalibrationCase)
		return (
			<EmptyState
				title="没有可用样本"
				description="校准样本尚未生成，请先开始校准。"
			/>
		);
	const selectedCalibrationIndex = calibrationDetail.cases.findIndex(
		(item) => item.id === selectedCalibrationCase.id
	);
	return (
		<section className="calibration-case">
			<div className="template-pane-head">
				<strong>
					{selectedCalibrationCase.question_no} · 样本{" "}
					{selectedCalibrationIndex + 1}/{calibrationDetail.cases.length}
				</strong>
				<Space>
					<StatusTag tone="neutral">
						{selectedCalibrationCase.matches === undefined
							? "盲标样本"
							: calibrationStratumLabel(selectedCalibrationCase.sample_stratum)}
					</StatusTag>
					<Button
						size="small"
						disabled={selectedCalibrationIndex <= 0 || calibrationBusy}
						onClick={() =>
							setCalibrationCaseId(
								calibrationDetail.cases[selectedCalibrationIndex - 1].id
							)
						}
					>
						上一份
					</Button>
					<Button
						size="small"
						disabled={
							selectedCalibrationIndex < 0 ||
							selectedCalibrationIndex >= calibrationDetail.cases.length - 1 ||
							calibrationBusy
						}
						onClick={() =>
							setCalibrationCaseId(
								calibrationDetail.cases[selectedCalibrationIndex + 1].id
							)
						}
					>
						下一份
					</Button>
				</Space>
			</div>
			<CalibrationImage view={view} />
			<CalibrationLabelResult view={view} />
		</section>
	);
}

function CalibrationSessionActions({ view }: TemplatePageViewProps) {
	const {
		detail: calibrationDetail,
		busy: calibrationBusy,
		setActionReason: setCalibrationActionReason,
		setAction: setCalibrationAction
	} = view.calibration;
	if (!calibrationDetail) return null;
	return (
		<div className="calibration-actions">
			{calibrationDetail.session.status === "draft" ? (
				<>
					<Button
						type="primary"
						disabled={
							!calibrationDetail.session.summary.ready_to_approve ||
							calibrationBusy
						}
						onClick={() => {
							setCalibrationActionReason("");
							setCalibrationAction("approve");
						}}
					>
						由独立审批人批准
					</Button>
					<Button
						danger
						disabled={calibrationBusy}
						onClick={() => {
							setCalibrationActionReason("");
							setCalibrationAction("discard");
						}}
					>
						弃用草稿
					</Button>
				</>
			) : null}
			{calibrationDetail.session.status === "approved" ? (
				<Button
					danger
					disabled={calibrationBusy}
					onClick={() => {
						setCalibrationActionReason("");
						setCalibrationAction("revoke");
					}}
				>
					立即撤销自动确认
				</Button>
			) : null}
		</div>
	);
}

function CalibrationEvidence({ view }: TemplatePageViewProps) {
	const { detail: calibrationDetail } = view.calibration;
	if (!calibrationDetail) return null;
	return (
		<Collapse
			items={[
				{
					key: "evidence",
					label: "审计校验信息",
					children: (
						<div className="calibration-evidence">
							<span title={calibrationDetail.session.template_content_hash}>
								模板校验码：
								{calibrationDetail.session.template_content_hash.slice(0, 16)}
							</span>
							<span title={calibrationDetail.session.profile_hash}>
								配置校验码：
								{calibrationDetail.session.profile_hash.slice(0, 16)}
							</span>
							<span title={calibrationDetail.session.reference_sha256}>
								对照页校验码：
								{calibrationDetail.session.reference_sha256.slice(0, 16)}
							</span>
							{calibrationDetail.session.evidence_hash ? (
								<span title={calibrationDetail.session.evidence_hash}>
									证据校验码：
									{calibrationDetail.session.evidence_hash.slice(0, 24)}
								</span>
							) : null}
						</div>
					)
				}
			]}
		/>
	);
}

function CalibrationDetailView({ view }: TemplatePageViewProps) {
	if (!view.calibration.detail) return <Spin />;
	return (
		<div className="calibration-drawer">
			<CalibrationSummary view={view} />
			<CalibrationCaseView view={view} />
			<CalibrationSessionActions view={view} />
			<CalibrationEvidence view={view} />
		</div>
	);
}

export function CalibrationDrawer({ view }: TemplatePageViewProps) {
	const {
		drawerOpen: calibrationDrawerOpen,
		setDrawerOpen: setCalibrationDrawerOpen
	} = view.calibration;
	return (
		<Drawer
			title="填涂识别校准记录"
			width={760}
			open={calibrationDrawerOpen}
			onClose={() => setCalibrationDrawerOpen(false)}
			destroyOnHidden={false}
		>
			<CalibrationDetailView view={view} />
		</Drawer>
	);
}

function calibrationActionNotice(
	action: TemplatePageViewProps["view"]["calibration"]["action"]
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

export function CalibrationActionModal({ view }: TemplatePageViewProps) {
	const {
		action: calibrationAction,
		busy: calibrationBusy,
		actionReason: calibrationActionReason,
		setActionReason: setCalibrationActionReason,
		submitAction: submitCalibrationAction,
		setAction: setCalibrationAction
	} = view.calibration;
	const notice = calibrationActionNotice(calibrationAction);
	return (
		<Modal
			open={Boolean(calibrationAction)}
			title={notice.title}
			okText={notice.okText}
			okButtonProps={{ danger: calibrationAction !== "approve" }}
			confirmLoading={calibrationBusy}
			onOk={() => void submitCalibrationAction()}
			onCancel={() => !calibrationBusy && setCalibrationAction(undefined)}
		>
			<Alert
				type={calibrationAction === "approve" ? "warning" : "info"}
				showIcon
				message={notice.message}
			/>
			<Input.TextArea
				autoFocus
				rows={4}
				value={calibrationActionReason}
				onChange={(event) => setCalibrationActionReason(event.target.value)}
				placeholder="请填写操作原因（至少 10 个字），将记入操作记录"
			/>
		</Modal>
	);
}
