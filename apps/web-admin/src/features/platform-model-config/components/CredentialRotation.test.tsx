// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { ModelOperationQueue } from "../lib/modelOperationQueue";
import { CredentialRotation, credentialRotationTargets } from "./CredentialRotation";

const api = vi.hoisted(() => ({ rotate: vi.fn() }));
vi.mock("../../../api/modelApiConfig", () => ({ rotateManagedModelAPIConfigCredential: api.rotate }));
vi.mock("./ModelConfigDrawer", () => ({ ModelConfigDrawer: ({ children, extra, open, title, closable, onClose }: { children: ReactNode; extra: ReactNode; open: boolean; title: string; closable: boolean; onClose: () => void }) => open ? <section role="dialog" aria-label={title}>{extra}<button disabled={!closable} onClick={onClose}>关闭向导</button>{children}</section> : null }));
vi.mock("antd", () => ({
  Button: ({ children, disabled, loading, onClick }: { children: ReactNode; disabled?: boolean; loading?: boolean; onClick: () => void }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
  Alert: ({ message }: { message: string }) => <p role="alert">{message}</p>,
  Select: ({ options, value, onChange, "aria-label": label }: { options: { value: string; label: string }[]; value: string; onChange: (value: string) => void; "aria-label": string }) => <select aria-label={label} value={value ?? ""} onChange={event => onChange(event.target.value)}><option value="" />{options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
  Input: { Password: ({ value, onChange, "aria-label": label }: { value: string; onChange: React.ChangeEventHandler<HTMLInputElement>; "aria-label": string }) => <input type="password" aria-label={label} value={value} onChange={onChange} /> },
  Checkbox: Object.assign(({ children, checked, disabled, onChange }: { children: ReactNode; checked?: boolean; disabled?: boolean; onChange?: React.ChangeEventHandler<HTMLInputElement> }) => <label><input type="checkbox" checked={Boolean(checked)} disabled={disabled} onChange={onChange} />{children}</label>, { Group: ({ onChange, disabled }: { onChange: (values: string[]) => void; disabled: boolean }) => <button disabled={disabled} onClick={() => onChange(["model-a", "model-b"])}>选择两个模型</button> })
}));

const first = { id: "model-a", tenant_id: "school-a", display_name: "模型A", provider_key: "custom", base_url: "https://api.example.test/v1", model_name: "a", updated_at: "2026-10-28T00:00:00Z" } as ManagedModelAPIConfig;
const second = { ...first, id: "model-b", display_name: "模型B", model_name: "b" };
afterEach(() => { cleanup(); vi.resetAllMocks(); });

async function selectBatch() {
  fireEvent.click(screen.getByRole("button", { name: "批量更换密钥" }));
  fireEvent.change(screen.getByRole("combobox", { name: "供应商连接" }), { target: { value: first.id } });
  fireEvent.click(screen.getByRole("button", { name: "选择两个模型" }));
  fireEvent.change(screen.getByLabelText("新 API Key"), { target: { value: "synthetic-batch-key-123456" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "我已核对所选模型使用同一份新密钥" }));
  fireEvent.click(screen.getByRole("button", { name: "确认更换所选密钥" }));
}

describe("学校模型批量轮换", () => {
  it("连接分组不跨学校、供应商、域名或HTTPS边界", () => {
    const configs = [first, second, { ...first, id: "other-school", tenant_id: "school-b" }, { ...first, id: "other-provider", provider_key: "other" }, { ...first, id: "other-origin", base_url: "https://different.example.test/v1" }, { ...first, id: "insecure", base_url: "http://api.example.test/v1" }];
    expect(credentialRotationTargets(configs, "school-a", first).map(config => config.id)).toEqual([first.id, second.id]);
  });
  it("逐项保存并显示部分成功，等待期间不关闭，密钥不留在输入或草稿", async () => {
    let rejectSecond!: (reason: unknown) => void;
    api.rotate.mockResolvedValueOnce({ config: { ...first, updated_at: "new-version" } }).mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectSecond = reject; }));
    const saved = vi.fn(); const state = vi.fn(); const queue = new ModelOperationQueue();
    render(<CredentialRotation tenantID="school-a" configs={[first, second]} disabled={false} queue={queue} onConfigSaved={saved} onStateChange={state} />);
    await selectBatch();
    await waitFor(() => expect(api.rotate).toHaveBeenCalledTimes(2));
    expect(api.rotate.mock.calls.map(call => [call[0], call[1], call[3]])).toEqual([[first.id, "school-a", first.updated_at], [second.id, "school-a", second.updated_at]]);
    expect((screen.getByLabelText("新 API Key") as HTMLInputElement).value).toBe("");
    expect((screen.getByRole("button", { name: "关闭向导" }) as HTMLButtonElement).disabled).toBe(true);
    expect(queue.isBusy("school-a", second.id)).toBe(true);
    await act(async () => rejectSecond(new Error("network failure")));
    await screen.findByText("已确认更新 1 / 2 项。");
    expect(saved).toHaveBeenCalledOnce();
    expect(saved).toHaveBeenCalledWith("school-a", expect.objectContaining({ id: first.id }));
    expect(queue.isBusy("school-a", second.id)).toBe(false);
    expect(document.body.textContent).not.toContain("synthetic-batch-key-123456");
    expect(state).toHaveBeenCalledWith(true, true);
  });
  it("切校后的迟到结果不回填，也不继续发出第二项轮换", async () => {
    let resolve!: (value: unknown) => void;
    api.rotate.mockImplementation(() => new Promise(done => { resolve = done; }));
    const saved = vi.fn(); const state = vi.fn(); const queue = new ModelOperationQueue();
    const props = { configs: [first, second], disabled: false, queue, onConfigSaved: saved, onStateChange: state };
    const view = render(<CredentialRotation {...props} tenantID="school-a" />);
    await selectBatch();
    expect(api.rotate).toHaveBeenCalledOnce();
    view.rerender(<CredentialRotation {...props} tenantID="school-b" />);
    view.rerender(<CredentialRotation {...props} tenantID="school-a" />);
    await act(async () => resolve({ config: first }));
    expect(api.rotate).toHaveBeenCalledOnce(); expect(saved).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(queue.isBusy("school-a", first.id)).toBe(false);
  });
});
