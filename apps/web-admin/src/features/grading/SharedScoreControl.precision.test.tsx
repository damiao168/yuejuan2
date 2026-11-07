// @vitest-environment jsdom
import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { SharedScoreControl, type SharedScoreValue } from "./SharedScoreControl";

beforeEach(() => {
  window.matchMedia = vi.fn().mockReturnValue({ matches: false, addListener: vi.fn(), removeListener: vi.fn() });
});

function Scoring() {
  const [value, setValue] = useState<SharedScoreValue>({ score: null, rubricSelections: {} });
  return <><SharedScoreControl value={value} onChange={setValue} maxScore={2.25}
    rubricPoints={[{ id: "p1", description: "过程", score: 1.25, required: true }, { id: "p2", description: "结果", score: 1, required: true }]} />
    <output aria-label="提交分值">{JSON.stringify(value)}</output></>;
}

it("真实录分控件失焦保留 2.25 满分，采分点 1.25 + 1 合计也为 2.25", () => {
  render(<Scoring />);
  const final = screen.getByRole("spinbutton", { name: "最终得分" });
  fireEvent.change(final, { target: { value: "2.25" } });
  fireEvent.blur(final);
  expect((final as HTMLInputElement).value).toBe("2.25");
  expect(JSON.parse(screen.getByLabelText("提交分值").textContent!).score).toBe(2.25);
  const process = screen.getByRole("spinbutton", { name: "过程得分" });
  fireEvent.change(process, { target: { value: "1.25" } });
  fireEvent.blur(process);
  expect((process as HTMLInputElement).value).toBe("1.25");
  fireEvent.click(screen.getAllByRole("checkbox")[1]);
  const submitted = JSON.parse(screen.getByLabelText("提交分值").textContent!);
  expect(submitted).toEqual({ score: 2.25, rubricSelections: { p1: 1.25, p2: 1 } });
  fireEvent.change(final, { target: { value: "2.2" } });
  fireEvent.blur(final);
  expect(screen.getByText("人工调整：与细则合计相差 -0.05 分")).toBeTruthy();
});
