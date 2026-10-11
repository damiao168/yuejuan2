// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperVersion } from "../../api/papers";
import type { StudentPrintCandidate } from "../../api/printing";
import { useTemplatePreview } from "./editor/useTemplatePreview";
import { answerSource, answerTemplate } from "./testing/answerSheetFixtures";
import { StudentPrintPanel } from "./view/StudentPrintingView";
import type { TemplatePageViewState } from "./view/templateViewTypes";

const mocks = vi.hoisted(() => ({ download: vi.fn(), getDocument: vi.fn() }));
vi.mock("../../api/files", () => ({ downloadFileBlob: mocks.download }));
vi.mock("../../api/client", () => ({ getUserErrorMessage: (_error: unknown, fallback: string) => fallback }));
vi.mock("pdfjs-dist", () => ({ GlobalWorkerOptions: {}, getDocument: mocks.getDocument }));
vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({ default: "mock-worker.js" }));
vi.mock("antd", () => {
  type Props = { children?: ReactNode; message?: ReactNode };
  type Column = { title: string; dataIndex?: keyof StudentPrintCandidate; render?: (value: unknown, candidate: StudentPrintCandidate) => ReactNode };
  const Box = ({ children, message }: Props) => <div>{message}{children}</div>;
  return {
    Alert: Box, Button: Box, Checkbox: Box, Input: Object.assign(Box, { TextArea: Box }),
    List: Object.assign(Box, { Item: Box }), Modal: Box, Spin: Box, Tag: Box,
    // 旧页面测试的 Table 不执行列回调；这里消费真实列，直接核对学生状态单元格。
    Table: ({ dataSource, columns }: { dataSource: StudentPrintCandidate[]; columns: Column[] }) => (
      <table><tbody>{dataSource.map(candidate => (
        <tr key={candidate.student_id}>{columns.map(column => (
          <td key={column.title} data-column={column.title}>
            {column.render ? column.render(undefined, candidate) : candidate[column.dataIndex!]}
          </td>
        ))}</tr>
      ))}</tbody></table>
    )
  };
});

const candidate: StudentPrintCandidate = {
  student_id: "student", student_no: "01", student_name: "学生甲", class_id: "class", class_name: "高一一班",
  attendance_status: "expected", has_active_sheet: true
};

describe("answer-sheet template batch47 real entries", () => {
  let root: Root;
  let host: HTMLDivElement;
  let current: ReturnType<typeof useTemplatePreview>;
  function PreviewProbe({ paper }: { paper: PaperVersion }) {
    current = useTemplatePreview(paper);
    return null;
  }
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it.each<[Partial<StudentPrintCandidate>, string]>([
    [{ attendance_status: "absent", active_sheet_status: "observed" }, "缺考，不签发"],
    [{ has_active_sheet: false, active_sheet_status: "observed" }, "尚未生成"],
    [{ active_sheet_status: "observed" }, "已扫描"],
    [{ active_sheet_status: "conflict" }, "需核验"],
    [{ active_sheet_status: "issued" }, "已生成，待扫描"],
    [{}, "已生成，待扫描"]
  ])("preserves student sheet status for %j", async (patch, expected) => {
    const item = { ...candidate, ...patch };
    const view = {
      selectedTemplate: { ...answerTemplate, status: "locked" }, canManage: true,
      printing: {
        printContext: { candidates: [item], batches: [] }, printContextLoading: false, printBusy: false,
        printableCandidates: [], loadPrintContext: vi.fn(), openPrintModal: vi.fn(), downloadPrintBatch: vi.fn()
      }
    } as unknown as TemplatePageViewState;
    await act(async () => root.render(<StudentPrintPanel view={view} />));
    expect(host.querySelector('[data-column="答题卡状态"]')?.textContent).toBe(expected);
  });

  it("disables text-layer assistance when a later PDF page fails after an earlier page has text", async () => {
    const pages = [
      { getTextContent: vi.fn().mockResolvedValue({ items: [{ str: "第1题" }] }) },
      { getTextContent: vi.fn().mockRejectedValue(new Error("synthetic second-page text failure")) }
    ].map(page => ({
      ...page,
      getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }),
      render: vi.fn(() => ({ promise: Promise.resolve() }))
    }));
    const pdfDocument = {
      numPages: pages.length, getPage: vi.fn(async (pageNo: number) => pages[pageNo - 1]), destroy: vi.fn(async () => {})
    } as unknown as PDFDocumentProxy;
    const blob = new Blob(["synthetic template"], { type: "application/pdf" });
    Object.defineProperty(blob, "arrayBuffer", { value: async () => new ArrayBuffer(8) });
    mocks.download.mockResolvedValue({ blob, contentType: "application/pdf" });
    mocks.getDocument.mockReturnValue({ promise: Promise.resolve(pdfDocument) });
    class PreviewURL extends URL {
      static createObjectURL = vi.fn(() => "blob:batch47-preview");
      static revokeObjectURL = vi.fn();
    }
    vi.stubGlobal("URL", PreviewURL);
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({} as CanvasRenderingContext2D);
    vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(callback => callback(new Blob(["preview"], { type: "image/png" })));

    // 前页命中文字仍须读取后页；后页失败后整份文字层辅助回退，PDF 预览不报错。
    await act(async () => root.render(<PreviewProbe paper={answerSource} />));
    for (const page of pages) expect(page.getTextContent).toHaveBeenCalledOnce();
    expect(current.preview).toMatchObject({ pageCount: 2, textLayerAvailable: false, textLayerChecking: false });
    expect(current.preview.error).toBeUndefined();
  });
});
