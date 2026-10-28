import type { OfflineAnswerMaterial, OfflineTaskPackage } from "../types";

export const MAX_OFFLINE_ANSWER_BYTES = 2 * 1024 * 1024;
export const MAX_OFFLINE_DRAFT_BYTES = 4 * 1024 * 1024;
const contentTypes = new Set(["image/png", "image/jpeg", "image/webp", "image/gif", "application/pdf"]);

async function readBytes(blob: Blob): Promise<ArrayBuffer> {
  if (typeof blob.arrayBuffer === "function") return blob.arrayBuffer();
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => reader.result instanceof ArrayBuffer ? resolve(reader.result) : reject(new Error("答卷材料读取失败"));
    reader.onerror = () => reject(new Error("答卷材料读取失败"));
    reader.readAsArrayBuffer(blob);
  });
}

function encode(bytes: Uint8Array) {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

async function hash(bytes: ArrayBuffer) {
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

export async function prepareOfflineAnswerMaterial(pkg: OfflineTaskPackage, blob: Blob, contentType: string, filename?: string): Promise<OfflineAnswerMaterial> {
  const type = contentType.split(";")[0].trim().toLowerCase();
  if (!contentTypes.has(type)) throw new Error("此答卷格式暂不能离线准备，请保持联网核对原件。");
  if (!blob.size || blob.size > MAX_OFFLINE_ANSWER_BYTES) throw new Error("答卷原件超过本机离线材料的 2 MB 上限，不能离线准备；联网查看仍可使用完整原件。");
  const bytes = await readBytes(blob);
  const material: OfflineAnswerMaterial = {
    version: 1, taskId: pkg.task.id, taskRevision: pkg.task.revision, questionId: pkg.task.question_id,
    answerSegmentId: pkg.task.answer_segment_id, contentType: type, filename,
    byteLength: bytes.byteLength, sha256: await hash(bytes), base64: encode(new Uint8Array(bytes))
  };
  // base64 会扩容，按 UTF-8 序列化后的真实大小核算，并为评分与待确认命令预留空间；绝不截断原件。
  if (new TextEncoder().encode(JSON.stringify({ ...pkg, answerMaterial: material })).byteLength > MAX_OFFLINE_DRAFT_BYTES - 512 * 1024) {
    throw new Error("任务包含答卷原件后超过离线容量，不能离线准备；评分草稿仍可单独保存。");
  }
  return material;
}

export async function restoreOfflineAnswerMaterial(pkg: OfflineTaskPackage): Promise<{ blob: Blob; contentType: string; filename?: string } | null> {
  const material = pkg.answerMaterial;
  if (!material) return null;
  if (material.version !== 1 || material.taskId !== pkg.task.id || material.taskRevision !== pkg.task.revision
    || material.questionId !== pkg.task.question_id || material.answerSegmentId !== pkg.task.answer_segment_id
    || !contentTypes.has(material.contentType) || !Number.isSafeInteger(material.byteLength)
    || material.byteLength <= 0 || material.byteLength > MAX_OFFLINE_ANSWER_BYTES
    || typeof material.base64 !== "string" || material.base64.length > Math.ceil(MAX_OFFLINE_ANSWER_BYTES / 3) * 4
    || !/^[0-9a-f]{64}$/.test(material.sha256)) throw new Error("本机答卷材料与任务身份、版本或格式不符，请联网恢复原件。");
  const binary = atob(material.base64);
  if (binary.length !== material.byteLength) throw new Error("本机答卷材料长度不符，请联网恢复原件。");
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  if (await hash(bytes.buffer) !== material.sha256) throw new Error("本机答卷材料校验失败，请联网恢复原件。");
  return { blob: new Blob([bytes], { type: material.contentType }), contentType: material.contentType, filename: material.filename };
}
