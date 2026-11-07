import { createContext } from "react";
import type { AccountConversationModel } from "./useAccountConversation";

// 只共享当前授权范围内的内存控制器；消息正文和在途任务不通过此入口落盘。
export const ConversationSessionContext = createContext<{
  accountKey: string;
  model: AccountConversationModel;
  activate: () => void;
  registerView: () => () => void;
} | null>(null);
