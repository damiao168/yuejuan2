// @vitest-environment jsdom
import { act, useState, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { fireEvent } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import type { StudentPrintContext } from "../../../api/printing";
import { StudentPrintLayoutModal } from "./StudentPrintLayoutModal";

const previewState = vi.hoisted(() => ({ loading: false, renderedPage: undefined as number | undefined,
  downloadSource: undefined as ((fileId: string) => Promise<unknown>) | undefined, requestBlob: vi.fn() }));
vi.mock("../../../api/client", () => ({ apiClient: { requestBlob: previewState.requestBlob } }));
vi.mock("../editor/useTemplatePreview", () => ({ useTemplatePreview: (paper: { id: string }, downloadSource: (fileId: string) => Promise<unknown>) => {
  previewState.downloadSource = downloadSource;
  const [pageNo, setPageNo] = useState(1);
  return { pageNo, setPageNo, preview: { sourceId: paper.id, loading: previewState.loading, pageCount: 2, width: 420, height: 594,
    pageSizes: [{ width: 420, height: 594 }, { width: 420, height: 594 }], imageUrl: `blob:page-${previewState.renderedPage ?? pageNo}` } };
} }));
vi.mock("antd", () => ({
  Modal: ({ title, children, footer }: { title: string; children: ReactNode; footer: ReactNode }) => <section aria-label={title}>{children}{footer}</section>,
  Button: ({ children, onClick, disabled }: { children: ReactNode; onClick: () => void; disabled: boolean }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Alert: ({ message }: { message: ReactNode }) => <p>{message}</p>,
  Spin: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Select: ({ value, onChange, options, "aria-label": label }: { value: number; onChange: (value: number) => void; options: { value: number; label: string }[]; "aria-label": string }) =>
    <select aria-label={label} value={value} onChange={event => onChange(Number(event.target.value))}>{options.map(option => <option value={option.value} key={option.value}>{option.label}</option>)}</select>,
  InputNumber: ({ value, onChange, disabled, "aria-label": label }: { value: number; onChange: (value: number) => void; disabled: boolean; "aria-label": string }) =>
    <input type="number" aria-label={label} value={value} disabled={disabled} onChange={event => onChange(Number(event.target.value))} />,
  Checkbox: ({ checked, disabled, onChange, children }: { checked: boolean; disabled: boolean; onChange: (event: { target: { checked: boolean } }) => void; children: ReactNode }) =>
    <label><input type="checkbox" checked={checked} disabled={disabled} onChange={onChange} />{children}</label>
}));

const template = { id: "template", tenant_id: "tenant", exam_id: "exam", version_no: 1 } as AnswerSheetTemplate;
const context = { print_source: { file_asset_id: "source", content_type: "application/pdf", sha256: "frozen-hash" }, qr_layout_page_nos: [1, 2] } as StudentPrintContext;

describe("print placement confirmation", () => {
  let host: HTMLDivElement;
  let root: Root;
  const confirm = vi.fn().mockResolvedValue(undefined);
  beforeEach(async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    previewState.loading = false; previewState.renderedPage = undefined; confirm.mockClear(); previewState.requestBlob.mockReset();
    previewState.requestBlob.mockResolvedValue({ blob: new Blob(["synthetic source"]), contentType: "application/pdf" });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    await act(async () => root.render(<StudentPrintLayoutModal template={template} context={context} busy={false} onCancel={vi.fn()} onConfirm={confirm} />));
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  const ok = () => [...host.querySelectorAll("button")].find(button => button.textContent === "确认位置并继续")!;
  const checkbox = () => host.querySelector<HTMLInputElement>('input[type="checkbox"]')!;

  it("requires an explicit confirmation for every page and sends only the frozen source layout", async () => {
    expect(ok().disabled).toBe(true);
    await act(async () => checkbox().click());
    expect(ok().disabled).toBe(true);
    await act(async () => fireEvent.change(host.querySelector("select")!, { target: { value: "2" } }));
    expect(checkbox().checked).toBe(false);
    await act(async () => checkbox().click());
    expect(ok().disabled).toBe(false);
    await act(async () => ok().click());
    expect(confirm).toHaveBeenCalledWith({ source_sha256: "frozen-hash", pages: [
      expect.objectContaining({ page_no: 1, width: expect.any(Number), height: expect.any(Number) }),
      expect.objectContaining({ page_no: 2, width: expect.any(Number), height: expect.any(Number) })
    ] });
    expect(confirm.mock.calls[0][0].pages.every((page: object) => !("confirmed" in page))).toBe(true);
  });

  it("downloads the exam-managed template source through its scoped print endpoint", async () => {
    const scopedTemplate = { ...template, id: "exam-managed/template" };
    await act(async () => root.render(<StudentPrintLayoutModal template={scopedTemplate} context={context} busy={false} onCancel={vi.fn()} onConfirm={confirm} />));
    const downloader = previewState.downloadSource;
    await downloader?.("source");
    expect(previewState.requestBlob).toHaveBeenCalledExactlyOnceWith("/api/v1/answer-sheet-templates/exam-managed%2Ftemplate/print-source");
    await act(async () => root.render(<StudentPrintLayoutModal template={scopedTemplate} context={context} busy={false} onCancel={vi.fn()} onConfirm={confirm} />));
    expect(previewState.downloadSource).toBe(downloader);
  });

  it("invalidates page confirmation after changing its position", async () => {
    await act(async () => checkbox().click());
    expect(checkbox().checked).toBe(true);
    await act(async () => fireEvent.keyDown(host.querySelector('[role="button"]')!, { key: "ArrowLeft" }));
    expect(checkbox().checked).toBe(false);
    expect(ok().disabled).toBe(true);
    expect(confirm).not.toHaveBeenCalled();
  });

  it("cannot confirm a newly selected page while its previous preview remains visible", async () => {
    await act(async () => checkbox().click());
    previewState.loading = true; previewState.renderedPage = 1;
    await act(async () => fireEvent.change(host.querySelector("select")!, { target: { value: "2" } }));
    expect(checkbox().disabled).toBe(true);
    previewState.loading = false;
    await act(async () => root.render(<StudentPrintLayoutModal template={template} context={context} busy={false} onCancel={vi.fn()} onConfirm={confirm} />));
    expect(checkbox().disabled).toBe(true);
    expect(ok().disabled).toBe(true);
    previewState.renderedPage = 2;
    await act(async () => root.render(<StudentPrintLayoutModal template={template} context={context} busy={false} onCancel={vi.fn()} onConfirm={confirm} />));
    expect(checkbox().disabled).toBe(false);
    expect(checkbox().checked).toBe(false);
    expect(ok().disabled).toBe(true);
  });
});
