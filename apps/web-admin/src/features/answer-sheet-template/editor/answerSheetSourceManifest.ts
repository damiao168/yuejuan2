import type { PDFDocument } from "pdf-lib";

const MANIFEST_KEY = "EduGradeAnswerSheetSourceManifest";
const MANIFEST_VERSION = 1;
const MAX_SOURCE_HASHES = 50;
const MAX_MANIFEST_LENGTH = 8192;

function normalizedHashes(values: readonly unknown[]): string[] {
  return [...new Set(values.filter((value): value is string => typeof value === "string" && /^[a-f\d]{64}$/i.test(value))
    .map(value => value.toLowerCase()))];
}

/** 清单只记录本批输入文件的字节指纹，不把 PDF 页面或其中的图片当作原始文件。 */
export async function writeAnswerSheetSourceHashes(document: PDFDocument, hashes: readonly string[]): Promise<void> {
  const values = normalizedHashes(hashes);
  if (!values.length || values.length > MAX_SOURCE_HASHES || values.length !== hashes.length) {
    throw new Error("答题卡原文件指纹清单无效，请重新选择文件。");
  }
  const { PDFHexString, PDFName } = await import("pdf-lib");
  // 不写文件名、时间或本地路径；改名和隔天上传仍能得到相同的整套文件指纹。
  document.catalog.set(PDFName.of(MANIFEST_KEY), PDFHexString.fromText(JSON.stringify({ version: MANIFEST_VERSION, hashes: values })));
}

/** 旧 PDF 或第三方 PDF 没有本清单时返回空；清单仅辅助重复提示，不提供权限或模板可信依据。 */
export async function readAnswerSheetSourceHashes(blob: Blob): Promise<string[]> {
  if (!blob.size || blob.size > 100 * 1024 * 1024) return [];
  try {
    const { PDFDocument, PDFHexString, PDFName, PDFString } = await import("pdf-lib");
    const document = await PDFDocument.load(await blob.arrayBuffer(), { updateMetadata: false });
    const value = document.catalog.lookup(PDFName.of(MANIFEST_KEY));
    if (!(value instanceof PDFHexString || value instanceof PDFString)) return [];
    const text = value.decodeText();
    if (text.length > MAX_MANIFEST_LENGTH) return [];
    const manifest: unknown = JSON.parse(text);
    if (!manifest || typeof manifest !== "object" || Array.isArray(manifest)) return [];
    const { version, hashes } = manifest as { version?: unknown; hashes?: unknown };
    if (version !== MANIFEST_VERSION || !Array.isArray(hashes) || hashes.length > MAX_SOURCE_HASHES) return [];
    // 外部 PDF 可能带有同名字段；只接受合法 SHA，不能拿任意字符串冒充已上传文件。
    return normalizedHashes(hashes);
  } catch {
    // 不可解析或加密的旧 PDF 不阻止其他文件上传，仍由整文件 SHA 执行既有去重。
    return [];
  }
}
