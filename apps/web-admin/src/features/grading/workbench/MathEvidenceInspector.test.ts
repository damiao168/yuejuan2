// @vitest-environment jsdom
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MathUnderstandingResponse } from "../../../api/mathUnderstanding";
import { isFormulaEvidenceSubject, MathEvidenceInspector, type MathEvidenceInspectorProps } from "./MathEvidenceInspector";
import { selectEffectiveMathArtifact } from "./mathEffectiveEvidence";

describe("math evidence subject boundary", () => {
  it("enables formula evidence only for calibrated quantitative subjects", () => {
    expect(isFormulaEvidenceSubject("mathematics")).toBe(true);
    expect(isFormulaEvidenceSubject("physics")).toBe(true);
    expect(isFormulaEvidenceSubject("chemistry")).toBe(true);
  });

  it.each(["chinese", "history", "ethics_politics", "geography", "english", "biology"])(
    "does not call formula evidence for %s",
    (subject) => expect(isFormulaEvidenceSubject(subject)).toBe(false)
  );
});

describe("math evidence correction projection", () => {
  it("uses effective evidence while retaining the immutable artifact response", () => {
    const response = {
      artifact: { formulas: [{ id: "formula-1", canonical_latex: "x=2" }] },
      effective_artifact: { formulas: [{ id: "formula-1", canonical_latex: "x=3" }] },
      correction_revision: 1,
      corrected: true,
      corrections: []
    } as unknown as MathUnderstandingResponse;

    expect(selectEffectiveMathArtifact(response).formulas).toEqual([
      { id: "formula-1", canonical_latex: "x=3" }
    ]);
    expect(response.artifact.formulas).toEqual([{ id: "formula-1", canonical_latex: "x=2" }]);
  });

  it("keeps older server responses usable without interpreting correction history", () => {
    const response = {
      artifact: { formulas: [{ id: "formula-1", canonical_latex: "x=2" }] },
      corrections: [{ revision: 1, corrected_contract: { formulas: [{ canonical_latex: "x=9" }] } }]
    } as unknown as MathUnderstandingResponse;

    expect(selectEffectiveMathArtifact(response)).toBe(response.artifact);
  });
});

describe("math evidence inspector details", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
  });

  function props(): MathEvidenceInspectorProps {
    const artifact = { id: "artifact-1", answer_segment_id: "segment-1", version: 2,
      blocks: [{ id: "block-1", status: "crossed_out", text: "x = 1" }, { id: "block-2", status: "active", text: "x = 2" }],
      formulas: [{ id: "formula-1", canonical_latex: "x=1", confidence: .7 }],
      solution_graph: { steps: [{ id: "step-1", block_ids: ["block-1", "block-2"], confidence: .7 }], edges: [] },
      rubric_evidence: [{ id: "e1", status: "supported" }, { id: "e2", status: "unsupported" }, { id: "e3", status: "uncertain" }] };
    return { segmentId: "segment-1", subjectCode: "physics", loading: false, onRefresh: vi.fn(), onCorrectionSaved: vi.fn(),
      onDirtyChange: vi.fn(), onSelectStep: vi.fn(), data: { artifact, corrected: true, correction_revision: 3,
        effective_artifact: { ...artifact, formulas: [{ id: "formula-1", canonical_latex: "x=2", confidence: .69 }] } } as unknown as MathUnderstandingResponse };
  }

  async function open(options: MathEvidenceInspectorProps) {
    await act(async () => root.render(createElement(MathEvidenceInspector, options)));
    await act(async () => host.querySelector<HTMLButtonElement>(".math-evidence-summary button")!.click());
  }

  it("keeps the confidence boundary, effective formula, evidence tones and step navigation", async () => {
    const options = props();
    await open(options);
    expect(host.querySelector(".math-evidence-summary")?.textContent).toContain("1 项识别需要确认");
    expect(host.querySelector(".math-evidence-summary button")?.textContent).toBe("收起详情");
    expect(host.querySelector(".math-evidence-meta")?.textContent).toContain("物理识别版本 2 · 校正 #3");
    expect(host.querySelector<HTMLInputElement>('[aria-label="校正公式 formula-1"]')?.value).toBe("x=2");
    expect(Array.from(host.querySelectorAll(".math-rubric-evidence .ant-tag"), (tag) => tag.className)).toEqual([
      expect.stringContaining("ant-tag-green"), expect.stringContaining("ant-tag-red"), expect.stringContaining("ant-tag-orange"),
    ]);
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="定位步骤 step-1"]')!.click());
    expect(options.onSelectStep).toHaveBeenCalledExactlyOnceWith("step-1");
    options.data!.effective_artifact!.formulas![0].confidence = .7;
    await act(async () => root.render(createElement(MathEvidenceInspector, { ...options, data: { ...options.data! } })));
    await act(async () => host.querySelector<HTMLButtonElement>(".math-evidence-summary button")!.click());
    expect(host.querySelector(".math-evidence-summary button")?.textContent).toBe("查看详情");
  });

  it("does not mount read-only correction controls while evidence remains navigable", async () => {
    await open({ ...props(), disabled: true });
    expect(host.querySelector<HTMLInputElement>('[aria-label="校正公式 formula-1"]')).toBeNull();
    expect(host.textContent).toContain("x=2");
    expect(host.querySelector<HTMLButtonElement>('[aria-label="定位步骤 step-1"]')?.disabled).toBe(false);
    expect(host.querySelectorAll(".math-step-row button:not([aria-label^='定位步骤']), .math-crossed-block button, .ant-space-compact button, .math-evidence-actions button")).toHaveLength(0);
  });
});
