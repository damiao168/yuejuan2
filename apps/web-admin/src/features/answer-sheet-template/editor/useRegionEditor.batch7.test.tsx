// @vitest-environment jsdom
import { act, useReducer, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TemplateLayout } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import { templateLayoutReducer } from "./templateLayoutReducer";
import { useRegionEditor } from "./useRegionEditor";

const mocks = vi.hoisted(() => ({ warning: vi.fn() }));
vi.mock("antd", () => ({
	App: { useApp: () => ({ message: { warning: mocks.warning } }) }
}));
const layout: TemplateLayout = {
	pages: [
		{
			page_no: 1,
			width: 100,
			height: 200,
			registration_marks: [],
			identity_regions: [],
			question_regions: [
				{
					id: "region",
					question_id: "question",
					x: 0.1,
					y: 0.2,
					width: 0.3,
					height: 0.4
				}
			]
		}
	]
};
const questions = [{ id: "question", question_no: "1" }] as Question[];

describe("region editor option and pointer boundaries", () => {
	let root: Root,
		host: HTMLDivElement,
		current: ReturnType<typeof useRegionEditor>,
		currentLayout: TemplateLayout;
	function Probe({ readonly = false }: Readonly<{ readonly?: boolean }>) {
		const [draft, dispatchLayout] = useReducer(templateLayoutReducer, layout);
		const [pageNo, setPageNo] = useState(1);
		currentLayout = draft;
		current = useRegionEditor({
			layout: draft,
			dispatchLayout,
			questions,
			pageNo,
			setPageNo,
			readonly
		});
		return <div ref={current.canvasRef} />;
	}
	beforeEach(() => {
		vi.clearAllMocks();
		Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
		host = document.createElement("div");
		document.body.append(host);
		root = createRoot(host);
	});
	afterEach(async () => {
		await act(async () => root.unmount());
		host.remove();
	});

	it("adds A through L using the existing twelve-option limit", async () => {
		await act(async () => root.render(<Probe />));
		await act(async () => current.setSelectedRegionId("region"));
		for (let index = 0; index < 12; index++)
			await act(async () => current.addOptionRegion());
		expect(
			currentLayout.pages[0].question_regions[0].option_regions?.map(
				(option) => option.label
			)
		).toEqual(["A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L"]);
		await act(async () => current.addOptionRegion());
		expect(
			currentLayout.pages[0].question_regions[0].option_regions
		).toHaveLength(12);
		expect(mocks.warning).toHaveBeenCalledWith("每题最多配置 12 个选项区域");
	});

	it("drops an in-progress drawing when editing becomes read-only", async () => {
		await act(async () => root.render(<Probe />));
		const canvas = current.canvasRef.current!;
		canvas.setPointerCapture = vi.fn();
		canvas.getBoundingClientRect = () =>
			({ left: 10, top: 20, width: 100, height: 200 }) as DOMRect;
		const event = (clientX: number, clientY: number) =>
			({
				clientX,
				clientY,
				currentTarget: canvas,
				pointerId: 1
			}) as import("react").PointerEvent<HTMLDivElement>;
		await act(async () => current.onCanvasPointerDown(event(30, 60)));
		await act(async () => current.onCanvasPointerMove(event(80, 160)));
		expect(current.draftRect).toMatchObject({ x: 0.2, y: 0.2 });
		expect(current.draftRect?.width).toBeCloseTo(0.5);
		await act(async () => root.render(<Probe readonly />));
		await act(async () => current.onCanvasPointerUp());
		expect(current.draftRect).toBeUndefined();
		expect(currentLayout.pages[0].question_regions).toHaveLength(1);
		expect(currentLayout.pages[0].question_regions[0].id).toBe("region");
	});
});
