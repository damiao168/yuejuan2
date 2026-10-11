import type { SchoolChatAttachment } from "../../api/schoolAiChat";

const MAX_TEXT_FILE_BYTES = 512 * 1024;
const MAX_PDF_FILE_BYTES = 10 * 1024 * 1024;
const MAX_ATTACHMENT_CHARACTERS = 60_000;
const supportedTextExtensions = new Set([
  "txt", "md", "csv", "json", "xml", "html", "css", "js", "jsx", "ts", "tsx",
  "py", "java", "go", "sql", "yaml", "yml", "log", "ini", "conf"
]);

function attachmentMediaType(file: File) {
  if (file.type) return file.type;
  const extension = file.name.split(".").pop()?.toLowerCase();
  if (extension === "pdf") return "application/pdf";
  if (extension === "md") return "text/markdown";
  if (extension === "csv") return "text/csv";
  if (extension === "json") return "application/json";
  return "text/plain";
}

function throwIfAborted(signal?: AbortSignal) {
  if (signal?.aborted) throw new DOMException("附件读取已取消", "AbortError");
}

export async function readChatAttachment(file: File, signal?: AbortSignal): Promise<SchoolChatAttachment> {
  throwIfAborted(signal);
  const extension = file.name.split(".").pop()?.toLowerCase() ?? "";
  const isPDF = extension === "pdf" || file.type === "application/pdf";
  const isText = file.type.startsWith("text/") || supportedTextExtensions.has(extension) || file.type === "application/json";
  if (!isPDF && !isText) throw new Error("仅支持 PDF、文本、Markdown、表格文本、代码和日志文件");
  if (file.size > (isPDF ? MAX_PDF_FILE_BYTES : MAX_TEXT_FILE_BYTES)) {
    throw new Error(isPDF ? "PDF 文件不能超过 10 MB" : "文本文件不能超过 512 KB");
  }
  // 文本上传不加载 PDF 运行时；动态模块返回时仍核对取消状态，避免离开账户后开始解析。
  const content = isPDF ? await (async () => {
    const { readConversationPDF } = await import("./conversationPdf");
    throwIfAborted(signal);
    return readConversationPDF(file, MAX_ATTACHMENT_CHARACTERS, signal);
  })() : (await file.text()).trim();
  throwIfAborted(signal);
  if (!content || content.includes("\x00")) throw new Error("文件为空或不是可读取的文本文件");
  if (Array.from(content).length > MAX_ATTACHMENT_CHARACTERS) throw new Error("文件文字不能超过 60000 字，请拆分后上传");
  return { name: file.name, media_type: attachmentMediaType(file), content, size: file.size };
}

