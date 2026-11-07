import { Alert, Button, Checkbox, Space, Spin } from "antd";
import { EmptyState } from "../../../components/PageState";
import { StatusTag } from "../../../components/StatusTag";
import { calibrationMismatchDescription, calibrationStratumLabel } from "./OMRCalibrationLabels";
import type { TemplateOMRView } from "./templateViewTypes";


export function CalibrationImage({ view }: Readonly<{ view: TemplateOMRView }>) {
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

export function CalibrationLabelActions({ view }: Readonly<{ view: TemplateOMRView }>) {
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

export function CalibrationLabelResult({ view }: Readonly<{ view: TemplateOMRView }>) {
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

export function CalibrationCaseView({ view }: Readonly<{ view: TemplateOMRView }>) {
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
