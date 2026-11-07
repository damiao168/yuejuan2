import { Alert, Button, Spin } from "antd";
import { GlobalWorkerOptions, getDocument } from "pdfjs-dist";
import pdfWorker from "pdfjs-dist/build/pdf.worker.min.mjs?url";
import { useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { type NormalizedPoint, type RegistrationCorrection } from "../api/capture";
import { getUserErrorMessage } from "../api/client";
import { downloadFileBlob } from "../api/files";


GlobalWorkerOptions.workerSrc = pdfWorker;
// 对应点按左上、右上、右下、左下成对排列，坐标归一化到 0–1，与预览缩放无关。
export const initialPoints: NormalizedPoint[] = [
  { x: 0, y: 0 },
  { x: 1, y: 0 },
  { x: 1, y: 1 },
  { x: 0, y: 1 },
];
export const labels = ["左上", "右上", "右下", "左下"];
export const clamp = (value: number) => Math.max(0, Math.min(1, value));
export const correctionErrorLabels: Record<string, string> = {
  coverage_too_low: "对齐后覆盖不足，请把四个点贴近答卷四角后重试",
  registration_required_region_incomplete:
    "对齐后有作答区域超出原图，请核对对应点；原图缺页或缺边时请重新扫描",
  registration_roi_evidence_invalid:
    "对应点无法形成有效的页面映射，请按左上、右上、右下、左下重新标记",
};
export const correctionErrorText = (code: string) =>
  correctionErrorLabels[code] ?? "预览失败，请调整对应点后重试";
export const errorText = (error: unknown) => getUserErrorMessage(error, "操作失败，请重试");

export function pointKeyOffset(
  key: string,
  delta: number,
): readonly [number, number] | undefined {
  switch (key) {
    case "ArrowLeft":
      return [-delta, 0];
    case "ArrowRight":
      return [delta, 0];
    case "ArrowUp":
      return [0, -delta];
    case "ArrowDown":
      return [0, delta];
    default:
      return undefined;
  }
}

export function correctionStatusText(status: RegistrationCorrection["status"] | undefined) {
  switch (status) {
    case "preview_ready":
      return "预览检查通过";
    case "applied":
      return "校正已应用";
    case "undone":
      return "已撤销校正";
    case "failed":
      return "预览检查未通过";
    default:
      return "等待生成预览";
  }
}

export function previewFileAssetId(current: RegistrationCorrection) {
  // 状态就绪且预览文件存在才继续下载；失败时保留业务错误码日志和原来的用户提示。
  if (
    current.status !== "preview_ready" ||
    !current.preview_registered_file_asset_id
  ) {
    if (current.error_code) {
      console.warn("registration correction preview failed:", current.error_code);
    }
    throw new Error(
      current.error_code
        ? correctionErrorText(current.error_code)
        : "校正预览未完成，请稍后重试",
    );
  }
  return current.preview_registered_file_asset_id;
}

export async function imageURL(fileId: string, pageNo = 1) {
  const download = await downloadFileBlob(fileId);
  if (download.contentType !== "application/pdf")
    return URL.createObjectURL(download.blob);
  const pdf = await getDocument({ data: await download.blob.arrayBuffer() })
    .promise;
  try {
    const page = await pdf.getPage(Math.min(pageNo, pdf.numPages));
    const base = page.getViewport({ scale: 1 });
    const viewport = page.getViewport({
      scale: Math.min(1.5, 1400 / base.width),
    });
    const canvas = document.createElement("canvas");
    canvas.width = Math.ceil(viewport.width);
    canvas.height = Math.ceil(viewport.height);
    const context = canvas.getContext("2d");
    if (!context) throw new Error("浏览器无法创建模板画布");
    await page.render({ canvasContext: context, viewport, canvas }).promise;
    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, "image/png"),
    );
    if (!blob) throw new Error("模板页面渲染失败");
    return URL.createObjectURL(blob);
  } finally {
    await pdf.destroy();
  }
}

export function PointCanvas({
  title,
  url,
  points,
  editable,
  zoom,
  loading,
  error,
  onRetry,
  onImageError,
  onChange,
}: Readonly<{
  title: string;
  url?: string;
  points: NormalizedPoint[];
  editable: boolean;
  zoom: number;
  loading: boolean;
  error?: string;
  onRetry: () => void;
  onImageError: () => void;
  onChange: (points: NormalizedPoint[]) => void;
}>) {
  const ref = useRef<HTMLDivElement>(null);
  const [dragging, setDragging] = useState<number>();
  function move(index: number, clientX: number, clientY: number) {
    if (!editable) return;
    const bounds = ref.current?.getBoundingClientRect();
    if (!bounds) return;
    const next = points.map((point) => ({ ...point }));
    next[index] = {
      x: clamp((clientX - bounds.left) / bounds.width),
      y: clamp((clientY - bounds.top) / bounds.height),
    };
    onChange(next);
  }
  function keyMove(index: number, event: KeyboardEvent<HTMLButtonElement>) {
    const delta = event.shiftKey ? 0.01 : 0.002;
    const offset = pointKeyOffset(event.key, delta);
    if (!offset) return;
    event.preventDefault();
    const next = points.map((point) => ({ ...point }));
    next[index] = {
      x: clamp(next[index].x + offset[0]),
      y: clamp(next[index].y + offset[1]),
    };
    onChange(next);
  }
  return (
    <section className="correction-canvas-column">
      <header>
        <strong>{title}</strong>
        <span>
          {editable
            ? "拖动圆点，或选中后用方向键微调（按住 Shift 加速）"
            : "模板参考点已锁定（开启『同时调整模板参考点』后可调）"}
        </span>
      </header>
      <div className="correction-canvas-scroll">
        <div
          ref={ref}
          className="correction-canvas"
          style={{ width: `${zoom * 620}px` }}
          onPointerMove={(event) =>
            dragging === undefined
              ? undefined
              : move(dragging, event.clientX, event.clientY)
          }
          onPointerUp={() => setDragging(undefined)}
        >
          {url ? (
            <img src={url} alt={title} draggable={false} onError={onImageError} />
          ) : (
            <div className="correction-loading">
              {/* 一幅图已失败时立即说明原因，另一幅仍在读取也不能把已知失败显示成加载。 */}
              {error || !loading ? <Alert type="error" showIcon message={`${title}读取失败`} description={error || "尚未读取到可核对图像，请重新读取材料。"} action={<Button onClick={onRetry}>重新读取{title}</Button>} /> : <><Spin /><span>正在读取{title}</span></>}
            </div>
          )}
          {points.map((point, index) => (
            <button
              type="button"
              key={labels[index]}
              className={`correction-point point-${index + 1}`}
              style={{ left: `${point.x * 100}%`, top: `${point.y * 100}%` }}
              disabled={!editable}
              aria-label={`${title}${labels[index]}点`}
              onKeyDown={(event) => keyMove(index, event)}
              onPointerDown={(event: PointerEvent<HTMLButtonElement>) => {
                if (!editable) return;
                event.currentTarget.setPointerCapture(event.pointerId);
                setDragging(index);
              }}
            >
              <span>{index + 1}</span>
            </button>
          ))}
        </div>
      </div>
    </section>
  );
}
