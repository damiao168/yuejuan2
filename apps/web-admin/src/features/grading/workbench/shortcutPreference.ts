import { useSyncExternalStore } from "react";

const storageKey = "edugrade.grading-single-key-shortcuts";
const changeEvent = "edugrade:grading-shortcuts-change";
let sessionPreference: boolean | undefined;

function readPreference() {
  if (sessionPreference !== undefined) return sessionPreference;
  try { return window.localStorage.getItem(storageKey) !== "disabled"; }
  catch { return true; }
}

function subscribe(onChange: () => void) {
  const onStorage = (event: StorageEvent) => {
    if (event.key !== storageKey && event.key !== null) return;
    sessionPreference = undefined;
    onChange();
  };
  window.addEventListener(changeEvent, onChange);
  window.addEventListener("storage", onStorage);
  return () => {
    window.removeEventListener(changeEvent, onChange);
    window.removeEventListener("storage", onStorage);
  };
}

function setPreference(enabled: boolean) {
  // 存储被浏览器禁用时，当前会话仍可关闭快捷键；此偏好不包含账号或评分数据。
  try {
    window.localStorage.setItem(storageKey, enabled ? "enabled" : "disabled");
    sessionPreference = undefined;
  } catch { sessionPreference = enabled; }
  window.dispatchEvent(new Event(changeEvent));
}

export function useSingleKeyShortcuts() {
  const enabled = useSyncExternalStore(subscribe, readPreference, () => true);
  return { enabled, setEnabled: setPreference };
}
