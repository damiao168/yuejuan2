// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AnswerSheetTemplatePage } from "../../pages/AnswerSheetTemplatePage";
import type {
	OMRCalibrationCase,
	OMRCalibrationSession
} from "../../api/configuration";
import type { useTemplateEditor } from "./editor/useTemplateEditor";
import type { useTemplateBinding } from "./binding/useTemplateBinding";
import type { useTemplatePreview } from "./editor/useTemplatePreview";
import type { useStudentPrinting } from "./printing/useStudentPrinting";
import type { useOMRCalibration } from "./calibration/useOMRCalibration";
import type { useAnswerSheetSourceUpload } from "./editor/useAnswerSheetSourceUpload";
import {
	answerQuestion as question,
	answerSource as source,
	answerRegion as region,
	answerTemplate as template,
	calibrationCase as sample,
	calibrationDetail as detail,
	templateBinding
} from "./testing/answerSheetFixtures";

const mocks = vi.hoisted(() => ({
	papers: vi.fn(),
	questions: vi.fn(),
	templates: vi.fn(),
	exam: vi.fn(),
	getBinding: vi.fn(),
	message: { warning: vi.fn(), error: vi.fn(), success: vi.fn() },
	confirm: vi.fn(),
	guard: vi.fn(),
	editor: {} as ReturnType<typeof useTemplateEditor>,
	binding: {} as ReturnType<typeof useTemplateBinding>,
	preview: {} as ReturnType<typeof useTemplatePreview>,
	printing: {} as ReturnType<typeof useStudentPrinting>,
	calibration: {} as ReturnType<typeof useOMRCalibration>,
	upload: {} as ReturnType<typeof useAnswerSheetSourceUpload>
}));
vi.mock("../../api/papers", () => ({
	listPapers: mocks.papers,
	listQuestions: mocks.questions
}));
vi.mock("../../api/configuration", () => ({
	listAnswerSheetTemplates: mocks.templates,
	getExamTemplateBinding: mocks.getBinding
}));
vi.mock("../../api/exams", () => ({ getExam: mocks.exam }));
vi.mock("./editor/useTemplateEditor", () => ({
	useTemplateEditor: () => mocks.editor
}));
vi.mock("./binding/useTemplateBinding", () => ({
	useTemplateBinding: () => mocks.binding
}));
vi.mock("./editor/useTemplatePreview", () => ({
	useTemplatePreview: () => mocks.preview
}));
vi.mock("./printing/useStudentPrinting", () => ({
	useStudentPrinting: () => mocks.printing
}));
vi.mock("./calibration/useOMRCalibration", () => ({
	useOMRCalibration: () => mocks.calibration
}));
vi.mock("./editor/useAnswerSheetSourceUpload", () => ({
	useAnswerSheetSourceUpload: () => mocks.upload
}));
vi.mock("../../router/useUnsavedChangesGuard", () => ({
	useUnsavedChangesGuard: mocks.guard
}));
vi.mock("../../components/PageState", () => ({
	EmptyState: ({
		title,
		description
	}: Readonly<{ title: string; description: string }>) => (
		<div>
			{title}
			{description}
		</div>
	),
	LoadingState: ({ label }: Readonly<{ label: string }>) => <div>{label}</div>,
	ErrorState: ({ message }: Readonly<{ message: string }>) => (
		<div>{message}</div>
	)
}));
vi.mock("../../components/StatusTag", () => ({
	StatusTag: ({
		children,
		tone
	}: Readonly<{ children: import("react").ReactNode; tone: string }>) => (
		<span data-tone={tone}>{children}</span>
	)
}));
vi.mock("antd", async () => {
	const React = await import("react");
	type Props = Readonly<
		Record<string, unknown> & { children?: import("react").ReactNode }
	>;
	const Box = ({ children }: Props) => <div>{children}</div>;
	const Button = ({ children, disabled, onClick, ...props }: Props) => (
		<button
			type="button"
			disabled={Boolean(disabled)}
			aria-label={props["aria-label"] as string | undefined}
			onClick={onClick as () => void}
		>
			{children}
		</button>
	);
	const Input = ({ value, disabled, onChange, ...props }: Props) => (
		<input
			aria-label={props["aria-label"] as string | undefined}
			value={String(value ?? "")}
			disabled={Boolean(disabled)}
			onChange={
				onChange as (
					event: import("react").ChangeEvent<HTMLInputElement>
				) => void
			}
		/>
	);
	const TextArea = ({ value, onChange }: Props) => (
		<textarea
			value={String(value ?? "")}
			onChange={
				onChange as (
					event: import("react").ChangeEvent<HTMLTextAreaElement>
				) => void
			}
		/>
	);
	const InputNumber = ({ value, disabled, onChange, ...props }: Props) => (
		<input
			type="number"
			aria-label={props["aria-label"] as string}
			value={Number(value)}
			disabled={Boolean(disabled)}
			onChange={(event) =>
				(onChange as (value: number) => void)(Number(event.target.value))
			}
		/>
	);
	const Select = ({ options, value, disabled, onChange, ...props }: Props) => (
		<select
			aria-label={String(
				props["aria-label"] ?? props.placeholder ?? "识别方式"
			)}
			value={String(value ?? "")}
			disabled={Boolean(disabled)}
			onChange={(event) =>
				(onChange as (value: string) => void)(event.target.value)
			}
		>
			{(options as { value: string; label: string }[]).map((item) => (
				<option key={item.value} value={item.value}>
					{item.label}
				</option>
			))}
		</select>
	);
	const Alert = ({ message, description, action, type }: Props) => (
		<div role="status" data-alert={String(message)} data-type={String(type)}>
			{message as import("react").ReactNode}
			{description as import("react").ReactNode}
			{action as import("react").ReactNode}
		</div>
	);
	const List = ({ dataSource, renderItem }: Props) => (
		<div>
			{(dataSource as unknown[]).map((item, index) =>
				React.createElement(
					React.Fragment,
					{ key: index },
					(
						renderItem as (
							item: unknown,
							index: number
						) => import("react").ReactNode
					)(item, index)
				)
			)}
		</div>
	);
	const Item = ({ children, actions, onClick }: Props) => (
		<div onClick={onClick as () => void}>
			{children}
			{actions as import("react").ReactNode}
		</div>
	);
	const Drawer = ({ children, open }: Props) =>
		open ? <aside data-drawer>{children}</aside> : null;
	const Modal = ({
		children,
		open,
		title,
		okText,
		okButtonProps,
		onOk
	}: Props) =>
		open ? (
			<section role="dialog">
				<h3>{String(title)}</h3>
				{children}
				<button
					type="button"
					disabled={Boolean(
						(okButtonProps as { disabled?: boolean })?.disabled
					)}
					onClick={onOk as () => void}
				>
					{String(okText)}
				</button>
			</section>
		) : null;
	const Spin = ({ tip }: Props) => (
		<span data-spin>{String(tip ?? "加载中")}</span>
	);
	const Progress = ({ percent, status }: Props) => (
		<output data-progress={String(status)}>{String(percent)}%</output>
	);
	const Checkbox = ({ children, checked, onChange }: Props) => (
		<label>
			<input
				type="checkbox"
				checked={Boolean(checked)}
				onChange={
					onChange as (
						event: import("react").ChangeEvent<HTMLInputElement>
					) => void
				}
			/>
			{children}
		</label>
	);
	const Group = ({ options, value, onChange }: Props) => (
		<div>
			{(options as string[]).map((option) => (
				<button
					key={option}
					type="button"
					onClick={() =>
						(onChange as (values: string[]) => void)([
							...(value as string[]),
							option
						])
					}
				>
					{option}
				</button>
			))}
		</div>
	);
	return {
		App: {
			useApp: () => ({
				message: mocks.message,
				modal: { confirm: mocks.confirm }
			})
		},
		Alert,
		Button,
		Input: Object.assign(Input, { TextArea }),
		InputNumber,
		Select,
		Space: Box,
		List: Object.assign(List, { Item }),
		Drawer,
		Modal,
		Spin,
		Table: Box,
		Progress,
		Upload: Object.assign(Box, { Dragger: Box, LIST_IGNORE: "ignore" }),
		Checkbox: Object.assign(Checkbox, { Group }),
		Collapse: Box
	};
});

describe("answer sheet page behavior before and after extraction", () => {
	let root: Root;
	let host: HTMLDivElement;
	beforeEach(() => {
		vi.resetAllMocks();
		Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
		mocks.papers.mockImplementation((_examId: string, purpose?: string) =>
			Promise.resolve({ papers: purpose ? [source] : [] })
		);
		mocks.questions.mockResolvedValue({ questions: [question] });
		mocks.templates.mockResolvedValue({ templates: [template] });
		mocks.exam.mockResolvedValue({ exam: { status: "configured" } });
		mocks.getBinding.mockResolvedValue({ binding: null });
		mocks.binding = {
			examBinding: null,
			bindingBusy: false,
			setExamBinding: vi.fn(),
			bindTemplateForExam: vi.fn(),
			activateTemplate: vi.fn(),
			releaseExamBinding: vi.fn()
		};
		mocks.preview = {
			pdfRef: { current: undefined },
			pageNo: 1,
			setPageNo: vi.fn(),
			preview: {
				loading: false,
				pageCount: 1,
				width: 100,
				height: 200,
				imageUrl: "image.png"
			}
		};
		mocks.editor = {
			layout: structuredClone(template.layout),
			name: template.name,
			setName: vi.fn(),
			setLayout: vi.fn(),
			saving: false,
			suggestingRegions: false,
			readonly: false,
			coveredQuestions: new Set([question.id]),
			currentPage: template.layout.pages[0],
			selectedRegion: undefined,
			selectedQuestionId: question.id,
			selectedQuestion: question,
			selectedRegionId: "",
			zoom: 1,
			setZoom: vi.fn(),
			canvasRef: { current: null },
			revision: 1,
			pageNo: 1,
			setPageNo: vi.fn(),
			setSelectedRegionId: vi.fn(),
			draftRect: undefined,
			onCanvasPointerDown: vi.fn(),
			onCanvasPointerMove: vi.fn(),
			onCanvasPointerUp: vi.fn(),
			beginRegionInteraction: vi.fn(),
			setSelectedQuestionId: vi.fn(),
			createDraft: vi.fn(),
			cloneTemplate: vi.fn(),
			suggestQuestionRegions: vi.fn(),
			saveDraft: vi.fn(),
			confirmLock: vi.fn(),
			confirmActivate: vi.fn(),
			activationError: undefined,
			createRegionWithoutDragging: vi.fn(),
			removeRegion: vi.fn(),
			updateRegion: vi.fn(),
			addOptionRegion: vi.fn(),
			updateOption: vi.fn(),
			removeOptionRegion: vi.fn(),
			setOMRProfile: vi.fn()
		};
		mocks.upload = {
			uploading: false,
			error: undefined,
			progress: 0,
			canRetry: false,
			upload: vi.fn(),
			retry: vi.fn()
		};
		mocks.calibration = {
			calibrations: [],
			loading: false,
			error: undefined,
			detail: undefined,
			selectedCase: undefined,
			caseId: "",
			imageUrl: undefined,
			imageLoading: false,
			action: undefined,
			drawerOpen: false,
			busy: false,
			setDrawerOpen: vi.fn(),
			setCaseId: vi.fn(),
			setAction: vi.fn(),
			setActionReason: vi.fn(),
			actionReason: "",
			expectedOptions: [],
			setExpectedOptions: vi.fn(),
			reload: vi.fn(),
			open: vi.fn(),
			start: vi.fn(),
			labelCase: vi.fn(),
			submitAction: vi.fn()
		};
		mocks.printing = {
			printContext: undefined,
			printContextError: undefined,
			printContextLoading: false,
			printModalOpen: false,
			selectedPrintClassIds: [],
			printBusy: false,
			printableCandidates: [],
			printClasses: [],
			setPrintModalOpen: vi.fn(),
			setSelectedPrintClassIds: vi.fn(),
			loadPrintContext: vi.fn(),
			openPrintModal: vi.fn(),
			downloadPrintBatch: vi.fn(),
			issuePrintPackage: vi.fn(),
			reprintCandidate: undefined, reprintReason: "", setReprintReason: vi.fn(), reprintError: undefined,
			reprintReasonFrozen: false, downloadPendingBatch: undefined, openReprint: vi.fn(), reprint: vi.fn(), closeReprint: vi.fn()
		};
		host = document.createElement("div");
		document.body.append(host);
		root = createRoot(host);
	});
	afterEach(async () => {
		await act(async () => root.unmount());
		host.remove();
	});
	async function render(canManage = true, canCalibrate = true) {
		await act(async () =>
			root.render(
				<AnswerSheetTemplatePage
					examId="exam"
					canManage={canManage}
					canCalibrate={canCalibrate}
				/>
			)
		);
	}
	function button(text: string) {
		return Array.from(host.querySelectorAll("button")).find(
			(item) => item.textContent === text
		)!;
	}
	function locked() {
		mocks.templates.mockResolvedValue({
			templates: [{ ...template, status: "locked" }]
		});
		mocks.editor.readonly = true;
	}
	function calibration(
		patch: Partial<OMRCalibrationCase> = {},
		status: OMRCalibrationSession["status"] = "draft",
		mismatch = 0,
		labeled = 1,
		total = 1
	) {
		mocks.calibration.drawerOpen = true;
		mocks.calibration.detail = detail(status, mismatch, labeled, total);
		mocks.calibration.selectedCase = sample(patch);
		mocks.calibration.detail.cases = [mocks.calibration.selectedCase];
	}

	it.each([
		[true, "configured", "上传空白答题卡"],
		[true, "preparation_confirmed", "考试准备已确认"],
		[false, "configured", "请联系考试负责人"]
	])(
		"keeps empty source permissions for %s / %s",
		async (canManage, status, text) => {
			mocks.papers.mockResolvedValue({ papers: [] });
			mocks.templates.mockResolvedValue({ templates: [] });
			mocks.exam.mockResolvedValue({ exam: { status } });
			await render(canManage as boolean);
			expect(host.textContent).toContain(text as string);
		}
	);

	it.each([
		[true, "bad", "image.png", "正在渲染页面"],
		[false, "bad", "image.png", "bad"],
		[false, undefined, "image.png", "答题卡第 1 页"]
	])(
		"preserves preview loading/error/image priority",
		async (loading, error, imageUrl, expected) => {
			mocks.preview.preview = {
				...mocks.preview.preview,
				loading: loading as boolean,
				error: error as string | undefined,
				imageUrl: imageUrl as string
			};
			await render();
			const canvas = host.querySelector(".template-canvas")!;
			if (expected === "答题卡第 1 页")
				expect(canvas.querySelector("img")?.alt).toBe(expected);
			else {
				expect(canvas.textContent).toContain(expected as string);
				expect(canvas.querySelector("img")).toBeNull();
			}
		}
	);

	it.each([
		["bound_auto", "template", "确认本场模板", "首张答卷自动识别"],
		["locked_with_guard", "other", "用于本场考试", "已绑定其他模板版本"],
		["locked_with_guard", "template", undefined, "已用于本场考试"]
	])("preserves binding states %s / %s", async (mode, id, action, text) => {
		locked();
		mocks.binding.examBinding = templateBinding(
			mode as "bound_auto" | "locked_with_guard",
			id as string
		);
		await render();
		expect(host.textContent).toContain(text as string);
		if (action) {
			await act(async () => button(action as string).click());
			expect(mocks.binding.bindTemplateForExam).toHaveBeenCalledWith(
				expect.objectContaining({ id: "template" })
			);
		} else expect(button("确认本场模板")).toBeUndefined();
	});

	it.each([
		[true, 2, 3, "可下载"],
		[false, 2, 3, "存在冲突"],
		[false, 0, 3, "已作废"],
		[false, 0, 0, "已开始扫描"]
	])(
		"preserves print batch status precedence",
		async (downloadable, conflict_count, revoked_count, expected) => {
			locked();
			mocks.printing.printContext = {
				exam_id: "exam",
				template_id: "template",
				template_content_hash: "hash",
				candidates: [],
				batches: [
					{
						print_batch_id: "batch",
						operation: "initial",
						issued_count: 1,
						observed_count: 0,
						issued_at: "2026-10-01T00:00:00Z",
						sheet_count: 1,
						page_count: 2,
						downloadable: downloadable as boolean,
						conflict_count: conflict_count as number,
						revoked_count: revoked_count as number
					}
				]
			};
			await render();
			expect(host.querySelector(".print-batch-row")?.textContent).toContain(
				expected as string
			);
			expect(Boolean(button("下载"))).toBe(downloadable);
		}
	);

	it.each([
		[[], "签发并下载", true],
		[["class-1"], "签发并下载（2 人）", false],
		[["class-1", "class-2"], "签发并下载（3 人）", false]
	] as const)(
		"keeps the selected class count and issue boundary for %s",
		async (selected, text, disabled) => {
			mocks.printing.printModalOpen = true;
			mocks.printing.selectedPrintClassIds = [...selected];
			mocks.printing.printClasses = [
				{ id: "class-1", name: "一班", count: 2 },
				{ id: "class-2", name: "二班", count: 1 }
			];
			mocks.printing.printableCandidates = [
				"class-1",
				"class-1",
				"class-2"
			].map((classId, index) => ({
				student_id: `student-${index}`,
				student_no: `${index}`,
				student_name: `学生${index}`,
				class_id: classId,
				class_name: classId,
				attendance_status: "expected",
				has_active_sheet: false
			}));
			await render();
			const issue = button(text);
			expect(issue.disabled).toBe(disabled);
			await act(async () => issue.click());
			expect(mocks.printing.issuePrintPackage).toHaveBeenCalledTimes(
				disabled ? 0 : 1
			);
			const selectionText = selected.length === 2 ? "取消全选" : "全选";
			await act(async () => button(selectionText).click());
			expect(mocks.printing.setSelectedPrintClassIds).toHaveBeenCalledWith(
				selected.length === 2 ? [] : ["class-1", "class-2"]
			);
		}
	);

	it.each([
		["登记失败", true, false, true],
		["登记失败", false, false, false],
		[undefined, true, true, false]
	] as const)(
		"preserves source upload error/progress priority and retry boundary",
		async (error, canRetry, showProgress, showRetry) => {
			mocks.upload.error = error;
			mocks.upload.uploading = true;
			mocks.upload.progress = 42;
			mocks.upload.canRetry = canRetry;
			await render();
			expect(Boolean(host.querySelector("output[data-progress]"))).toBe(
				showProgress
			);
			expect(Boolean(button("重试保存答题卡"))).toBe(showRetry);
			if (showRetry) {
				expect(button("重试保存答题卡").disabled).toBe(true);
				await act(async () => button("重试保存答题卡").click());
				expect(mocks.upload.retry).not.toHaveBeenCalled();
			}
			if (error)
				expect(
					host.querySelector(`[data-alert='${error}']`)?.textContent
				).toContain(error);
		}
	);

	it.each([
		["approved", "success", "校准已生效"],
		["revoked", "error", "撤销已生效"],
		["draft", "info", "请只依据答题图片盲标"]
	])("preserves calibration status %s", async (status, type, text) => {
		calibration({}, status as OMRCalibrationSession["status"]);
		await render();
		const alert = host.querySelector(
			`[data-alert="状态：${({ approved: "已批准", revoked: "已撤销", draft: "待标注" } as Record<string, string>)[status]}"]`
		)!;
		expect(alert.getAttribute("data-type")).toBe(type);
		expect(alert.textContent).toContain(text);
	});

	it.each([
		[1, 1, 1, "exception"],
		[0, 1, 1, "success"],
		[0, 0, 0, "active"]
	])(
		"preserves mismatch/progress precedence",
		async (mismatch, labeled, total, expected) => {
			calibration(
				{},
				"draft",
				mismatch as number,
				labeled as number,
				total as number
			);
			await render();
			expect(
				host
					.querySelector(".calibration-drawer output")
					?.getAttribute("data-progress")
			).toBe(expected);
		}
	);

	it.each([
		[true, "sample.png", "正在加载答题图片"],
		[false, "sample.png", "校准样本 1"],
		[false, undefined, "图片加载失败"]
	])(
		"preserves calibration image precedence",
		async (loading, url, expected) => {
			calibration();
			mocks.calibration.imageLoading = loading as boolean;
			mocks.calibration.imageUrl = url as string | undefined;
			await render();
			const crop = host.querySelector(".calibration-crop")!;
			if (url && !loading)
				expect(crop.querySelector("img")?.alt).toBe(expected);
			else {
				expect(crop.textContent).toContain(expected as string);
				expect(crop.querySelector("img")).toBeNull();
			}
		}
	);

	it.each([
		[true, "selected_high", "一致"],
		[false, "selected_high", "高置信错误，阻断审批"],
		[false, "selected_low", "不一致，但该分支仍保持人工复核"]
	])(
		"preserves labeled sample result precedence",
		async (matches, stratum, expected) => {
			calibration({
				matches: matches as boolean,
				sample_stratum: stratum as OMRCalibrationCase["sample_stratum"],
				expected_options: [],
				observed_options: []
			});
			await render();
			expect(
				host.querySelector("[data-alert='人工标注：空白']")?.textContent
			).toContain(expected as string);
		}
	);

	it.each([
		[undefined, "弃用校准草稿", "弃用草稿"],
		["approve", "批准自动确认校准", "确认批准"],
		["revoke", "撤销自动确认校准", "立即撤销"],
		["discard", "弃用校准草稿", "弃用草稿"]
	])(
		"preserves calibration action modal defaults",
		async (action, title, okText) => {
			mocks.calibration.action = action as ReturnType<
				typeof useOMRCalibration
			>["action"];
			await render();
			const dialog = host.querySelector("[role=dialog]");
			if (!action) expect(dialog).toBeNull();
			else {
				expect(dialog?.textContent).toContain(title);
				await act(async () => button(okText).click());
				expect(mocks.calibration.submitAction).toHaveBeenCalledTimes(1);
			}
		}
	);

	it("retains the mounted name input, focus and draft while rerendering read-only state", async () => {
		await render();
		const input = host.querySelector(
			".template-inspector input"
		) as HTMLInputElement;
		input.focus();
		mocks.editor.name = "未保存草稿";
		mocks.editor.readonly = true;
		await render(false);
		const next = host.querySelector(
			".template-inspector input"
		) as HTMLInputElement;
		expect(next).toBe(input);
		expect(document.activeElement).toBe(input);
		expect(next.value).toBe("未保存草稿");
		expect(next.disabled).toBe(true);
		expect(host.textContent).toContain("模板有未保存修改");
		expect(button("确认区域并用于本场考试").disabled).toBe(true);
		expect(mocks.guard).toHaveBeenLastCalledWith(
			expect.objectContaining({ dirty: true })
		);
	});

	it.each(["saving", "suggesting", "readonly", "questions"])(
		"blocks draft lock when %s",
		async (boundary) => {
			if (boundary === "saving") mocks.editor.saving = true;
			if (boundary === "suggesting") mocks.editor.suggestingRegions = true;
			if (boundary === "readonly") mocks.editor.readonly = true;
			if (boundary === "questions")
				mocks.questions.mockResolvedValue({ questions: [] });
			await render();
			expect(button("确认区域并用于本场考试").disabled).toBe(true);
		}
	);

	it("offers one activation action that can save the current dirty layout", async () => {
		mocks.editor.name = "changed";
		await render();
		expect(button("确认区域并用于本场考试").disabled).toBe(false);
		await act(async () => button("确认区域并用于本场考试").click());
		expect(mocks.editor.confirmActivate).toHaveBeenCalledTimes(1);
	});

	it("requires confirmation before clearing an unsaved layout on source switch", async () => {
		mocks.papers.mockImplementation((_examId: string, purpose?: string) =>
			Promise.resolve({
				papers: purpose ? [source, { ...source, id: "other" }] : []
			})
		);
		mocks.editor.name = "changed";
		await render();
		const select = host.querySelector(
			"[aria-label='答题卡源文件']"
		) as HTMLSelectElement;
		await act(async () => {
			select.value = "other";
			select.dispatchEvent(new Event("change", { bubbles: true }));
		});
		expect(mocks.editor.setLayout).not.toHaveBeenCalled();
		expect(mocks.confirm).toHaveBeenCalledTimes(1);
		await act(async () => mocks.confirm.mock.calls[0][0].onOk());
		expect(mocks.editor.setLayout).toHaveBeenCalledWith({ pages: [] });
	});

	it("keeps coordinate inputs in normalized page bounds", async () => {
		mocks.editor.selectedRegion = { ...region };
		mocks.editor.selectedRegionId = region.id;
		await render();
		const input = host.querySelector(
			"[aria-label='答题区域左']"
		) as HTMLInputElement;
		await act(async () => {
			const setter = Object.getOwnPropertyDescriptor(
				HTMLInputElement.prototype,
				"value"
			)!.set!;
			setter.call(input, "120");
			input.dispatchEvent(new Event("input", { bubbles: true }));
			input.dispatchEvent(new Event("change", { bubbles: true }));
		});
		expect(mocks.editor.updateRegion).toHaveBeenCalledWith(
			"r1",
			expect.objectContaining({ x: 0.99, width: 1 - 0.99, y: 0.2, height: 0.4 })
		);
	});

	it.each([false, true])("选中状态为 %s 时选项标签保留原文、几何位置和题框交互", async (selected) => {
		const option = { id: "option-a", label: "A长标签123456789012", x: 0.2, y: 0.3, width: 0.25, height: 0.1 };
		const currentRegion = { ...region, option_regions: [option] };
		const currentPage = { ...template.layout.pages[0], question_regions: [currentRegion] };
		mocks.editor.currentPage = currentPage;
		mocks.editor.layout = { pages: [currentPage] };
		mocks.editor.selectedRegionId = selected ? region.id : "";
		await render();
		const marker = host.querySelector<HTMLElement>(".template-option-region")!;
		const label = marker.querySelector(":scope > span")!;
		expect(label.textContent).toBe(option.label);
		expect(marker.title).toBe(`选项 ${option.label}`);
		expect(marker.style.left).toBe("20%");
		expect(marker.style.top).toBe("30%");
		expect(marker.style.width).toBe("25%");
		expect(marker.style.height).toBe("10%");
		expect(marker.parentElement?.classList.contains("selected")).toBe(selected);
		expect(host.querySelector(".template-canvas > img")?.getAttribute("src")).toBe("image.png");
		await act(async () => { label.dispatchEvent(new Event("pointerdown", { bubbles: true })); });
		expect(mocks.editor.beginRegionInteraction).toHaveBeenCalledWith(expect.anything(), currentRegion, "move");
	});
});

