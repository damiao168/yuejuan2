export type ReadingSize = "standard" | "large";
const storageKey = "edugrade.reading-size";

export function readReadingSize(): ReadingSize {
  try { return window.localStorage.getItem(storageKey) === "large" ? "large" : "standard"; }
  catch { return "standard"; }
}

export function applyReadingSize(size: ReadingSize) {
  document.documentElement.dataset.readingSize = size;
  try { window.localStorage.setItem(storageKey, size); } catch { /* 当前会话中的偏好设置仍然有效。 */ }
}
