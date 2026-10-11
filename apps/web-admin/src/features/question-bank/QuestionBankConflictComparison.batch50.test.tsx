// @vitest-environment jsdom
import { cleanup, render, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { QuestionBankVersion } from "@edugrade/sdk";
import { QuestionBankConflictComparison } from "./QuestionBankConflictComparison";

function version(): QuestionBankVersion {
  return {
    id: "version", revision: 2, stem: "题干", default_score: 5, question_type: "short_answer",
    options: [], knowledge_points: [],
    scoring: { answer: { standard_answer: "答案", equivalent_answers: [] }, assets: [],
      template_version_id: "template-current", use_policy: "practice_only" }
  } as unknown as QuestionBankVersion;
}

function comparison(equivalent: unknown[], template?: unknown) {
  const latest = version();
  return <QuestionBankConflictComparison original={latest} latest={latest} localContent={{}}
    scoringDraft={{ fields: { equivalent, ...(template === undefined ? {} : { template }) } }} />;
}

function localList(container: HTMLElement) {
  const area = container.querySelector<HTMLElement>('[aria-label="本地等价答案"]')!;
  return within(area).getByRole("list");
}

describe("题库冲突比较的内容和列表身份", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it.each(["id", "point_id", "rubric_point_id"])("%s 保持重排和内容更新后的展开状态", idField => {
    const first = { [idField]: "first", description: "条件甲", legacy_note: "甲的历史内容" };
    const second = { [idField]: "second", description: "条件乙", legacy_note: "乙的历史内容" };
    const view = render(comparison([first, second]));
    const originalItem = within(localList(view.container)).getByText("条件甲").closest("li")!;
    originalItem.querySelector("details")!.open = true;

    // 内容可改而业务 ID 不变；展开状态应跟随同一项，不能随数组位置转给条件乙。
    view.rerender(comparison([second, { ...first, description: "条件甲更新" }]));
    const items = within(localList(view.container)).getAllByRole("listitem");
    expect(items[1]).toBe(originalItem);
    expect(items[1].textContent).toContain("条件甲更新");
    expect(items[1].querySelector("details")!.open).toBe(true);
    expect(items[0].querySelector("details")!.open).toBe(false);
  });

  it("无 ID 的历史内容按规范内容和重复次数区分，属性顺序不影响身份", () => {
    const errors = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const first = { description: "重复条件", legacy_note: "保留的历史说明" };
    const other = { description: "另一条件", legacy_note: "其他说明" };
    const view = render(comparison([first, { ...first }, other, "重复答案", "重复答案"]));
    const before = within(localList(view.container)).getAllByRole("listitem");
    before[0].querySelector("details")!.open = true;
    view.rerender(comparison([other, { legacy_note: first.legacy_note, description: first.description }, { ...first }, "重复答案", "重复答案"]));
    const after = within(localList(view.container)).getAllByRole("listitem");
    expect(after[0]).toBe(before[2]);
    expect(after[1]).toBe(before[0]);
    expect(after[2]).toBe(before[1]);
    expect(after[1].querySelector("details")!.open).toBe(true);
    expect(after[2].querySelector("details")!.open).toBe(false);
    expect(after[3]).not.toBe(after[4]);
    expect(errors.mock.calls.flat().join(" ")).not.toMatch(/same key|unique.*key/i);
  });

  it.each([
    { label: "对象", value: { id: "old-template", revision: 3, legacy: { note: "<img src=x>", enabled: false } } },
    { label: "数组", value: [{ id: "template-a", revision: 0 }, "template-b"] }
  ])("完整展示历史模板$label，不使用对象默认字符串或注入 HTML", ({ value }) => {
    const view = render(comparison([], value));
    const local = view.container.querySelector<HTMLElement>('[aria-label="本地引用评分模板"]')!;
    const latest = view.container.querySelector<HTMLElement>('[aria-label="最新引用评分模板"]')!;
    expect(local.querySelector("code")!.textContent).toBe(JSON.stringify(value, null, 2));
    expect(local.textContent).not.toContain("[object Object]");
    expect(local.querySelector("img")).toBeNull();
    expect(latest.querySelector("code")!.textContent).toBe("template-current");
  });

  it("模板字符串和显式清空保持原显示语义", () => {
    const view = render(comparison([], "template-local"));
    const area = () => view.container.querySelector<HTMLElement>('[aria-label="本地引用评分模板"]')!;
    expect(area().querySelector("code")!.textContent).toBe("template-local");
    view.rerender(comparison([], null));
    expect(area().textContent).toContain("未选用评分模板");
    expect(area().querySelector("code")).toBeNull();
  });
});
