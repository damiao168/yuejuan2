// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { CommentTemplateManager } from "./CommentTemplateManager";
import type { ReviewCommentTemplate } from "../../../api/reviewAnnotations";
import type { ReactNode } from "react";

// 仅移除Drawer的portal/过渡成本；保留真实AntD输入、按钮、列表及保存回调，遵守默认5秒超时。
vi.mock("antd", async (load) => {
  const actual = await load<typeof import("antd")>();
  return { ...actual, Drawer: ({ open, children }: { open: boolean; children: ReactNode }) => open ? <aside aria-label="个人常用评语">{children}</aside> : null };
});

const template = { id: "synthetic-template", title: "步骤", shortcut: "step", content: "旧内容", usage_count: 0, revision: 2 } as ReviewCommentTemplate;
beforeEach(() => { vi.stubGlobal("matchMedia", () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} })); });
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe("真实常用评语管理器保存期间草稿", () => {
  it.each([false, true])("保存期间冻结草稿，成功=%s才清理已提交版本", async (success) => {
    let finish!: (value: unknown) => void;
    let fail!: (reason: Error) => void;
    const update = vi.fn((_current, _draft) => new Promise((resolve, reject) => { finish = resolve; fail = reject; }));
    render(<CommentTemplateManager open templates={[template]} onClose={vi.fn()} onCreate={vi.fn()} onUpdate={update} onDelete={vi.fn()} />);
    const button = (text: string) => screen.getByText(text).closest("button")!;
    fireEvent.click(button("编辑"));
    const input = screen.getByLabelText("评语内容") as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "第一次提交内容" } });
    fireEvent.click(button("保存修改"));
    await waitFor(() => expect(update).toHaveBeenCalledOnce());
    expect(update.mock.calls[0][1].content).toBe("第一次提交内容");
    expect(input.disabled).toBe(true);
    expect((screen.getByLabelText("评语名称") as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByLabelText("评语快捷码") as HTMLInputElement).disabled).toBe(true);
    expect(button("取消编辑").disabled).toBe(true);
    // 保存期间所有输入冻结，不会接受另一个随后被旧回执清空的版本。
    await act(async () => {
      if (success) finish({ comment_template: { ...template, content: "第一次提交内容", revision: 3 } });
      else fail(new Error("synthetic failure"));
    });
    expect((screen.getByLabelText("评语内容") as HTMLTextAreaElement).value).toBe(success ? "" : "第一次提交内容");
    expect(button(success ? "新增评语" : "保存修改")).toBeTruthy();
    expect((screen.getByLabelText("评语内容") as HTMLTextAreaElement).disabled).toBe(false);
    expect(update).toHaveBeenCalledTimes(1);
  });
});
