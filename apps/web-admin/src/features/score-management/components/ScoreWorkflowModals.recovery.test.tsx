// @vitest-environment jsdom
import { act, type ComponentProps, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import { ScoreWorkflowModals } from "./ScoreWorkflowModals";
import { releaseVisibilityPreset } from "../hooks/useScoreReleaseWorkflow";

it("公开模板保留复核依赖，任何模板都不顺带公开匿名范例卷或具体排名", () => {
  expect(releaseVisibilityPreset("basic", false).show_question_scores).toBe(false);
  const appeals = releaseVisibilityPreset("basic", true);
  expect(appeals.show_question_scores).toBe(true); expect(appeals.show_answers).toBe(false);
  for (const preset of [appeals, releaseVisibilityPreset("question", true)]) {
    expect(preset.show_high_score_paper).toBe(false); expect(preset.show_exact_rank).toBe(false);
  }
});

vi.mock("antd", () => {
  const List = Object.assign(({ dataSource, renderItem }: { dataSource: unknown[]; renderItem: (value: unknown) => ReactNode }) => <div>{dataSource.map(renderItem)}</div>, {
    Item: Object.assign(({ actions, children }: { actions: ReactNode[]; children: ReactNode }) => <article>{children}{actions}</article>, {
      Meta: ({ title, description }: { title: ReactNode; description: ReactNode }) => <div>{title}{description}</div>
    })
  });
  const Input = Object.assign(() => null, { TextArea: (props: ComponentProps<"textarea">) => <textarea {...props} /> });
  return {
    List, Input,
    Modal: ({ open, children }: { open: boolean; children: ReactNode }) => open ? <section>{children}</section> : null,
    Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => <div>{message}{description}</div>,
    Button: ({ children, onClick, disabled }: ComponentProps<"button">) => <button type="button" onClick={onClick} disabled={disabled}>{children}</button>,
    Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
    InputNumber: () => null, Checkbox: ({ children, checked, disabled, onChange }: { children: ReactNode; checked: boolean; disabled: boolean; onChange: (event: { target: { checked: boolean } }) => void }) => <label><input type="checkbox" checked={checked} disabled={disabled} onChange={onChange} />{children}</label>, Select: () => null
  };
});

it("关闭逐题得分时禁用所有依赖内容，群体统计仍可单独选择", async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  type Props = ComponentProps<typeof ScoreWorkflowModals>;
  const host = document.createElement("div"); const root = createRoot(host);
  try {
    await act(async () => root.render(<ScoreWorkflowModals gradeTotal={0} publishedReleaseExists={false} actioning={null}
      attendanceWorkflow={{} as Props["attendanceWorkflow"]}
      regradeWorkflow={{ regradeReviewScores: {}, regradeReviewNotes: {} } as Props["regradeWorkflow"]}
      releaseWorkflow={{ releaseModalOpen: true, releaseVisibility: { show_question_scores: false, show_answers: true, show_feedback: true } } as Props["releaseWorkflow"]}
    />));
    const find = (label: string) => [...host.querySelectorAll("label")].find(value => value.textContent === label)?.querySelector<HTMLInputElement>("input");
    expect(find("教师公开反馈")?.disabled).toBe(true);
    expect(find("参考答案与本人答案")?.disabled).toBe(true);
    expect(find("题目统计")?.disabled).toBe(true);
    expect(find("群体统计")?.disabled).toBe(false);
    expect(find("逐题得分")?.disabled).toBe(false);
  } finally { await act(async () => root.unmount()); }
});
vi.mock("./RegradeCandidateEvidence", () => ({ RegradeCandidateEvidence: () => <span>当前修订证据</span> }));

it("异常候选仍出现在管理复核界面，接受/驳回及处理说明均可达", async () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
  const decide = vi.fn();
  type Props = ComponentProps<typeof ScoreWorkflowModals>;
  const container = document.createElement("div");
  const root = createRoot(container);
  const item = { id: "exception", status: "exception", revision: 8, review_note: "原异常说明", max_score: 5 };
  try {
    await act(async () => root.render(<ScoreWorkflowModals
      gradeTotal={0} publishedReleaseExists={false} actioning={null}
      attendanceWorkflow={{} as Props["attendanceWorkflow"]}
      releaseWorkflow={{ releaseVisibility: {} } as Props["releaseWorkflow"]}
      regradeWorkflow={{ regradeReviewOpen: true, regradeReview: { items: [item, { id: "resolved", status: "resolved" }] }, regradeReviewScores: {}, regradeReviewNotes: { exception: "新处理说明" }, reviewedEvidence: { exception: 8 }, decideRegradeItem: decide } as unknown as Props["regradeWorkflow"]}
    />));
    expect(container.textContent).toContain("异常待处理");
    expect(container.textContent).toContain("原异常说明");
    expect(container.textContent).toContain("处理异常必填");
    expect(container.querySelector("textarea")?.value).toBe("新处理说明");
    expect(container.querySelectorAll("article")).toHaveLength(1);
    const buttons = [...container.querySelectorAll("button")];
    expect(buttons.map(button => button.textContent)).toEqual(["接受", "驳回"]);
    await act(async () => buttons[0].click());
    expect(decide).toHaveBeenCalledWith(item, "accept");
    await act(async () => buttons[1].click());
    expect(decide).toHaveBeenCalledWith(item, "reject");
  } finally {
    await act(async () => root.unmount());
  }
});
