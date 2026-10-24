export type ImageReadingAxis = "none" | "vertical" | "horizontal";

export interface ImageReadingLayoutInput {
  imageWidth: number;
  imageHeight: number;
  // 传入已扣除工具栏的画布大小，单位为 CSS px。
  viewportWidth: number;
  viewportHeight: number;
  gap?: number;
}

export interface ImageReadingTile {
  index: number;
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface ImageReadingLayout {
  axis: ImageReadingAxis;
  tiles: ImageReadingTile[];
  columns: number;
  rows: number;
  perGroup: number;
  groupCount: number;
  scale: number;
  overlap: number;
  gap: number;
  tileWidth: number;
  tileHeight: number;
  displayWidth: number;
  displayHeight: number;
}

const OVERLAP = 0.1;
const ASPECT_MISMATCH_THRESHOLD = 1.8;
const MAX_TILES = 4096;
const DEFAULT_GAP = 12;

const isPositiveFinite = (value: number) => Number.isFinite(value) && value > 0;

/**
 * 只计算阅读视口，不裁剪或改写图片；所有 tile 坐标均以原图左上角为原点，单位为原图 px。
 * vertical 沿原图纵向分段后横排，horizontal 沿横向分段后竖排；scale 统一用于段内显示。
 * 图片尺寸无效时返回空计划；画布尚未测量时保留整图，等有效画布尺寸到达后再规划。
 */
export function planImageReadingLayout(input: ImageReadingLayoutInput): ImageReadingLayout {
  const { imageWidth, imageHeight, viewportWidth, viewportHeight } = input;
  const gap = input.gap !== undefined && Number.isFinite(input.gap) && input.gap >= 0 ? input.gap : DEFAULT_GAP;
  const validImage = isPositiveFinite(imageWidth) && isPositiveFinite(imageHeight);
  const validViewport = isPositiveFinite(viewportWidth) && isPositiveFinite(viewportHeight);
  const wholeScale = validImage && validViewport
    ? Math.min(viewportWidth / imageWidth, viewportHeight / imageHeight)
    : 1;
  const whole: ImageReadingLayout = {
    axis: "none",
    tiles: validImage ? [{ index: 0, x: 0, y: 0, width: imageWidth, height: imageHeight }] : [],
    columns: 1,
    rows: 1,
    perGroup: 1,
    groupCount: validImage ? 1 : 0,
    scale: wholeScale,
    overlap: OVERLAP,
    gap,
    tileWidth: validImage ? imageWidth : 0,
    tileHeight: validImage ? imageHeight : 0,
    displayWidth: validImage ? imageWidth * wholeScale : 0,
    displayHeight: validImage ? imageHeight * wholeScale : 0,
  };
  if (!validImage || !validViewport) return whole;

  // 相同图片在横屏和竖屏下可能有不同读法，必须比较图片与画布的比例，而非固定判断图片长宽比。
  // 用对数比较，避免极长图的比例先溢出，再污染段数和循环边界。
  const logAspectMismatch = Math.log(imageHeight) - Math.log(imageWidth)
    - Math.log(viewportHeight) + Math.log(viewportWidth);
  if (Math.abs(logAspectMismatch) <= Math.log(ASPECT_MISMATCH_THRESHOLD) + 1e-9) return whole;

  const vertical = logAspectMismatch > 0;
  const sourceLength = vertical ? imageHeight : imageWidth;
  const sourceCross = vertical ? imageWidth : imageHeight;
  const viewportLength = vertical ? viewportHeight : viewportWidth;
  const viewportCross = vertical ? viewportWidth : viewportHeight;
  const targetCrossSize = vertical ? 640 : 300;
  let slots = Math.max(1, Math.min(3, Math.round(viewportCross / targetCrossSize)));

  // 分成过多列/行可能比整图 contain 更小；逐档下调，只有正文能放大时才采用该档。
  while (slots > 1) {
    const candidateCross = (viewportCross - (slots - 1) * gap) / slots;
    const logScaleGain = Math.abs(logAspectMismatch) + Math.log(candidateCross) - Math.log(viewportCross);
    if (candidateCross > 0 && logScaleGain > 1e-9) break;
    slots -= 1;
  }

  const cellCross = (viewportCross - (slots - 1) * gap) / slots;
  const desiredLogFraction = Math.log(sourceCross) - Math.log(sourceLength)
    + Math.log(viewportLength) - Math.log(cellCross);
  if (desiredLogFraction >= 0) return whole;

  // 极长图增大单段覆盖范围来限制计划大小，不能简单截断数组而丢掉尾部内容。
  // 最小占比仍按每段 10% 重叠推导；只有超出 4096 段的极端输入才会缩小显示比例。
  const minimumFraction = 1 / (1 + (MAX_TILES - 1) * (1 - OVERLAP));
  const fraction = Math.max(minimumFraction, Math.exp(desiredLogFraction));
  const tileLength = sourceLength * fraction;
  const count = Math.max(2, Math.min(MAX_TILES,
    Math.ceil((1 / fraction - 1) / (1 - OVERLAP) - 1e-10) + 1,
  ));
  const step = tileLength * (1 - OVERLAP);
  const finalStart = sourceLength - tileLength;
  const tileWidth = vertical ? imageWidth : tileLength;
  const tileHeight = vertical ? tileLength : imageHeight;
  const scale = Math.min(cellCross / sourceCross, viewportLength / tileLength);
  const tiles = Array.from({ length: count }, (_, index): ImageReadingTile => {
    // 末段保持完整段长并贴齐原图尾端，避免不足一段的尾片被额外留白或漏读。
    const start = index === count - 1 ? finalStart : Math.min(index * step, finalStart);
    return { index, x: vertical ? 0 : start, y: vertical ? start : 0, width: tileWidth, height: tileHeight };
  });

  return {
    axis: vertical ? "vertical" : "horizontal",
    tiles,
    columns: vertical ? slots : 1,
    rows: vertical ? 1 : slots,
    perGroup: slots,
    groupCount: Math.ceil(count / slots),
    scale,
    overlap: OVERLAP,
    gap,
    tileWidth,
    tileHeight,
    displayWidth: tileWidth * scale,
    displayHeight: tileHeight * scale,
  };
}
