export type ImageReadingAxis = "none" | "vertical" | "horizontal";

export interface ImageReadingLayoutInput {
  imageWidth: number;
  imageHeight: number;
  // 传入已扣除工具栏的画布大小，单位为 CSS px。
  viewportWidth: number;
  viewportHeight: number;
  gap?: number;
  // 每段标题占用的 CSS 高度；横向长图竖排时每一行都要扣除。
  captionHeight?: number;
  allowSegmentation?: boolean;
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
// 整图缩小不足四分之一时优先保留连续阅读；分段至少增大四成才抵消上下文切换。
const MIN_WHOLE_READING_SCALE = 0.75;
const MIN_READING_GAIN = 1.4;
const MAX_TILES = 4096;
const DEFAULT_GAP = 12;

const isPositiveFinite = (value: number) => Number.isFinite(value) && value > 0;

export function canSegmentAnswerImage(questionType: string | undefined, width: number, height: number): boolean {
  // 题型未知、选项条和短答案保留上下文；几何窗口不能判断选项或句子的语义边界。
  return ["short_answer", "calculation", "essay", "discussion", "short_constructed", "structured_steps", "extended_response"].includes(questionType ?? "")
    && isPositiveFinite(width) && isPositiveFinite(height) && height / width >= ASPECT_MISMATCH_THRESHOLD;
}

/**
 * 只计算阅读视口，不裁剪或改写图片；所有 tile 坐标均以原图左上角为原点，单位为原图 px。
 * vertical 沿原图纵向分段后横排，horizontal 沿横向分段后竖排；scale 统一用于段内显示。
 * 图片尺寸无效时返回空计划；画布尚未测量时保留整图，等有效画布尺寸到达后再规划。
 */
export function planImageReadingLayout(input: ImageReadingLayoutInput): ImageReadingLayout {
  const { imageWidth, imageHeight, viewportWidth, viewportHeight } = input;
  const gap = input.gap !== undefined && Number.isFinite(input.gap) && input.gap >= 0 ? input.gap : DEFAULT_GAP;
  const captionHeight = input.captionHeight !== undefined && Number.isFinite(input.captionHeight) && input.captionHeight >= 0 ? input.captionHeight : 0;
  const validImage = isPositiveFinite(imageWidth) && isPositiveFinite(imageHeight);
  const validViewport = isPositiveFinite(viewportWidth) && isPositiveFinite(viewportHeight);
  const wholeScale = validImage && validViewport
    ? Math.min(1, viewportWidth / imageWidth, viewportHeight / imageHeight)
    : 1;
  const whole: ImageReadingLayout = {
    axis: "none",
    tiles: validImage ? [{ index: 0, x: 0, y: 0, width: imageWidth, height: imageHeight }] : [],
    columns: 1,
    rows: 1,
    scale: wholeScale,
    overlap: OVERLAP,
    gap,
    tileWidth: validImage ? imageWidth : 0,
    tileHeight: validImage ? imageHeight : 0,
    displayWidth: validImage ? imageWidth * wholeScale : 0,
    displayHeight: validImage ? imageHeight * wholeScale : 0,
  };
  if (!validImage || !validViewport || input.allowSegmentation === false || wholeScale >= MIN_WHOLE_READING_SCALE) return whole;

  // 相同图片在横屏和竖屏下可能有不同读法，必须比较图片与画布的比例，而非固定判断图片长宽比。
  // 用对数比较，避免极长图的比例先溢出，再污染段数和循环边界。
  const logAspectMismatch = Math.log(imageHeight) - Math.log(imageWidth)
    - Math.log(viewportHeight) + Math.log(viewportWidth);
  if (Math.abs(logAspectMismatch) <= Math.log(ASPECT_MISMATCH_THRESHOLD) + 1e-9) return whole;

  const vertical = logAspectMismatch > 0;
  const sourceLength = vertical ? imageHeight : imageWidth;
  const sourceCross = vertical ? imageWidth : imageHeight;
  const viewportLength = vertical ? viewportHeight - captionHeight : viewportWidth;
  const viewportCross = vertical ? viewportWidth : viewportHeight;
  if (viewportLength <= 0) return whole;

  // 所有段必须同时放进一个画布。按完整覆盖反推段长，再选正文缩放最大的段数，不能先定列宽再分页。
  // n 段各重叠 10% 时，总覆盖长度 = 单段长度 × [1 + (n - 1) × 0.9]。
  let count = 1, scale = wholeScale;
  for (let candidate = 2; candidate <= MAX_TILES; candidate += 1) {
    const availableCross = viewportCross - (candidate - 1) * gap - (vertical ? 0 : candidate * captionHeight);
    if (availableCross <= 0) break;
    const crossScale = availableCross / candidate / sourceCross;
    const candidateLength = sourceLength / (1 + (candidate - 1) * (1 - OVERLAP));
    const lengthScale = viewportLength / candidateLength;
    const candidateScale = Math.min(1, crossScale, lengthScale);
    if (candidateScale > scale * (1 + 1e-9)) { count = candidate; scale = candidateScale; }
    // 横向可用宽度随段数递减、段长限制随段数放宽；交点之后不会出现更大的缩放比例。
    if (crossScale <= lengthScale) break;
  }
  // 仅轻微增大不值得打断连续作答；自动布局也不能放大原始像素制造虚假的清晰度。
  if (count === 1 || scale < wholeScale * MIN_READING_GAIN) return whole;
  const tileLength = sourceLength / (1 + (count - 1) * (1 - OVERLAP));
  const step = tileLength * (1 - OVERLAP);
  const finalStart = sourceLength - tileLength;
  const tileWidth = vertical ? imageWidth : tileLength;
  const tileHeight = vertical ? tileLength : imageHeight;
  const tiles = Array.from({ length: count }, (_, index): ImageReadingTile => {
    // 最后一段贴齐原图尾端，消除浮点累积误差；段长已由完整覆盖推导，不再额外制造尾片。
    const start = index === count - 1 ? finalStart : Math.min(index * step, finalStart);
    return { index, x: vertical ? 0 : start, y: vertical ? start : 0, width: tileWidth, height: tileHeight };
  });

  return {
    axis: vertical ? "vertical" : "horizontal",
    tiles,
    columns: vertical ? count : 1,
    rows: vertical ? 1 : count,
    scale,
    overlap: OVERLAP,
    gap,
    tileWidth,
    tileHeight,
    displayWidth: tileWidth * scale,
    displayHeight: tileHeight * scale,
  };
}
