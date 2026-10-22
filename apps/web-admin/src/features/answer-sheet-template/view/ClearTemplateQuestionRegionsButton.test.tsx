// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode, type SetStateAction } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TemplateLayout } from "../../../api/configuration";
import { answerRegion, answerTemplate } from "../testing/answerSheetFixtures";
import { ClearTemplateQuestionRegionsButton } from "./ClearTemplateQuestionRegionsButton";

const mocks = vi.hoisted(() => ({ confirm: vi.fn() }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) },
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) =>
    <button type="button" disabled={disabled} onClick={onClick}>{children}</button>
}));

type Props = ComponentProps<typeof ClearTemplateQuestionRegionsButton>;
type Confirmation = { content: string; onOk: () => void; onCancel: () => void };

describe("clear old answer-sheet question regions", () => {
  let root: Root;
  let host: HTMLDivElement;
  let props: Props;
  let layout: TemplateLayout;
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    layout = {
      omr_profile: { mode: "manual_only", version: "opencv-fill-v1" },
      pages: [1, 2].map(pageNo => ({ ...answerTemplate.layout.pages[0], page_no: pageNo,
        registration_marks: [{ ...answerRegion, id: `mark-${pageNo}` }],
        identity_regions: [{ ...answerRegion, id: `identity-${pageNo}` }],
        question_regions: [{ ...answerRegion, id: `answer-${pageNo}` }] }))
    };
    props = {
      scopeKey: "exam:source:template:1", editable: true, busy: false, layout,
      unassignedRegions: new Map([[1, [{ ...answerRegion, id: "candidate-1" }]], [2, [{ ...answerRegion, id: "candidate-2" }]]]),
      setLayout: vi.fn((value: SetStateAction<TemplateLayout>) => { layout = typeof value === "function" ? value(layout) : value; }),
      discardSuggestedRegion: vi.fn(), onCleared: vi.fn()
    };
    host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  async function render(patch: Partial<Props> = {}) {
    props = { ...props, ...patch };
    await act(async () => root.render(<ClearTemplateQuestionRegionsButton {...props} />));
  }
  async function open() {
    await act(async () => host.querySelector("button")?.click());
    return mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1]?.[0] as Confirmation;
  }
  const expectUntouched = () => {
    expect(props.setLayout).not.toHaveBeenCalled();
    expect(props.discardSuggestedRegion).not.toHaveBeenCalled();
    expect(props.onCleared).not.toHaveBeenCalled();
  };

  it("clears all page question regions and candidates only after confirmation, preserving page metadata", async () => {
    const original = layout;
    await render(); const confirmation = await open();
    expectUntouched();
    expect(confirmation.content).toContain("所有页面");
    expect(confirmation.content).toContain("不修改后台已保存版本");
    await act(async () => confirmation.onOk());
    expect(layout).toEqual({ ...original, pages: original.pages.map(page => ({ ...page, question_regions: [] })) });
    for (let index = 0; index < original.pages.length; index++) {
      expect(layout.pages[index].registration_marks).toBe(original.pages[index].registration_marks);
      expect(layout.pages[index].identity_regions).toBe(original.pages[index].identity_regions);
    }
    expect(props.discardSuggestedRegion).toHaveBeenNthCalledWith(1, "candidate-1");
    expect(props.discardSuggestedRegion).toHaveBeenNthCalledWith(2, "candidate-2");
    // 弹窗同一次确认只能生效一次，不能再次清掉其后新增的区域。
    await act(async () => confirmation.onOk());
    expect(props.setLayout).toHaveBeenCalledOnce();
    expect(props.discardSuggestedRegion).toHaveBeenCalledTimes(2);
    expect(props.onCleared).toHaveBeenCalledOnce();
  });

  it.each(["source", "template", "revision", "permission", "busy"])("invalidates old confirmation across a %s boundary even after switching back", async boundary => {
    await render(); const confirmation = await open();
    const original = props;
    const patch = boundary === "permission" ? { editable: false } : boundary === "busy" ? { busy: true }
      : { scopeKey: `exam:${boundary}:changed` };
    await render(patch); await render(original);
    await act(async () => confirmation.onOk());
    expectUntouched();
  });

  it("does not clear after cancellation", async () => {
    await render(); const confirmation = await open();
    await act(async () => { confirmation.onCancel(); confirmation.onOk(); });
    expectUntouched();
  });

  it("does not clear after unmounting", async () => {
    await render(); const confirmation = await open();
    await act(async () => root.render(null));
    await act(async () => confirmation.onOk());
    expectUntouched();
  });

  it("keeps a deferred state update from clearing a different draft", async () => {
    const setLayout = vi.fn(); await render({ setLayout }); const confirmation = await open();
    await act(async () => confirmation.onOk());
    const update = setLayout.mock.calls[0][0] as (current: TemplateLayout) => TemplateLayout;
    await render({ scopeKey: "exam:other-source:other-template:1" });
    // React 的函数更新可能晚于确认回调执行，此时也必须重新核对草稿边界。
    expect(update(layout)).toBe(layout);
  });

  it.each(["readonly", "busy", "empty"])("disables clearing when %s", async state => {
    const patch = state === "readonly" ? { editable: false } : state === "busy" ? { busy: true }
      : { layout: { pages: [] }, unassignedRegions: new Map() };
    await render(patch);
    expect(host.querySelector("button")?.disabled).toBe(true);
    await open();
    expect(mocks.confirm).not.toHaveBeenCalled();
    expectUntouched();
  });

  it("can clear candidates when no question region is assigned", async () => {
    await render({ layout: { pages: [] } });
    expect(host.querySelector("button")?.disabled).toBe(false);
    const confirmation = await open(); await act(async () => confirmation.onOk());
    expect(props.discardSuggestedRegion).toHaveBeenCalledTimes(2);
  });

  it("clears manual areas without any per-question ROI and resets remembered shared areas", async () => {
    layout = { pages: [{ ...layout.pages[0], question_regions: [], grading_groups: [
      { group_id: "manual-fill", question_ids: ["q12", "q13", "q14"], bbox: { x: .1, y: .2, width: .7, height: .2 } }
    ] }] };
    await render({ layout, unassignedRegions: new Map() });
    expect(host.querySelector("button")?.disabled).toBe(false);
    const confirmation = await open(); await act(async () => confirmation.onOk());
    expect(layout.pages[0].grading_groups).toEqual([]);
    expect(layout.pages[0].question_regions).toEqual([]);
    expect(props.onCleared).toHaveBeenCalledOnce();
  });
});
