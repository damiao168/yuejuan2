import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, Button, Checkbox, InputNumber, Modal, Select, Space, Spin } from "antd";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import type { PaperVersion } from "../../../api/papers";
import type { StudentPrintContext, StudentPrintLayout, StudentPrintQRPage } from "../../../api/printing";
import { downloadStudentPrintSource } from "../../../api/printing";
import { useTemplatePreview } from "../editor/useTemplatePreview";
import "./student-print-layout.css";

type Placement = StudentPrintQRPage & { confirmed: boolean };

function squareForPage(pageNo: number, size: { width: number; height: number }, pdf: boolean): Placement {
  // PDF 的预览尺寸是点，图片只有像素比例，后端按短边 210 毫米打印。
  const mmPerUnit = pdf ? 25.4 / 72 : 210 / Math.min(size.width, size.height);
  const width = 32 / (size.width * mmPerUnit);
  const height = 32 / (size.height * mmPerUnit);
  return { page_no: pageNo, x: Math.max(0, 1 - width - .03), y: .03, width, height, confirmed: false };
}

export function StudentPrintLayoutModal({ template, context, busy, error, onCancel, onConfirm }: Readonly<{
  template: AnswerSheetTemplate;
  context: StudentPrintContext;
  busy: boolean;
  error?: string;
  onCancel: () => void;
  onConfirm: (layout: StudentPrintLayout) => Promise<void>;
}>) {
  const source = context.print_source;
  const paper = useMemo<PaperVersion | undefined>(() => source ? {
    id: `${source.file_asset_id}:${source.sha256}`, tenant_id: template.tenant_id, exam_id: template.exam_id,
    file_asset_id: source.file_asset_id, version_no: template.version_no, status: "locked", purpose: "answer_sheet",
    file: { original_name: "答题卡原页", content_type: source.content_type, size_bytes: 0, hash_sha256: source.sha256 }
  } : undefined, [source, template.tenant_id, template.exam_id, template.version_no]);
  const downloadSource = useCallback(() => downloadStudentPrintSource(template.id), [template.id]);
  const { preview, pageNo, setPageNo } = useTemplatePreview(paper, downloadSource);
  const required = context.qr_layout_page_nos ?? [];
  // 默认坐标只提供起点，用户必须看到原页后逐页勾选，不能把预填位置当成已确认。
  const [placements, setPlacements] = useState<Record<number, Placement>>({});
  const [selected, setSelected] = useState(required[0] ?? 1);
  const [renderedPage, setRenderedPage] = useState(0);
  const renderedURL = useRef<string | undefined>(undefined);
  useEffect(() => { setPageNo(selected); }, [selected, setPageNo]);
  useEffect(() => {
    // 翻页时旧图仍可能短暂留在预览状态，必须等新图完成后才能确认这一页。
    if (preview.imageUrl && preview.imageUrl !== renderedURL.current && !preview.loading) {
      renderedURL.current = preview.imageUrl;
      setRenderedPage(pageNo);
    }
  }, [preview.imageUrl, preview.loading, pageNo]);
  const size = preview.pageSizes?.[selected - 1];
  const placement = placements[selected] ?? (size ? squareForPage(selected, size, source?.content_type === "application/pdf") : undefined);
  const oversized = placement && (placement.width >= 1 || placement.height >= 1);
  const ready = Boolean(source && preview.sourceId === paper?.id && !preview.loading && !preview.error && preview.imageUrl && renderedPage === selected && pageNo === selected && placement && !oversized);
  const allConfirmed = required.length > 0 && required.every(number => placements[number]?.confirmed);

  const update = (patch: Partial<Placement>) => {
    if (!placement || !ready || busy) return;
    const next = { ...placement, ...patch };
    next.x = Math.min(Math.max(next.x, 0), Math.max(0, 1 - next.width));
    next.y = Math.min(Math.max(next.y, 0), Math.max(0, 1 - next.height));
    setPlacements(current => ({ ...current, [selected]: next }));
  };
  const changePosition = (x: number, y: number) => update({ x, y, confirmed: false });
  return <Modal open title="确认二维码打印位置" width={900} onCancel={onCancel} closable={!busy} maskClosable={false}
    footer={<Space><Button disabled={busy} onClick={onCancel}>返回</Button><Button type="primary" loading={busy} disabled={!allConfirmed || !ready}
      onClick={() => source && void onConfirm({ source_sha256: source.sha256, pages: required.map(number => {
        const { confirmed: _, ...page } = placements[number];
        return page;
      }) })}>确认位置并继续</Button></Space>}>
    <p>点击原页中的空白处放置二维码，逐页核对框内没有题目、说明或个人信息。打印会覆盖所选区域，确认后本批次位置固定。</p>
    {!source ? <Alert type="error" message="未读取到答题卡原页，请关闭后刷新打印状态。" /> : null}
    <Space wrap>
      <Select aria-label="核对二维码位置的页码" value={selected} disabled={busy} options={required.map(number => ({ value: number,
        label: `第 ${number} 页${placements[number]?.confirmed ? "（已核对）" : "（待核对）"}` }))}
        onChange={number => { setSelected(number); setPageNo(number); }} />
      <span>左边距</span><InputNumber aria-label="二维码左边距百分比" addonAfter="%" min={0} max={placement ? (1 - placement.width) * 100 : 100}
        value={placement ? Math.round(placement.x * 1000) / 10 : 0} disabled={!ready || busy} onChange={value => value !== null && placement && changePosition(Number(value) / 100, placement.y)} />
      <span>上边距</span><InputNumber aria-label="二维码上边距百分比" addonAfter="%" min={0} max={placement ? (1 - placement.height) * 100 : 100}
        value={placement ? Math.round(placement.y * 1000) / 10 : 0} disabled={!ready || busy} onChange={value => value !== null && placement && changePosition(placement.x, Number(value) / 100)} />
    </Space>
    {preview.loading ? <Spin tip="正在读取答题卡原页"><div style={{ minHeight: 120 }} /></Spin> : null}
    {preview.error ? <Alert type="error" showIcon message={preview.error} /> : null}
    {oversized ? <Alert type="error" message="本页尺寸不足以放置二维码，请核对答题卡纸张尺寸。" /> : null}
    {preview.imageUrl && placement ? <div className="student-print-layout-page" style={{ aspectRatio: `${preview.width} / ${preview.height}` }}
      role="button" tabIndex={0} aria-label="在原页选择二维码位置" aria-disabled={!ready || busy}
      onKeyDown={event => {
        const moves: Record<string, readonly [number, number]> = { ArrowLeft: [-.01, 0], ArrowRight: [.01, 0], ArrowUp: [0, -.01], ArrowDown: [0, .01] };
        const move = moves[event.key];
        if (move) { event.preventDefault(); changePosition(placement.x + move[0], placement.y + move[1]); }
      }}
      onClick={event => {
        if (!ready) return;
        const bounds = event.currentTarget.getBoundingClientRect();
        if (bounds.width && bounds.height) changePosition((event.clientX - bounds.left) / bounds.width - placement.width / 2,
          (event.clientY - bounds.top) / bounds.height - placement.height / 2);
      }}>
      <img src={preview.imageUrl} alt={`第 ${selected} 页答题卡原页`} draggable={false} />
      {ready ? <div className="student-print-layout-box" aria-label="二维码将覆盖的区域" style={{ left: `${placement.x * 100}%`, top: `${placement.y * 100}%`,
        width: `${placement.width * 100}%`, height: `${placement.height * 100}%` }}>二维码</div> : null}
    </div> : null}
    <Checkbox checked={Boolean(placements[selected]?.confirmed)} disabled={!ready || busy} onChange={event => update({ confirmed: event.target.checked })}>
      已核对本页，所选区域可被二维码覆盖
    </Checkbox>
    <p className="muted">已核对 {required.filter(number => placements[number]?.confirmed).length} / {required.length} 页</p>
    {error ? <Alert type="error" showIcon message={error} /> : null}
  </Modal>;
}
