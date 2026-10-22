// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AnswerSheetTemplatePage } from "../../pages/AnswerSheetTemplatePage";
import type {
	OMRCalibrationCase,
	OMRCalibrationSession,
  TemplateEditorCandidate
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
vi.mock("../../api/answerSheetTemplateLibrary", () => ({
  listAnswerSheetTemplateLibrary: async () => ({ libraries: [] }),
  resolveAnswerSheetTemplate: async () => ({ status: "unknown", candidates: [] }),
  instantiateAnswerSheetTemplate: vi.fn(), publishAnswerSheetTemplate: vi.fn()
}));
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
  const { MockTemplateSourceDropdown, MockTemplatePopover } = await import("./testing/MockTemplateSourceDropdown");
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
			title={props.title as string | undefined}
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
    Dropdown: { Button: MockTemplateSourceDropdown },
    Popover: MockTemplatePopover,
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
		mocks.getBinding.mockResolvedValue({ binding: null, revision: 0 });
		mocks.binding = {
			examBinding: null,
			bindingBusy: false,
			bindingRevision: 0, setBindingState: vi.fn(),
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
				sourceId: source.id,
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
			suggestionProgress: undefined,
			suggestionError: undefined,
			suggestionModel: undefined,
			suggestionTone: "info",
			unassignedRegions: new Map(),
			objectiveGroups: new Map(),
      fillGradingGroups: new Map(),
      fillGradingGroup: undefined,
      setFillGradingMode: vi.fn(),
      setFillGradingRegion: vi.fn(),
      resetFillGradingGroups: vi.fn(),
			discardObjectiveGroup: vi.fn(),
			assignSuggestedRegion: vi.fn(),
			assignSuggestedIdentity: vi.fn(),
			reviewMessage: "",
			confirmRegion: vi.fn(),
			confirmPageRegions: vi.fn(),
			canUndo: false,
			canRedo: false,
			undo: vi.fn(),
			redo: vi.fn(),
			discardSuggestedRegion: vi.fn(),
			readonly: false,
			coveredQuestions: new Set([question.id]),
			currentPage: template.layout.pages[0],
			selectedRegion: undefined,
      selectedRegionKind: undefined,
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
			cancelInteraction: vi.fn(),
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
      createIdentityRegion: vi.fn(),
			removeRegion: vi.fn(),
			updateRegion: vi.fn(),
			addOptionRegion: vi.fn(),
			updateOption: vi.fn(),
			removeOptionRegion: vi.fn(),
			setOMRProfile: vi.fn()
		};
		mocks.upload = {
			uploading: false,
			phase: "idle",
			error: undefined,
			progress: 0,
			canRetry: false,
			upload: vi.fn(),
			uploadBatch: vi.fn(),
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
	async function render(canManage = true, canCalibrate = true, openSettings = true) {
		await act(async () =>
			root.render(
				<AnswerSheetTemplatePage
					examId="exam"
					canManage={canManage}
					canCalibrate={canCalibrate}
				/>
			)
		);
    // 旧功能回归显式打开按需设置；默认布局由独立用例验证没有常驻属性栏。
    if (openSettings) await openSettingsDialog();
	}
	async function openSettingsDialog() {
    if (host.querySelector(".template-settings-content")) return;
    const more = host.querySelector<HTMLButtonElement>('button[aria-label="更多操作"]');
    if (!more) return;
    await act(async () => more.click());
    await act(async () => button("模板设置").click());
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
  function savedPendingCandidate(role: "uncertain" | "objective_group") {
    const candidate: TemplateEditorCandidate = {
      id: "saved-candidate", page_no: 1, role,
      region: { ...region, id: "saved-candidate", question_id: undefined,
        origin: "model", review_state: "suggested", semantic_check: "needs_review" }
    };
    const savedLayout = structuredClone(template.layout);
    savedLayout.editor_state = { version: 1, source_sha256: source.file.hash_sha256!, candidates: [candidate] };
    mocks.templates.mockResolvedValue({ templates: [{ ...template, layout: savedLayout }] });
    mocks.editor.layout = structuredClone(savedLayout);
    mocks.editor.reviewMessage = "仍有待核对区域，请先核对全部候选。";
    if (role === "objective_group") mocks.editor.objectiveGroups = new Map([[1, [candidate.region]]]);
    else mocks.editor.unassignedRegions = new Map([[1, [candidate.region]]]);
    mocks.papers.mockImplementation((_examId: string, purpose?: string) =>
      Promise.resolve({ papers: purpose ? [source, { ...source, id: "other" }] : [] }));
    return candidate;
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
		[true, "configured", "上传答题卡"],
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
			expect(host.textContent).not.toContain("答题卡区域尚未确认");
			expect(button("确认答题卡设置")).toBeUndefined();
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
		["bound_auto", "template", "确认答题卡设置", "首张答卷自动识别"],
		["locked_with_guard", "other", "确认答题卡设置", "已绑定其他模板版本"],
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
		} else {
			expect(button("确认答题卡设置")).toBeUndefined();
			expect(host.textContent).toContain("使用设置已确认");
			expect(host.querySelector(".template-toolbar")?.textContent).not.toContain("答题卡区域");
		}
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

	it("答题卡设置保持普通页、模板库可见且不锁定页面滚动", async () => {
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "clip";
    try {
      await render(true, true, false);
      const page = host.querySelector(".template-editor-page")!;
      expect(page.classList.contains("is-workbench-fullscreen")).toBe(false);
      expect(host.querySelector(".template-workbench")?.classList.contains("is-fullscreen")).toBe(false);
      expect(host.querySelector<HTMLElement>('[aria-label="模板库与考试配置"]')?.hidden).toBe(false);
      expect(host.querySelector('[aria-label="打开全屏工作台"]')).toBeNull();
      expect(host.querySelector('[aria-label="返回考试管理"]')).toBeNull();
      expect(document.body.style.overflow).toBe("clip");
      await act(async () => host.querySelector<HTMLSelectElement>('[aria-label="选择要配置的题目"]')!.dispatchEvent(new Event("change", { bubbles: true })));
      expect(host.querySelector("#template-question-pane")).not.toBeNull();
      expect(host.querySelector(".template-inspector")).toBeNull();
      expect(host.querySelector(".template-settings-content")).toBeNull();
      expect(host.querySelector('[aria-label="切换题目列表"]')).toBeNull();
      expect(host.querySelector('[aria-label="切换属性面板"]')).toBeNull();
      expect(host.querySelector(".template-question-toggle")).toBeNull();
    } finally {
      document.body.style.overflow = previousOverflow;
    }
  });
	it("retains the mounted name input, focus and draft while rerendering read-only state", async () => {
		await render();
		const input = host.querySelector(
			".template-settings-content input"
		) as HTMLInputElement;
		input.focus();
		mocks.editor.name = "未保存草稿";
		mocks.editor.readonly = true;
		await render(false);
		const next = host.querySelector(
			".template-settings-content input"
		) as HTMLInputElement;
		expect(next).toBe(input);
		expect(document.activeElement).toBe(input);
		expect(next.value).toBe("未保存草稿");
		expect(next.disabled).toBe(true);
  expect(host.querySelector(".template-save-state")).toBeNull();
		expect(button("确认答题卡设置").disabled).toBe(true);
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
			expect(button("确认答题卡设置").disabled).toBe(true);
		}
	);

	it("offers one activation action that can save the current dirty layout", async () => {
		mocks.editor.name = "changed";
		await render();
		expect(button("确认答题卡设置").disabled).toBe(false);
		expect(host.querySelector("#template-exam-binding")?.contains(button("确认答题卡设置"))).toBe(true);
		expect(host.querySelector(".template-toolbar")?.textContent).not.toContain("答题卡区域尚未确认");
		expect(host.querySelector('[data-alert="答题卡区域尚未确认"]')).toBeNull();
		await act(async () => button("确认答题卡设置").click());
		expect(mocks.editor.confirmActivate).toHaveBeenCalledTimes(1);
	});

	it("只有点击模板设置才显示确认区，选择左侧题目保持画布工作区", async () => {
		await render(true, true, false);
    expect(host.querySelector("#template-exam-binding")).toBeNull();
    await act(async () => host.querySelector<HTMLSelectElement>('[aria-label="选择要配置的题目"]')!.dispatchEvent(new Event("change", { bubbles: true })));
    expect(host.querySelector(".template-settings-content")).toBeNull();
    await openSettingsDialog();
    expect(host.querySelector("#template-exam-binding")).not.toBeNull();
		expect(mocks.editor.confirmActivate).not.toHaveBeenCalled();
	});

	it("只看未配置题目保留原选择入口和题目切换动作", async () => {
		mocks.questions.mockResolvedValue({ questions: [question, { ...question, id: "missing", question_no: "2" }] });
		await render();
		const pane = host.querySelector<HTMLElement>("#template-question-pane")!;
		expect(Array.from(pane.querySelectorAll("strong"), item => item.textContent)).toEqual(["题目", "1", "2"]);
		await act(async () => pane.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
		expect(Array.from(pane.querySelectorAll("strong"), item => item.textContent)).toEqual(["题目", "2"]);
		expect(pane.querySelectorAll('select[aria-label="选择要配置的题目"] option')).toHaveLength(2);
		await act(async () => pane.querySelectorAll("strong")[1].parentElement!.parentElement!.click());
		expect(mocks.editor.setSelectedQuestionId).toHaveBeenCalledWith("missing");
	});

	it("未框选完全部题目时显示缺项，不开放确认或显示已完成", async () => {
		mocks.editor.coveredQuestions = new Set();
		await render();
		expect(host.textContent).toContain("还有 1 道题未配置答题区域");
		expect(host.textContent).not.toContain("答题卡区域检查已通过");
		expect(button("确认答题卡设置").disabled).toBe(true);
		await act(async () => button("确认答题卡设置").click());
		expect(mocks.editor.confirmActivate).not.toHaveBeenCalled();
	});

	it.each(["unbound", "automatic", "hash", "exam", "coverage"])("已锁定模板在 %s 缺项时仍待确认", async boundary => {
		locked();
		mocks.binding.examBinding = templateBinding("locked_with_guard", "template");
		if (boundary === "unbound") mocks.binding.examBinding = null;
		if (boundary === "automatic") mocks.binding.examBinding!.mode = "bound_auto";
		if (boundary === "hash") mocks.binding.examBinding!.template_content_hash = "different";
		if (boundary === "exam") mocks.binding.examBinding!.exam_id = "other-exam";
		if (boundary === "coverage") {
			mocks.questions.mockResolvedValue({ questions: [question, { ...question, id: "missing", question_no: "2" }] });
			mocks.binding.examBinding = null;
		}
		await render();
		expect(host.textContent).not.toContain("使用设置已确认");
		expect(host.querySelector(".template-toolbar")?.textContent).not.toContain("还有");
		expect(button("确认答题卡设置").disabled).toBe(boundary === "coverage");
	});

	it("选中已绑定旧版本时不在顶部重复显示锁定模板说明", async () => {
		const oldLocked = { ...template, status: "locked", version_no: 1 };
		const latestLocked = { ...template, id: "latest", status: "locked", version_no: 3,
			layout: { ...template.layout, pages: template.layout.pages.map(page => ({ ...page, question_regions: [] })) } };
		mocks.templates.mockResolvedValue({ templates: [oldLocked, latestLocked] });
		mocks.binding.examBinding = templateBinding("locked_with_guard", "template");
		mocks.editor.readonly = true;
		await render();
		expect(host.textContent).not.toContain("本场最新已锁定模板");
		expect(host.textContent).not.toContain("答题卡区域检查已通过");
		expect(host.textContent).toContain("使用设置已确认");
	});

	it("其他已锁定模板不代替当前版本的确认覆盖校验", async () => {
		const oldLocked = { ...template, status: "locked", version_no: 1,
			layout: { ...template.layout, pages: template.layout.pages.map(page => ({ ...page, question_regions: [] })) } };
		const latestLocked = { ...template, id: "latest", status: "locked", version_no: 3 };
		const foreignLocked = { ...oldLocked, id: "foreign", exam_id: "other-exam", version_no: 99 };
		mocks.templates.mockResolvedValue({ templates: [oldLocked, foreignLocked, latestLocked] });
		mocks.editor.readonly = true;
		await render();
		expect(host.textContent).not.toContain("答题卡区域检查已通过");
		expect(host.textContent).not.toContain("本场最新已锁定模板");
		expect(host.textContent).toContain("使用设置待确认");
		expect(button("确认答题卡设置").disabled).toBe(true);
	});

	it("绑定失败后的已锁定区域仍已通过检查，并保留使用设置重试入口", async () => {
		locked();
		mocks.editor.activationError = "区域已保存并锁定，但尚未用于本场考试。请点击“确认答题卡设置”重试。";
		await render();
		expect(host.querySelector(".template-toolbar")?.textContent).not.toContain("答题卡区域检查");
		expect(host.textContent).toContain("使用设置待确认");
		expect(host.textContent).toContain("区域已保存并锁定");
		expect(button("确认答题卡设置").disabled).toBe(false);
		await act(async () => button("确认答题卡设置").click());
		expect(mocks.binding.bindTemplateForExam).toHaveBeenCalledOnce();
		expect(mocks.editor.confirmActivate).not.toHaveBeenCalled();
	});

	it.each(["ready", "collecting", "grading", "archived"])("考试 %s 后保留确认入口但禁止变更", async status => {
		mocks.exam.mockResolvedValue({ exam: { status } });
		await render();
		expect(button("确认答题卡设置").disabled).toBe(true);
		expect(host.textContent).toContain("考试准备已冻结");
		await act(async () => button("确认答题卡设置").click());
		expect(mocks.editor.confirmActivate).not.toHaveBeenCalled();
	});

	it("读取考试失败仍显示错误，不因资料归属尚未加载而一直显示加载中", async () => {
		mocks.exam.mockRejectedValue(new Error("synthetic exam load failure"));
		await render();
		expect(host.textContent).not.toContain("正在加载答题卡模板");
		expect(host.textContent).toContain("操作失败");
		expect(button("确认答题卡设置")).toBeUndefined();
	});

	it("读取空绑定时把解绑后的游标一起传给绑定生命周期", async () => {
		mocks.getBinding.mockResolvedValue({ binding: null, revision: 2 });
		await render();
		expect(mocks.binding.setBindingState).toHaveBeenCalledWith({ binding: null, revision: 2 });
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
			"[aria-label='答题卡文件']"
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

  it.each(["uncertain", "objective_group"] as const)("已保存的 %s 候选允许离开及切换来源，最终确认仍需核对", async role => {
    savedPendingCandidate(role);
    await render();
    expect(mocks.guard).toHaveBeenLastCalledWith(expect.objectContaining({ dirty: false, busy: false }));
    expect(host.querySelector(".template-unsaved-note")).toBeNull();
    expect(button("确认答题卡设置").disabled).toBe(true);
    expect(button("核对并收录模板库").disabled).toBe(true);
    const select = host.querySelector<HTMLSelectElement>('[aria-label="答题卡文件"]')!;
    await act(async () => { select.value = "other"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(mocks.confirm).not.toHaveBeenCalled();
    expect(mocks.editor.setLayout).toHaveBeenCalledWith({ pages: [] });
    expect(mocks.editor.confirmActivate).not.toHaveBeenCalled();
  });

  it.each(["新增", "移动", "丢弃", "转换"] as const)("候选%s后未保存仍保护刷新及来源切换", async change => {
    savedPendingCandidate("uncertain");
    const layout = mocks.editor.layout;
    const candidate = layout.editor_state!.candidates[0];
    if (change === "新增") layout.editor_state!.candidates.push({ ...candidate, id: "new-candidate",
      region: { ...candidate.region, id: "new-candidate", x: .6 } });
    if (change === "移动") candidate.region.x = .25;
    if (change === "丢弃") {
      candidate.region.review_state = "discarded";
      mocks.editor.unassignedRegions = new Map();
    }
    if (change === "转换") {
      layout.editor_state!.candidates = [];
      layout.pages[0].identity_regions.push({ ...candidate.region, label: "姓名", review_state: "edited" });
      mocks.editor.unassignedRegions = new Map();
    }
    await render();
    expect(mocks.guard).toHaveBeenLastCalledWith(expect.objectContaining({ dirty: true }));
    expect(host.querySelector(".template-unsaved-note")).toBeNull();
    expect(host.querySelector(".template-save-state")).toBeNull();
    const select = host.querySelector<HTMLSelectElement>('[aria-label="答题卡文件"]')!;
    await act(async () => { select.value = "other"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(mocks.confirm).toHaveBeenCalledTimes(1);
    expect(mocks.editor.setLayout).not.toHaveBeenCalled();
  });

	it.each(["draft", "locked"])("%s 工具栏从答题卡文件与操作开始，移除标题、状态与旧说明", async status => {
		mocks.papers.mockImplementation((_examId: string, purpose?: string) => Promise.resolve({ papers: purpose ? [source, { ...source, id: "other", version_no: 9, file: { ...source.file, original_name: "另一份答题卡.png" } }] : [] }));
		mocks.templates.mockResolvedValue({ templates: [{ ...template, status }, { ...template, id: "locked-version", version_no: 3, status: "locked" }, { ...template, id: "retired-version", version_no: 4, status: "retired" }, { ...template, id: "other-template", exam_paper_id: "other", version_no: 99 }] });
		await render();
		const fileSelect = host.querySelector<HTMLSelectElement>('select[aria-label="答题卡文件"]')!;
		expect(host.querySelector('select[aria-label="区域模板"]')).toBeNull();
		expect(fileSelect.title).toBe("答题卡文件");
		expect(Array.from(fileSelect.options, option => option.text)).toEqual(["blank.pdf", "另一份答题卡.png"]);
		expect(button("刷新")).toBeUndefined();
		const toolbar = host.querySelector(".template-toolbar")!;
		expect(toolbar.querySelector("h2")).toBeNull();
		expect(toolbar.querySelector(".template-save-state")).toBeNull();
		expect(toolbar.firstElementChild?.getAttribute("aria-label")).toBe("答题卡文件与操作");
		expect(host.textContent).not.toContain("保存草稿不会完成区域检查");
		expect(host.querySelector(".template-library-panel > details")?.hasAttribute("open")).toBe(false);
		expect(host.textContent).not.toContain("OpenCV");
		expect(host.textContent).not.toContain("未匹配到已有模板，正在准备新模板候选");
	});

	it("切换到无模板的文件后提示创建区域模板，保留旧文件的区域版本", async () => {
		mocks.papers.mockImplementation((_examId: string, purpose?: string) => Promise.resolve({ papers: purpose ? [source, { ...source, id: "other" }] : [] }));
		await render();
		const fileSelect = host.querySelector<HTMLSelectElement>('select[aria-label="答题卡文件"]')!;
		await act(async () => { fileSelect.value = "other"; fileSelect.dispatchEvent(new Event("change", { bubbles: true })); });
		expect(button("新建区域模板")).toBeDefined();
		expect(host.querySelector('select[aria-label="区域模板"]')).toBeNull();
		await act(async () => { fileSelect.value = source.id; fileSelect.dispatchEvent(new Event("change", { bubbles: true })); });
		expect(button("识别区域")).toBeDefined();
	});

	it("自动识别显示连续处理的页数与失败信息，失败后可重试整份答题卡", async () => {
		mocks.editor.suggestingRegions = true;
		Object.assign(mocks.editor, { suggestionProgress: { completed: 2, total: 4 } });
		await render();
		expect(host.textContent).toContain("已处理 2 / 4 页");
		expect(button("识别区域").disabled).toBe(true);
		expect(button("保存草稿").disabled).toBe(true);
		mocks.editor.suggestingRegions = false;
		Object.assign(mocks.editor, { suggestionError: "第 3 页识别失败，请重试" });
		await render();
		expect(host.textContent).toContain("第 3 页识别失败，请重试");
		expect(button("识别区域").disabled).toBe(false);
		await act(async () => button("识别区域").click());
		expect(mocks.editor.suggestQuestionRegions).toHaveBeenCalledOnce();
	});

	it("部分页失败直接显示原因，已完成页模型名称放入更多操作", async () => {
		Object.assign(mocks.editor, { suggestionError: "第 1 页多模态后台分析未完成", suggestionModel: "vision-model" });
		await render(true, true, false);
		expect(host.textContent).toContain("第 1 页多模态后台分析未完成");
		expect(host.textContent).not.toContain("vision-model");
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="更多操作"]')!.click());
		expect(host.textContent).toContain("已完成页使用：vision-model（多模态）");
		expect(host.textContent).not.toContain("本次使用：");
	});

	it("打开答题卡不会直接识别，点击按钮才开始且不要求先有全部题目", async () => {
		mocks.questions.mockResolvedValue({ questions: [] });
		await render();
		expect(mocks.editor.suggestQuestionRegions).not.toHaveBeenCalled();
		expect(button("识别区域").disabled).toBe(false);
		await act(async () => button("识别区域").click());
		expect(mocks.editor.suggestQuestionRegions).toHaveBeenCalledOnce();
	});

	it.each(["info", "warning", "error", undefined] as const)("识别状态 %s 按实际结果等级显示", async tone => {
		mocks.editor.suggestionError = "识别到部分答题框，尚未关联题目";
		if (tone) mocks.editor.suggestionTone = tone;
		else delete (mocks.editor as Partial<typeof mocks.editor>).suggestionTone;
		await render();
		expect(host.querySelector('[data-alert="识别到部分答题框，尚未关联题目"]')?.getAttribute("data-type")).toBe(tone ?? "warning");
	});

	it("来源两页但旧区域只有一页时，按通用页码选择和翻页查看第二页", async () => {
		mocks.preview.preview.pageCount = 2;
		vi.mocked(mocks.preview.setPageNo).mockImplementation(value => {
			mocks.preview.pageNo = typeof value === "function" ? value(mocks.preview.pageNo) : value;
		});
		await render();
		expect(host.textContent).toContain("第 1 / 2 页");
		const pages = host.querySelector<HTMLSelectElement>('[aria-label="跳转答题卡页码"]')!;
		expect(Array.from(pages.options, option => option.text)).toEqual(["第 1 页", "第 2 页"]);
		expect(host.querySelector('[aria-label="答题卡正反面"]')).toBeNull();
		expect(host.textContent).not.toContain("正面");
		expect(host.textContent).not.toContain("反面");
		await act(async () => { pages.value = "2"; pages.dispatchEvent(new Event("change", { bubbles: true })); });
		expect(mocks.preview.setPageNo).toHaveBeenCalledWith(2);
		await render();
		expect(host.textContent).toContain("第 2 / 2 页");
		expect(host.querySelector<HTMLButtonElement>('[aria-label="下一页"]')?.disabled).toBe(true);
		await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="上一页"]')!.click());
		await render();
		await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="下一页"]')!.click());
		await render();
		expect(host.textContent).toContain("第 2 / 2 页");
	});

	it.each([3, 4])("来源有 %s 页而区域只有一页时，可按真实页数跳转并连续翻完全部页面", async pageCount => {
		mocks.preview.preview.pageCount = pageCount;
		vi.mocked(mocks.preview.setPageNo).mockImplementation(value => {
			mocks.preview.pageNo = typeof value === "function" ? value(mocks.preview.pageNo) : value;
		});
		await render();
		const pages = host.querySelector<HTMLSelectElement>('[aria-label="跳转答题卡页码"]')!;
		expect(pages.options).toHaveLength(pageCount);
		expect(host.textContent).not.toContain("正面");
		expect(host.textContent).not.toContain("反面");
		await act(async () => { pages.value = String(pageCount); pages.dispatchEvent(new Event("change", { bubbles: true })); });
		expect(mocks.preview.setPageNo).toHaveBeenCalledWith(pageCount);
		await render();
		expect(host.querySelector<HTMLButtonElement>('[aria-label="下一页"]')?.disabled).toBe(true);
		await act(async () => { pages.value = "1"; pages.dispatchEvent(new Event("change", { bubbles: true })); });
		await render();
		for (let nextPage = 2; nextPage <= pageCount; nextPage++) {
			expect(host.querySelector<HTMLButtonElement>('[aria-label="下一页"]')?.disabled).toBe(false);
			await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="下一页"]')!.click());
			await render();
			expect(host.textContent).toContain(`第 ${nextPage} / ${pageCount} 页`);
		}
		expect(host.querySelector<HTMLButtonElement>('[aria-label="下一页"]')?.disabled).toBe(true);
	});

	it("无题号候选只在当前页展示，点框后须显式关联题目才修改区域", async () => {
		mocks.editor.coveredQuestions = new Set();
		mocks.editor.unassignedRegions = new Map([
			[1, [{ ...region, id: "candidate-front", question_id: undefined }]],
			[2, [{ ...region, id: "candidate-back", question_id: undefined }]]
		]);
		await render();
		expect(host.querySelectorAll(".template-unassigned-region")).toHaveLength(1);
  expect(host.textContent).toContain("区域待核对，可先保存草稿");
		const candidate = host.querySelector<HTMLElement>(".template-unassigned-region")!;
		await act(async () => { candidate.dispatchEvent(new Event("pointerdown", { bubbles: true })); candidate.click(); });
		expect(mocks.editor.onCanvasPointerDown).not.toHaveBeenCalled();
		expect(mocks.editor.assignSuggestedRegion).not.toHaveBeenCalled();
		await act(async () => button("关联第 1 题").click());
		expect(mocks.editor.assignSuggestedRegion).toHaveBeenCalledWith("candidate-front", question.id);
		await act(async () => button("丢弃此区域").click());
		expect(mocks.editor.discardSuggestedRegion).toHaveBeenCalledWith("candidate-front");
	});

	it("候选关联选择不能跨到另一页，已有题框不得被候选覆盖", async () => {
		mocks.editor.unassignedRegions = new Map([
			[1, [{ ...region, id: "candidate-shared", question_id: undefined }]],
			[2, [{ ...region, id: "candidate-shared", question_id: undefined }]]
		]);
		await render();
		await act(async () => host.querySelector<HTMLElement>(".template-unassigned-region")!.click());
		expect(button("关联第 1 题").disabled).toBe(true);
		mocks.preview.pageNo = 2;
		await render();
		expect(button("关联第 1 题")).toBeUndefined();
		expect(host.querySelector('[aria-label="待核对区域 1"]')?.getAttribute("aria-pressed")).toBe("false");
	});

  it("未知候选可以按完整选择题大区核对，不必先关联成某一道题", async () => {
    const candidate = { ...region, id: "unknown-choice", question_id: undefined, semantic_check: "needs_review" as const };
    const openGrid = vi.fn();
    mocks.editor.objectiveGrid = { openGrid, choiceQuestions: [], questionIds: [], groups: [], assignments: {}, optionCount: 4, open: false } as never;
    mocks.editor.unassignedRegions = new Map([[1, [candidate]]]);
    await render();
    await act(async () => host.querySelector<HTMLElement>(".template-unassigned-region")!.click());
    await act(async () => button("按选择题核对").click());
    expect(openGrid).toHaveBeenCalledWith(candidate);
    expect(mocks.editor.assignSuggestedRegion).not.toHaveBeenCalled();
  });

  it("教师修改后的区域直接可用，不再显示逐框或本页确认入口", async () => {
    const edited = { ...region, origin: "model" as const, review_state: "edited" as const };
    mocks.editor.currentPage = { ...template.layout.pages[0], question_regions: [edited] };
    mocks.editor.selectedRegion = edited;
    mocks.editor.selectedRegionId = edited.id;
    await render();
    expect(button("确认本页异常区域已处理")).toBeUndefined();
    expect(mocks.editor.confirmPageRegions).not.toHaveBeenCalled();
    await openSettingsDialog();
    expect(button("确认此异常区域可用")).toBeUndefined();
    expect(mocks.editor.confirmRegion).not.toHaveBeenCalled();
  });

  it("模板库定位标记直接显示位置，无需再次确认每个标记", async () => {
    const mark = { ...region, id: "registration", question_id: undefined, origin: "library" as const,
      review_state: "suggested" as const, semantic_check: "not_evaluated" as const };
    mocks.editor.currentPage = { ...template.layout.pages[0], registration_marks: [mark], question_regions: [] };
    await render();
    expect(host.textContent).toContain("本页 1 个定位标记");
    const outline = host.querySelector<HTMLElement>('[aria-label="定位标记 1"]')!;
    expect(outline.style.left).toBe(`${mark.x * 100}%`);
    expect(outline.style.height).toBe(`${mark.height * 100}%`);
    expect(button("确认本页异常区域已处理")).toBeUndefined();
    expect(mocks.editor.confirmPageRegions).not.toHaveBeenCalled();
  });

  it("识别存在实质冲突时仍提供纠错后的明确保留入口", async () => {
    const conflict = { ...region, origin: "model" as const, review_state: "suggested" as const, semantic_check: "needs_review" as const };
    mocks.editor.currentPage = { ...template.layout.pages[0], question_regions: [conflict] };
    mocks.editor.selectedRegion = conflict;
    mocks.editor.selectedRegionId = conflict.id;
    await render();
    await act(async () => button("确认本页异常区域已处理").click());
    expect(mocks.editor.confirmPageRegions).toHaveBeenCalledOnce();
    await openSettingsDialog();
    await act(async () => button("确认此异常区域可用").click());
    expect(mocks.editor.confirmRegion).toHaveBeenCalledWith(conflict.id);
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

