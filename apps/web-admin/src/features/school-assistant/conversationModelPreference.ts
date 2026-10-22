export function conversationModelPreferenceKey(accountKey: string) {
  return `edugrade.ai-chat.model.v1:${accountKey}`;
}

export function readConversationModelPreference(key: string, publicComputer: boolean): string | undefined {
  if (publicComputer) return undefined;
  try { return window.localStorage.getItem(key) || undefined; } catch { return undefined; }
}

export function saveConversationModelPreference(key: string, publicComputer: boolean, modelID?: string) {
  if (publicComputer) return;
  try {
    if (modelID) window.localStorage.setItem(key, modelID);
    else window.localStorage.removeItem(key);
  } catch {
    // 偏好存储不可用时仍允许本页切换；实际可用范围始终由服务端重新读取。
  }
}
