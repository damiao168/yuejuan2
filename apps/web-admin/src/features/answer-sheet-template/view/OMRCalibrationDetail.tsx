import { Alert, Button, Collapse, Drawer, Input, Modal, Progress, Spin } from "antd";
import type { OMRCalibrationCase } from "../../../api/configuration";
import { CalibrationCaseView } from "./OMRCalibrationCase";
import { calibrationActionNotice, calibrationBlockerLabel, calibrationProgressStatus, calibrationStatusLabel, calibrationStatusNotice, calibrationStratumLabel } from "./OMRCalibrationLabels";
import type { TemplateOMRView } from "./templateViewTypes";


export function CalibrationSummary({ view }: Readonly<{ view: TemplateOMRView }>) {
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
					<span>自动确认候选正确</span>
					<strong>
						{calibrationDetail.session.summary.eligible_match_count}/
						{calibrationDetail.session.summary.eligible_count}
					</strong>
				</div>
				<div>
					<span>自动确认候选错误</span>
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

export function CalibrationSessionActions({ view }: Readonly<{ view: TemplateOMRView }>) {
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

export function CalibrationEvidence({ view }: Readonly<{ view: TemplateOMRView }>) {
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

export function CalibrationDetailView({ view }: Readonly<{ view: TemplateOMRView }>) {
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

export function CalibrationDrawer({ view }: Readonly<{ view: TemplateOMRView }>) {
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

export function CalibrationActionModal({ view }: Readonly<{ view: TemplateOMRView }>) {
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
