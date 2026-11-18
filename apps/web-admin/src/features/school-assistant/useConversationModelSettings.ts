import { useEffect, useState } from "react";
import { getUserErrorMessage } from "../../api/client";
import { getSchoolChatModel, type SchoolChatModel } from "../../api/schoolAiChat";
import { getSchoolAssistantCapabilities } from "../../api/schoolAssistant";
import {
  readConversationModelPreference,
  saveConversationModelPreference,
} from "./conversationModelPreference";

export function useConversationModelSettings(
  modelPreferenceKey: string,
  publicComputer: boolean,
  enabled: boolean,
) {
  const [model, setModel] = useState<SchoolChatModel | null>(null);
  const [chatModels, setChatModels] = useState<SchoolChatModel[]>([]);
  const [selectedModelID, setSelectedModelID] = useState<string>();
  const [modelLoading, setModelLoading] = useState(true);
  const [modelError, setModelError] = useState("");
  const [webSearchCapability, setWebSearchCapability] = useState({
    available: false,
    message: "正在读取联网搜索状态",
  });

  useEffect(() => {
    // 其他栏目不预取模型；首次打开助手后保持控制器，后台增量才能继续归入原对话。
    if (!enabled) return;
    let mounted = true;
    getSchoolChatModel()
      .then((response) => {
        if (!mounted) return;
        const candidates = response.models ?? [];
        // 本机偏好只在新读取的可用名单内恢复；取消开放、停用和跨校授权变化都不能沿用旧编号。
        const preferredID = readConversationModelPreference(modelPreferenceKey, publicComputer);
        const selected =
          candidates.find((item) => item.id === preferredID && item.available) ?? response.model;
        setModel(selected);
        setChatModels(candidates);
        setSelectedModelID(selected.id);
        saveConversationModelPreference(
          modelPreferenceKey,
          publicComputer,
          selected.available ? selected.id : undefined,
        );
      })
      .catch((error: unknown) => {
        if (mounted) setModelError(getUserErrorMessage(error, "无法加载对话模型"));
      })
      .finally(() => {
        if (mounted) setModelLoading(false);
      });
    return () => {
      mounted = false;
    };
  }, [enabled, modelPreferenceKey, publicComputer]);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    getSchoolAssistantCapabilities(controller.signal)
      .then((response) => {
        if (controller.signal.aborted) return;
        setWebSearchCapability(response.web_search ?? { available: false, message: "联网搜索尚未配置" });
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setWebSearchCapability({ available: false, message: "暂时无法读取联网搜索状态" });
      });
    return () => controller.abort();
  }, [enabled]);

  function chooseModel(id: string) {
    const selected = chatModels.find((item) => item.id === id && item.available);
    if (!selected) return;
    setSelectedModelID(id);
    setModel(selected);
    setModelError("");
    saveConversationModelPreference(modelPreferenceKey, publicComputer, id);
  }

  return {
    model,
    setModel,
    chatModels,
    selectedModelID,
    modelLoading,
    modelError,
    chooseModel,
    webSearchCapability,
  };
}
