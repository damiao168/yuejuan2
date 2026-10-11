import { Alert, Button, Checkbox, Input, List, Modal, Spin, Table } from "antd";
import { Download, Printer } from "lucide-react";
import { StatusTag } from "../../../components/StatusTag";
import type { StudentPrintBatchSummary, StudentPrintCandidate } from "../../../api/printing";
import type { TemplatePageViewProps } from "./templateViewTypes";

function printBatchTime(value: string) {
	return new Intl.DateTimeFormat("zh-CN", {
		month: "2-digit",
		day: "2-digit",
		hour: "2-digit",
		minute: "2-digit",
		hour12: false
	}).format(new Date(value));
}

function printBatchStatus(batch: StudentPrintBatchSummary) {
	if (batch.downloadable) return "可下载";
	if (batch.conflict_count) return "存在冲突";
	if (batch.revoked_count) return "已作废";
	return "已开始扫描";
}

function studentSheetStatus(candidate: StudentPrintCandidate) {
	// 缺考和无有效卡事实优先于卡的扫描状态，历史卡记录不能覆盖当前展示。
	if (candidate.attendance_status === "absent") return "缺考，不签发";
	if (!candidate.has_active_sheet) return "尚未生成";
	if (candidate.active_sheet_status === "observed") return "已扫描";
	if (candidate.active_sheet_status === "conflict") return "需核验";
	return "已生成，待扫描";
}

function PrintBatchHistory({ view }: TemplatePageViewProps) {
	const { printContext, printBusy, downloadPrintBatch } = view.printing;
	if (!printContext) return null;
	if (!printContext.batches.length)
		return <Alert type="info" showIcon message="尚未签发打印包" />;
	return (
		<div className="print-batch-history">
			<span>最近签发</span>
			<List
				size="small"
				dataSource={printContext.batches.slice(0, 4)}
				renderItem={(batch) => (
					<List.Item
						actions={
							batch.downloadable
								? [
										<Button
											key="download"
											type="link"
											size="small"
											icon={<Download size={14} />}
											loading={printBusy}
											onClick={() =>
												void downloadPrintBatch(batch.print_batch_id)
											}
										>
											下载
										</Button>
									]
								: []
						}
					>
						<div className="print-batch-row">
							<strong>
								{batch.sheet_count} 份 · {batch.sheet_count * batch.page_count}{" "}
								页
							</strong>
							<span>
								{printBatchTime(batch.issued_at)} · {printBatchStatus(batch)}
							</span>
						</div>
					</List.Item>
				)}
			/>
		</div>
	);
}

export function StudentPrintPanel({ view }: TemplatePageViewProps) {
	const { selectedTemplate, canManage } = view;
	const {
		printContext,
		printContextLoading,
		printContextError,
		printableCandidates,
		loadPrintContext,
		openPrintModal
	} = view.printing;
	if (selectedTemplate?.status !== "locked" || !canManage) return null;
	return (
		<section className="template-option-editor print-package-panel">
			<div className="template-pane-head">
				<strong>答题卡打印包</strong>
				{printContext ? (
					<StatusTag
						tone={printableCandidates.length ? "processing" : "success"}
					>
						{printableCandidates.length
							? `${printableCandidates.length} 人待签发`
							: "签发完成"}
					</StatusTag>
				) : null}
			</div>
			<p>
				按班级生成带学生身份条码的受控 PDF。系统不会把姓名或学号印在答题卡上。
			</p>
			{printContextLoading ? <Spin size="small" /> : null}
			{printContextError ? (
				<Alert
					type="warning"
					showIcon
					message="打印状态暂不可读取"
					description={printContextError}
					action={
						<Button
							size="small"
							onClick={() => void loadPrintContext(selectedTemplate.id)}
						>
							重试
						</Button>
					}
				/>
			) : null}
			{printContext ? (
				<>
					{view.printing.downloadPendingBatch ? <Alert type="warning" showIcon message="答题卡已生成，打印包尚未下载" description="旧卡作废与新卡生成的结果已保存；请继续下载这份文件。" action={<Button loading={view.printing.printBusy} onClick={() => void view.printing.downloadPrintBatch(view.printing.downloadPendingBatch!)}>下载已生成的打印包</Button>} /> : null}
					<div className="print-package-metrics">
						<div>
							<span>应考</span>
							<strong>
								{
									printContext.candidates.filter(
										(item) => item.attendance_status === "expected"
									).length
								}
							</strong>
						</div>
						<div>
							<span>已有有效答题卡</span>
							<strong>
								{
									printContext.candidates.filter(
										(item) => item.has_active_sheet
									).length
								}
							</strong>
						</div>
						<div>
							<span>缺考</span>
							<strong>
								{
									printContext.candidates.filter(
										(item) => item.attendance_status === "absent"
									).length
								}
							</strong>
						</div>
					</div>
					<Button
						block
						type="primary"
						icon={<Printer size={16} />}
						disabled={!printableCandidates.length}
						onClick={openPrintModal}
					>
						生成打印包
					</Button>
					<PrintBatchHistory view={view} />
					<details>
						<summary>学生打印记录与补打</summary>
						<p>原卡遗失或损坏时，选择学生补打。系统将同时作废旧条码并生成新卡；旧卡不可再扫描。</p>
						<Table rowKey="student_id" size="small" dataSource={printContext.candidates} pagination={{ pageSize: 10 }} columns={[
							{ title: "学生", render: (_, item) => `${item.student_name}（${item.student_no}）` },
							{ title: "班级", dataIndex: "class_name" },
							{ title: "答题卡状态", render: (_, item) => studentSheetStatus(item) },
							{ title: "操作", render: (_, item) => item.has_active_sheet && item.active_sheet_serial && item.attendance_status === "expected" ? <Button type="link" disabled={view.printing.printBusy} onClick={() => view.printing.openReprint(item)}>作废旧卡并补打</Button> : "—" }
						]} />
					</details>
				</>
			) : null}
		</section>
	);
}

export function StudentPrintModal({ view }: TemplatePageViewProps) {
	const {
		printModalOpen,
		printBusy,
		selectedPrintClassIds,
		setSelectedPrintClassIds,
		printClasses,
		printableCandidates,
		issuePrintPackage,
		setPrintModalOpen
	} = view.printing;
	const selectedPrintCount = printableCandidates.filter((item) =>
		selectedPrintClassIds.includes(item.class_id)
	).length;
	const printCountNotice = selectedPrintClassIds.length
		? `（${selectedPrintCount} 人）`
		: "";
	return (
		<>
		<Modal
			open={printModalOpen}
			title="生成受控答题卡打印包"
			okText={`签发并下载${printCountNotice}`}
			cancelText="取消"
			confirmLoading={printBusy}
			okButtonProps={{ disabled: !selectedPrintClassIds.length }}
			onOk={() => void issuePrintPackage()}
			onCancel={() => !printBusy && setPrintModalOpen(false)}
		>
			<div className="print-package-modal">
				<Alert
					type="warning"
					showIcon
					message="签发后请只打印所下载的这一份文件"
					description="每名学生、每一页都有唯一条码。重复复印或打乱页面会进入人工核验；补打必须使用作废与重印流程。"
				/>
				<div className="print-class-heading">
					<strong>选择班级</strong>
					<Button
						type="link"
						size="small"
						onClick={() =>
							setSelectedPrintClassIds(
								selectedPrintClassIds.length === printClasses.length
									? []
									: printClasses.map((item) => item.id)
							)
						}
					>
						{selectedPrintClassIds.length === printClasses.length
							? "取消全选"
							: "全选"}
					</Button>
				</div>
				<div className="print-class-list">
					{printClasses.map((item) => (
						<Checkbox
							key={item.id}
							checked={selectedPrintClassIds.includes(item.id)}
							onChange={(event) =>
								setSelectedPrintClassIds((current) =>
									event.target.checked
										? [...current, item.id]
										: current.filter((id) => id !== item.id)
								)
							}
						>
							<span>{item.name}</span>
							<small>{item.count} 名待签发</small>
						</Checkbox>
					))}
				</div>
				<p className="muted">
					已排除缺考学生和已有有效答题卡的学生，避免重复签发。
				</p>
			</div>
		</Modal>
		<Modal open={Boolean(view.printing.reprintCandidate)} title="作废旧卡并补打" okText={view.printing.reprintReasonFrozen ? "重试本次补打" : "作废旧卡并生成新卡"} cancelText="返回打印记录" confirmLoading={printBusy} cancelButtonProps={{ disabled: printBusy }} closable={!printBusy} maskClosable={false} onCancel={view.printing.closeReprint} onOk={() => void view.printing.reprint()}>
			<Alert type="warning" showIcon message="确认后旧卡不可再使用" description="系统会同时作废旧条码并生成该学生的新卡。已扫描的旧卡会进入核验，请收回纸质旧卡。" />
			<p>{view.printing.reprintCandidate?.class_name} · {view.printing.reprintCandidate?.student_name} · {view.printing.reprintCandidate?.student_no}</p>
			<label htmlFor="student-reprint-reason">补打原因</label>
			<Input.TextArea id="student-reprint-reason" value={view.printing.reprintReason} disabled={printBusy || view.printing.reprintReasonFrozen} maxLength={500} showCount onChange={event => view.printing.setReprintReason(event.target.value)} placeholder="例如：原卡遗失、打印破损" />
			{view.printing.reprintError ? <Alert type="error" showIcon message={view.printing.reprintError} /> : null}
		</Modal>
		</>
	);
}
