// @vitest-environment jsdom
import { useState, type ComponentProps, type ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { RubricPayload } from "../../api/papers";
import { RubricReviewEditor } from "./RubricReviewEditor";

vi.mock("antd", () => ({
  Button: ({ children, onClick, "aria-label": label }: ComponentProps<"button">) => <button aria-label={label} onClick={onClick}>{children}</button>,
  Input: () => null, InputNumber: () => null, Select: () => null, Switch: () => null,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>
}));
vi.mock("../../components/StructuredTeacherFields", () => ({ createScoringExample: () => ({}), StructuredListEditor: () => null }));
afterEach(cleanup);

it("识别资料删掉中间采分点再新增时不复用剩余证据的标识", () => {
  let saved: RubricPayload | undefined;
  function Harness() {
    const [value, setValue] = useState<RubricPayload | undefined>({ status: "draft", max_score: 6,
      points: [1, 2, 3].map(id => ({ id: `human-review-p${id}`, description: `步骤${id}`, score: 2, required: true })), deductions: [], examples: [] });
    saved = value;
    return <RubricReviewEditor value={value} questionScore={6} onChange={setValue} />;
  }
  render(<Harness />);
  fireEvent.click(screen.getAllByRole("button", { name: "删除采分点" })[1]);
  fireEvent.click(screen.getByRole("button", { name: "添加采分点" }));
  fireEvent.click(screen.getByRole("button", { name: "添加采分点" }));
  const ids = saved!.points.map(point => point.id);
  expect(ids.slice(0, 2)).toEqual(["human-review-p1", "human-review-p3"]);
  expect(new Set(ids).size).toBe(4);
});
