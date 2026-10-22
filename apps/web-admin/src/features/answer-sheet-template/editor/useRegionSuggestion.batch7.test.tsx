// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../../../api/papers";
import type { TemplateLayoutAction } from "./templateLayoutReducer";
import { useRegionSuggestion } from "./useRegionSuggestion";

const mocks = vi.hoisted(() => ({
	message: { warning: vi.fn(), success: vi.fn(), error: vi.fn() },
	dispatch: vi.fn()
}));
vi.mock("antd", () => ({
	App: { useApp: () => ({ message: mocks.message }) }
}));
type PDFPage = Awaited<ReturnType<PDFDocumentProxy["getPage"]>>;
function page(items: unknown[], height = 200): PDFPage {
	return {
		getViewport: () => ({ height }),
		getTextContent: async () => ({ items })
	} as unknown as PDFPage;
}
function pdf(pages: PDFPage[]): PDFDocumentProxy {
	return {
		numPages: pages.length,
		getPage: async (number: number) => pages[number - 1]
	} as PDFDocumentProxy;
}
const text = (str: string, y: number, font = 20) => ({
	str,
	transform: [font, 0, 0, font, 0, y]
});
const question = (id: string, number: string) =>
	({ id, question_no: number }) as Question;

describe("PDF region suggestion boundaries against the original hook", () => {
	let root: Root,
		host: HTMLDivElement,
		current: ReturnType<typeof useRegionSuggestion>;
	beforeEach(() => {
		vi.resetAllMocks();
		Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
		host = document.createElement("div");
		document.body.append(host);
		root = createRoot(host);
	});
	afterEach(async () => {
		await act(async () => root.unmount());
		host.remove();
	});
	async function mount(
		document: PDFDocumentProxy | undefined,
		questions: Question[],
		readonly = false
	) {
		function Probe() {
			current = useRegionSuggestion({
				pdfRef: { current: document },
				questions,
				readonly,
				dispatchLayout: mocks.dispatch
			});
			return null;
		}
		await act(async () => root.render(<Probe />));
	}
	function regions() {
		return (
			mocks.dispatch.mock.calls[0][0] as Extract<
				TemplateLayoutAction,
				{ type: "suggestRegions" }
			>
		).pages;
	}

	it("matches longer question numbers first and converts bottom-origin coordinates before sorting", async () => {
		const questions = [
			question("short", "1"),
			question("long", "1.1"),
			question("later", "2"),
			question("missing", "3")
		];
		await mount(
			pdf([
				page([
					text("1.1.answer", 100),
					text("2、 answer", 40),
					text("1． answer", 160),
					{ str: "3" },
					{ transform: [] }
				])
			]),
			questions
		);
		await act(async () => current.suggestQuestionRegions());
		expect(regions().get(1)).toMatchObject([
			{
				question_id: "short",
				x: 0.04,
				y: 0.09,
				width: 0.92,
				height: 0.298,
				suggestion_confidence: 0.72,
				suggestion_source: "pdf_text_anchor"
			},
			{ question_id: "long", y: 0.39, height: 0.298 },
			{ question_id: "later", y: 0.69, height: 0.258 }
		]);
		expect(mocks.message.success).toHaveBeenCalledWith(
      "已自动对应 3 道题；发现错误可直接修改，另有 1 道题未识别"
		);
		expect(current.suggestingRegions).toBe(false);
	});

	it("accepts only the first matching anchor and clamps page and region bounds", async () => {
		await mount(
			pdf([
				page([text("1", 500), text("1", 20), text("2", -200)]),
				page([text("1", 100)])
			]),
			[question("a", "1"), question("b", "2")]
		);
		await act(async () => current.suggestQuestionRegions());
		expect(regions().get(1)).toMatchObject([
			{ question_id: "a", y: 0.02, height: 0.42 },
			{ question_id: "b", y: 0.94, height: 0.04 }
		]);
		expect(regions().has(2)).toBe(false);
		expect(mocks.message.success).toHaveBeenCalledWith(
      "已自动对应 2 道题；发现错误可直接修改"
		);
	});

	it.each(["no_pdf", "readonly", "no_questions"] as const)(
		"does not dispatch when %s",
		async (boundary) => {
			await mount(
				boundary === "no_pdf" ? undefined : pdf([page([text("1", 100)])]),
				boundary === "no_questions" ? [] : [question("a", "1")],
				boundary === "readonly"
			);
			await act(async () => current.suggestQuestionRegions());
			expect(mocks.dispatch).not.toHaveBeenCalled();
			expect(mocks.message.warning).toHaveBeenCalledWith(
				"当前文件没有可分析的 PDF 页面，或模板已锁定"
			);
		}
	);

	it("keeps the layout untouched when the PDF has no matching text anchor", async () => {
		await mount(pdf([page([text("unrelated", 100)])]), [question("a", "1")]);
		await act(async () => current.suggestQuestionRegions());
		expect(mocks.dispatch).not.toHaveBeenCalled();
		expect(mocks.message.warning).toHaveBeenCalledWith(
			"没有找到与本场题目相符的题号。请在左侧选择题目，再在答题卡上拖拽框选区域。"
		);
		expect(current.suggestingRegions).toBe(false);
	});

	it("does not apply a partial layout if reading a later page fails", async () => {
		const document = pdf([page([text("1", 100)]), page([])]);
		document.getPage = vi
			.fn()
			.mockResolvedValueOnce(page([text("1", 100)]))
			.mockRejectedValueOnce(new Error("page unreadable"));
		await mount(document, [question("a", "1")]);
		await act(async () => current.suggestQuestionRegions());
		expect(mocks.dispatch).not.toHaveBeenCalled();
		expect(mocks.message.error).toHaveBeenCalledTimes(1);
		expect(current.suggestingRegions).toBe(false);
	});
});
