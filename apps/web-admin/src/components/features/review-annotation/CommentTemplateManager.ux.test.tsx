// @vitest-environment jsdom
import type { ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReviewCommentTemplate } from "../../../api/reviewAnnotations";
import { ApiClientError } from "../../../api/client";
import { CommentTemplateManager } from "./CommentTemplateManager";
vi.mock("antd", () => {
  const Input = Object.assign(({ value, onChange, "aria-label": label }: { value: string; onChange: React.ChangeEventHandler<HTMLInputElement>; "aria-label": string }) => <input aria-label={label} value={value} onChange={onChange} />, { TextArea: ({ value, onChange, "aria-label": label }: { value: string; onChange: React.ChangeEventHandler<HTMLTextAreaElement>; "aria-label": string }) => <textarea aria-label={label} value={value} onChange={onChange} /> });
  const List = Object.assign(({ dataSource, renderItem }: { dataSource: ReviewCommentTemplate[]; renderItem: (item: ReviewCommentTemplate) => ReactNode }) => <div>{dataSource.map(item => <div key={item.id}>{renderItem(item)}</div>)}</div>, { Item: Object.assign(({ children, actions }: { children: ReactNode; actions: ReactNode[] }) => <div>{children}{actions}</div>, { Meta: ({ title, description }: { title: ReactNode; description: ReactNode }) => <div>{title}{description}</div> }) });
  return { Input, List, Drawer: ({ open, children }: { open: boolean; children: ReactNode }) => open ? <div>{children}</div> : null, Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>, Popconfirm: ({ children, onConfirm }: { children: ReactNode; onConfirm: () => void }) => <span onClick={onConfirm}>{children}</span>, Alert: ({ message, action }: { message: ReactNode; action?: ReactNode }) => <aside>{message}{action}</aside>, Space: ({ children }: { children: ReactNode }) => <div>{children}</div>, Tag: ({ children }: { children: ReactNode }) => <span>{children}</span>, Empty: Object.assign(() => null, { PRESENTED_IMAGE_SIMPLE: null }) };
});
afterEach(cleanup);
describe("常用评语失败恢复", () => {
  it("删除失败仍保留评语和编辑输入，明确重试成功后关闭该草稿", async () => {
    const template = { id: "template", title: "步骤", shortcut: "step", content: "步骤完整", usage_count: 0, revision: 2 } as ReviewCommentTemplate;
    const remove = vi.fn().mockRejectedValueOnce(new Error("删除暂不可用")).mockResolvedValueOnce(undefined);
    render(<CommentTemplateManager open templates={[template]} onClose={vi.fn()} onCreate={vi.fn()} onUpdate={vi.fn()} onDelete={remove} />);
    fireEvent.click(screen.getByText("编辑")); fireEvent.change(screen.getByLabelText("评语内容"), { target: { value: "尚未保存的内容" } });
    fireEvent.click(screen.getByText("删除")); await screen.findByText("删除暂不可用"); expect((screen.getByLabelText("评语内容") as HTMLTextAreaElement).value).toBe("尚未保存的内容");
    fireEvent.click(screen.getByText("重试删除“步骤”")); await vi.waitFor(() => expect(remove).toHaveBeenCalledTimes(2)); expect(remove).toHaveBeenLastCalledWith(template);
    await vi.waitFor(() => expect((screen.getByLabelText("评语内容") as HTMLTextAreaElement).value).toBe(""));
  });
  it("保存冲突保留草稿且必须明确选用新版修订号才能继续", async () => {
    const original = { id: "template", title: "步骤", shortcut: "step", content: "旧内容", usage_count: 0, revision: 2 } as ReviewCommentTemplate;
    const latest = { ...original, content: "另一处保存的新内容", revision: 3 };
    const update = vi.fn().mockRejectedValueOnce(new ApiClientError(409, "revision_conflict", "内容已有新版")).mockResolvedValueOnce(undefined);
    const props = { open: true, onClose: vi.fn(), onCreate: vi.fn(), onUpdate: update, onDelete: vi.fn() };
    const view = render(<CommentTemplateManager {...props} templates={[original]} />);
    fireEvent.click(screen.getByText("编辑")); fireEvent.change(screen.getByLabelText("评语内容"), { target: { value: "我仍要保留的草稿" } });
    fireEvent.click(screen.getByText("保存修改")); await screen.findByText("评语已有新版本，当前输入已保留。请核对列表中的新内容后继续。");
    expect((screen.getByLabelText("评语内容") as HTMLTextAreaElement).value).toBe("我仍要保留的草稿");
    expect((screen.getByText("保存修改") as HTMLButtonElement).disabled).toBe(true);
    view.rerender(<CommentTemplateManager {...props} templates={[latest]} />);
    fireEvent.click(screen.getByText("以新版为基础继续编辑"));
    expect((screen.getByLabelText("评语内容") as HTMLTextAreaElement).value).toBe("我仍要保留的草稿");
    fireEvent.click(screen.getByText("保存修改"));
    await vi.waitFor(() => expect(update).toHaveBeenLastCalledWith(latest, expect.objectContaining({ content: "我仍要保留的草稿" })));
  });
});
