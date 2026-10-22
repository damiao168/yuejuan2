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

  it("does not reuse an occupied option label after removing a middle option", async () => {
    await act(async () => root.render(<Probe />)); await act(async () => current.setSelectedRegionId("region"));
    for (let index = 0; index < 3; index++) await act(async () => current.addOptionRegion());
    const original = currentLayout.pages[0].question_regions[0].option_regions!;
    await act(async () => current.removeOptionRegion(original[1].id)); await act(async () => current.addOptionRegion());
    const options = currentLayout.pages[0].question_regions[0].option_regions!;
    expect(options.map(option => option.label)).toEqual(["A", "C", "B"]);
    expect(options[0].id).toBe(original[0].id); expect(options[1].id).toBe(original[2].id);
    expect(new Set(options.map(option => option.label.toUpperCase())).size).toBe(3);
  });

  it("resizes from the upper-left corner without moving the opposite edges outside the page", async () => {
    await act(async () => root.render(<Probe />));
    const canvas = current.canvasRef.current!;
    canvas.setPointerCapture = vi.fn();
    canvas.getBoundingClientRect = () => ({ left: 0, top: 0, width: 100, height: 200 }) as DOMRect;
    const event = (clientX: number, clientY: number) => ({ clientX, clientY, currentTarget: canvas, pointerId: 1,
      stopPropagation: vi.fn(), preventDefault: vi.fn() }) as unknown as import("react").PointerEvent<HTMLDivElement>;
    await act(async () => current.beginRegionInteraction(event(10, 40), currentLayout.pages[0].question_regions[0], "resize", "nw"));
    await act(async () => current.onCanvasPointerMove(event(-100, -100)));
    expect(currentLayout.pages[0].question_regions[0]).toMatchObject({ x: 0, y: 0, width: .4 });
    expect(currentLayout.pages[0].question_regions[0].height).toBeCloseTo(.6);
    await act(async () => current.onCanvasPointerMove(event(100, 200)));
    expect(currentLayout.pages[0].question_regions[0].width).toBeCloseTo(.01);
    expect(currentLayout.pages[0].question_regions[0].height).toBeCloseTo(.01);
  });

  it("restores the original region when a live move is canceled and never commits a canceled drawing", async () => {
    await act(async () => root.render(<Probe />));
    const canvas = current.canvasRef.current!;
    canvas.setPointerCapture = vi.fn();
    canvas.getBoundingClientRect = () => ({ left: 0, top: 0, width: 100, height: 200 }) as DOMRect;
    const event = (clientX: number, clientY: number) => ({ clientX, clientY, currentTarget: canvas, pointerId: 1,
      stopPropagation: vi.fn(), preventDefault: vi.fn() }) as unknown as import("react").PointerEvent<HTMLDivElement>;
    await act(async () => current.beginRegionInteraction(event(10, 40), currentLayout.pages[0].question_regions[0], "move"));
    await act(async () => current.onCanvasPointerMove(event(40, 100)));
    expect(currentLayout.pages[0].question_regions[0].x).toBeCloseTo(.4);
    await act(async () => current.cancelInteraction());
    expect(currentLayout.pages[0].question_regions[0]).toMatchObject(layout.pages[0].question_regions[0]);
    await act(async () => current.onCanvasPointerDown(event(10, 20)));
    await act(async () => current.onCanvasPointerMove(event(60, 100)));
    await act(async () => current.cancelInteraction());
    await act(async () => current.onCanvasPointerUp());
    expect(currentLayout.pages[0].question_regions).toHaveLength(1);
    expect(currentLayout.pages[0].question_regions[0].id).toBe("region");
  });

  it("creates and selects an identity field with movable bounded geometry and no question coverage", async () => {
    await act(async () => root.render(<Probe />));
    await act(async () => current.createIdentityRegion("学号"));
    const identity = currentLayout.pages[0].identity_regions[0];
    expect(current.selectedRegionKind).toBe("identity");
    expect(current.selectedRegion?.id).toBe(identity.id);
    expect(current.selectedQuestion).toBeUndefined();
    expect(current.coveredQuestions).toEqual(new Set(["question"]));
    await act(async () => current.addOptionRegion());
    expect(currentLayout.pages[0].identity_regions[0].option_regions).toBeUndefined();
    const canvas = current.canvasRef.current!;
    canvas.setPointerCapture = vi.fn();
    canvas.getBoundingClientRect = () => ({ left: 0, top: 0, width: 100, height: 200 }) as DOMRect;
    const event = (clientX: number, clientY: number) => ({ clientX, clientY, currentTarget: canvas, pointerId: 1,
      stopPropagation: vi.fn(), preventDefault: vi.fn() }) as unknown as import("react").PointerEvent<HTMLDivElement>;
    await act(async () => current.beginRegionInteraction(event(10, 10), identity, "move"));
    await act(async () => current.onCanvasPointerMove(event(200, 400)));
    expect(currentLayout.pages[0].identity_regions[0].x).toBeCloseTo(.7);
    expect(currentLayout.pages[0].identity_regions[0].y).toBeCloseTo(.92);
    await act(async () => current.cancelInteraction());
    expect(currentLayout.pages[0].identity_regions[0]).toEqual(identity);
    await act(async () => current.setSelectedRegionId(identity.id));
    await act(async () => current.updateRegion(identity.id, { label: "准考证号", width: .4 }));
    expect(current.selectedRegion).toMatchObject({ label: "准考证号", width: .4 });
    await act(async () => current.removeRegion(identity.id));
    expect(currentLayout.pages[0].identity_regions).toEqual([]);
    expect(currentLayout.pages[0].question_regions).toHaveLength(1);
  });

  it("does not create identity fields when the template is read-only", async () => {
    await act(async () => root.render(<Probe readonly />));
    await act(async () => current.createIdentityRegion("姓名"));
    expect(currentLayout.pages[0].identity_regions).toEqual([]);
  });
});
