import { describe, expect, it } from "vitest";
import type { Question } from "../../../api/papers";
import { matchAnswerSheetRegions, type SheetFrame, type SheetWord } from "./answerSheetRegionMatching";

const question = (number: string): Question => ({ id: `q${number}`, question_no: number } as Question);
const frame = (patch: Partial<SheetFrame> = {}): SheetFrame => ({ x: .1, y: .3, width: .8, height: .5, confidence: .94, ...patch });
const word = (text: string, patch: Partial<SheetWord> = {}): SheetWord => ({
  text, x: .12, y: .32, width: .035, height: .018, confidence: 95, ...patch
});
const choice = (number: string, question_type = "single_choice"): Question => ({ ...question(number), question_type });
const choiceWords = (number: string, x: number, y: number, lineId: string): SheetWord[] => [
  word(number, { x, y, lineId, source: "ocr" }),
  word("[A]", { x: x + .07, y, lineId, source: "ocr" }),
  word("(B)", { x: x + .14, y, lineId, source: "ocr" }),
];

describe("answer sheet question-to-frame matching", () => {
  it("normalizes full-width parenthesized printed question markers", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [word("（ １５ ）")], [question("15")], used);
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({ question_id: "q15", x: .1, y: .3, width: .8, height: .5, suggestion_source: "ocr_layout" });
    expect(used).toEqual(new Set(["q15"]));
  });

  it("does not interpret a bare handwritten answer near the upper left as a question marker", () => {
    const used = new Set<string>();
    expect(matchAnswerSheetRegions([frame()], [word("1")], [question("1")], used)).toEqual([]);
    expect(used.size).toBe(0);
  });

  it.each(["single_choice", "multiple_choice"])("allows a bare %s question number with two distinct options on its own OCR line", questionType => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], choiceWords("1", .12, .32, "b0:p0:l0"), [choice("1", questionType)], used);
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({ question_id: "q1", x: .1, y: .3, width: .8, height: .5 });
    expect(used).toEqual(new Set(["q1"]));
  });

  it.each(["short_answer", "fill_blank", "true_false"])("does not use A/B text to treat a bare %s answer as a question number", questionType => {
    const used = new Set<string>();
    expect(matchAnswerSheetRegions([frame()], choiceWords("1", .12, .32, "line0"), [choice("1", questionType)], used)).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("does not borrow option letters from another OCR line even at similar vertical coordinates", () => {
    const used = new Set<string>();
    const words = choiceWords("1", .12, .32, "line0");
    words[1].lineId = "line1";
    words[2].lineId = "line1";
    expect(matchAnswerSheetRegions([frame()], words, [choice("1")], used)).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("requires two different option labels and does not count repeated A labels twice", () => {
    const used = new Set<string>();
    const words = choiceWords("1", .12, .32, "line0");
    words[2].text = "A";
    expect(matchAnswerSheetRegions([frame()], words, [choice("1")], used)).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("requires option evidence to be inside the same border and to the right of the number", () => {
    for (const optionPatch of [{ x: .02 }, { x: .88 }, { confidence: 54 }]) {
      const used = new Set<string>();
      const words = choiceWords("1", .12, .32, "line0");
      words[2] = { ...words[2], ...optionPatch };
      expect(matchAnswerSheetRegions([frame()], words, [choice("1")], used)).toEqual([]);
      expect(used.size).toBe(0);
    }
  });

  it("does not borrow the next question's options from the same OCR line", () => {
    const used = new Set<string>();
    const words = [word("1", { lineId: "line0", source: "ocr" }), ...choiceWords("2", .55, .32, "line0")];
    const result = matchAnswerSheetRegions([frame()], words, [choice("1"), choice("2")], used);
    expect(result.some(region => region.question_id === "q1")).toBe(false);
    expect(used.has("q1")).toBe(false);
  });

  it("keeps PDF text layer compatibility for a bare number without OCR option evidence", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [word("1", { source: "pdf_text" })], [question("1")], used);
    expect(result).toHaveLength(1);
    expect(result[0].question_id).toBe("q1");
  });

  it("splits a fully evidenced choice grid into nonoverlapping rows and columns", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [
      ...choiceWords("1", .12, .32, "line0"), ...choiceWords("2", .55, .32, "line0"),
      ...choiceWords("3", .12, .43, "line1"), ...choiceWords("4", .55, .43, "line1"),
    ], [choice("1"), choice("2"), choice("3"), choice("4")], used);
    expect(result.map(region => region.question_id)).toEqual(["q1", "q2", "q3", "q4"]);
    expect(result[0].y).toBe(.3);
    expect(result[0].x + result[0].width).toBeLessThanOrEqual(result[1].x);
    expect(result[0].y + result[0].height).toBeLessThanOrEqual(result[2].y);
    expect(result[1].y + result[1].height).toBeLessThanOrEqual(result[3].y);
    expect(result[2].x + result[2].width).toBeLessThanOrEqual(result[3].x);
    expect(result[3].y + result[3].height).toBeCloseTo(.8);
    expect(used).toEqual(new Set(["q1", "q2", "q3", "q4"]));
  });

  it.each([false, true])("coalesces a header retry and its full-page choice anchor while preserving both kinds of evidence (header first: %s)", headerFirst => {
    const used = new Set<string>();
    const words = [
      ...choiceWords("1", .12, .32, "line0"), ...choiceWords("2", .55, .32, "line0"),
      ...choiceWords("3", .12, .43, "line1"), ...choiceWords("4", .55, .43, "line1"),
    ];
    const header = word("1.", { x: .121, y: .321, width: .04, confidence: 80, source: "ocr", lineId: "header:0:0:0:0" });
    const result = matchAnswerSheetRegions([frame()], headerFirst ? [header, ...words] : [...words, header],
      [choice("1"), choice("2"), choice("3"), choice("4")], used);
    expect(result.map(region => region.question_id)).toEqual(["q1", "q2", "q3", "q4"]);
    expect(result[0].suggestion_confidence).toBe(.8);
    expect(result[0].y + result[0].height).toBeLessThanOrEqual(result[2].y);
    expect(used).toEqual(new Set(["q1", "q2", "q3", "q4"]));
  });

  it("does not merge same-number header retries from separate frames into one question assignment", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame({ width: .35 }), frame({ x: .55, width: .35 })], [
      ...choiceWords("1", .12, .32, "line0"), ...choiceWords("1", .57, .32, "line1"),
      word("1.", { x: .121, source: "ocr", lineId: "header:0:0:0:0" }),
      word("1.", { x: .571, source: "ocr", lineId: "header:1:0:0:0" }),
    ], [choice("1")], used);
    expect(result).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("does not coalesce same-number header markers at different positions within a frame", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [
      ...choiceWords("1", .12, .32, "line0"), ...choiceWords("1", .12, .43, "line1"),
      word("1.", { x: .121, y: .321, source: "ocr", lineId: "header:0:0:0:0" }),
      word("1.", { x: .121, y: .431, source: "ocr", lineId: "header:0:1:0:0" }),
    ], [choice("1")], used);
    expect(result).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("leaves an irregular choice grid unmatched instead of covering other rows with the first row", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [
      ...choiceWords("1", .12, .32, "line0"), ...choiceWords("2", .55, .32, "line0"),
      ...choiceWords("3", .12, .43, "line1"),
    ], [choice("1"), choice("2"), choice("3")], used);
    expect(result).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("does not let a recognized choice row cover another row with missing option evidence", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [
      ...choiceWords("1", .12, .32, "line0"), word("2", { y: .43, lineId: "line1", source: "ocr" }),
    ], [choice("1"), choice("2")], used);
    expect(result).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("does not let an explicitly retried header consume a neighboring choice row without option evidence", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [
      word("1", { lineId: "line0", source: "ocr" }), word("2", { y: .43, lineId: "line1", source: "ocr" }),
      word("1.", { x: .121, y: .321, lineId: "header:0:0:0:0", source: "ocr" }),
    ], [choice("1"), choice("2")], used);
    expect(result).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("lets a retried explicit header verify its own same-position bare number without requiring options", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [
      word("1", { lineId: "line0", source: "ocr" }),
      word("1.", { x: .121, y: .321, lineId: "header:0:0:0:0", source: "ocr" }),
    ], [choice("1")], used);
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({ question_id: "q1", x: .1, y: .3, width: .8, height: .5 });
    expect(used).toEqual(new Set(["q1"]));
  });

  it("does not let one explicit header verify another same-number bare marker at a different position", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [
      word("1", { lineId: "line0", source: "ocr" }), word("1", { y: .43, lineId: "line1", source: "ocr" }),
      word("1.", { x: .121, y: .321, lineId: "header:0:0:0:0", source: "ocr" }),
    ], [choice("1")], used);
    expect(result).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("does not let a retried choice header consume a bare neighbor on the same row", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame({ height: .18 })], [
      word("1", { lineId: "line0", source: "ocr" }), word("2", { x: .55, lineId: "line0", source: "ocr" }),
      word("1.", { x: .121, y: .321, lineId: "header:0:0:0:0", source: "ocr" }),
    ], [choice("1"), choice("2")], used);
    expect(result).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("does not reject a subjective parent frame because handwritten answer numbers match choice questions", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame()], [
      word("(15)"), word("(1)", { y: .39 }), word("(2)", { y: .46 }),
      word("3", { y: .53, lineId: "line3", source: "ocr" }),
    ], [question("15"), choice("1"), choice("2"), choice("3")], used);
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({ question_id: "q15", x: .1, y: .3, width: .8, height: .5 });
    expect(used).toEqual(new Set(["q15"]));
  });

  it("does not silently choose one repeated question marker when another question shares the printed header row", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame({ height: .18 })], [
      word("1."), word("1.", { x: .4 }), word("2.", { x: .7 }),
    ], [question("1"), question("2")], used);
    expect(result.some(region => region.question_id === "q1")).toBe(false);
    expect(used.has("q1")).toBe(false);
  });

  it("does not subdivide a short question frame using handwritten answer numbers", () => {
    const used = new Set<string>();
    // 第15题框内写了答案1、2；这些数字不能被套给考试中的第1、2题。
    const result = matchAnswerSheetRegions([frame({ height: .2 })], [
      word("(15)"), word("1", { x: .3, y: .38 }), word("2", { x: .45, y: .43 })
    ], [question("1"), question("2"), question("15")], used);
    expect(result.map(item => item.question_id)).toEqual(["q15"]);
    expect(result[0]).toMatchObject({ x: .1, y: .3, width: .8, height: .2 });
    expect(used).toEqual(new Set(["q15"]));
  });

  it("can subdivide a shared short frame from multiple explicit question markers", () => {
    const used = new Set<string>(["existing"]);
    const result = matchAnswerSheetRegions([frame({ height: .18 })], [
      word("1."), word("2.", { x: .55 })
    ], [question("1"), question("2")], used);
    expect(result.map(item => item.question_id)).toEqual(["q1", "q2"]);
    expect(result[0].x + result[0].width).toBeLessThanOrEqual(result[1].x);
    expect(result[0].width).toBeLessThan(.8);
    expect(result[0].y).toBe(.3);
    expect(result[1].y).toBe(.3);
    expect(result[0].height).toBeCloseTo(.18);
    expect(result[1].height).toBeCloseTo(.18);
    expect(used).toEqual(new Set(["existing", "q1", "q2"]));
  });

  it("does not split a parent question frame at parenthesized handwritten substeps", () => {
    const used = new Set<string>();
    // 已写答案中的(1)、(2)可能与考试小题同号；父题印刷题号应独占这个边框。
    const result = matchAnswerSheetRegions([frame({ height: .22 })], [
      word("(15)"), word("(1)", { y: .37 }), word("(2)", { y: .43 })
    ], [question("1"), question("2"), question("15")], used);
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({ question_id: "q15", x: .1, y: .3, width: .8 });
    expect(result[0].height).toBeCloseTo(.22);
    expect(used).toEqual(new Set(["q15"]));
  });

  it("does not call a second text line near the top border a shared printed header row", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame({ height: .22 })], [
      word("(15)"), word("(1)", { y: .35 }), word("(2)", { y: .40 })
    ], [question("1"), question("2"), question("15")], used);
    expect(result.map(item => item.question_id)).toEqual(["q15"]);
    expect(result[0].y).toBe(.3);
    expect(result[0].height).toBeCloseTo(.22);
    expect(used).toEqual(new Set(["q15"]));
  });

  it("keeps the complete short frame when it contains a single question marker", () => {
    const result = matchAnswerSheetRegions([frame({ height: .18 })], [word("1.")], [question("1")], new Set());
    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({ question_id: "q1", x: .1, y: .3, width: .8, height: .18 });
  });

  it("coalesces duplicate geometry without creating duplicate question coverage", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame(), frame()], [word("1.")], [question("1")], used);
    expect(result).toHaveLength(1);
    expect(used).toEqual(new Set(["q1"]));
  });

  it("does not silently prefer a large container frame over a differently sized frame at the same origin", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([frame(), frame({ width: .35, height: .32 })], [word("1.")], [question("1")], used);
    expect(result).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("leaves a question unmatched if its marker appears in separate frames on the same page", () => {
    const used = new Set<string>();
    const result = matchAnswerSheetRegions([
      frame({ width: .35 }), frame({ x: .55, width: .35 })
    ], [word("1."), word("1.", { x: .57 })], [question("1")], used);
    expect(result).toEqual([]);
    expect(used.size).toBe(0);
  });

  it("preserves existing coverage and rejects low-confidence, outside and unknown markers", () => {
    const used = new Set<string>(["q1"]);
    const result = matchAnswerSheetRegions([frame()], [
      word("1."), word("2.", { confidence: 54 }), word("3.", { x: .02 }), word("9.")
    ], [question("1"), question("2"), question("3")], used);
    expect(result).toEqual([]);
    expect(used).toEqual(new Set(["q1"]));
  });

  it("does not create regions or consume question IDs without printed borders", () => {
    const used = new Set<string>();
    expect(matchAnswerSheetRegions([], [word("1.")], [question("1")], used)).toEqual([]);
    expect(used.size).toBe(0);
  });
});
