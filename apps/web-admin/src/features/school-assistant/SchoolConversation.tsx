import { conversationAccountKey } from './conversationState';
import type { SchoolConversationProps } from './conversationTypes';
import { useAccountConversation } from './useAccountConversation';
import { AccountConversationView } from './AccountConversationView';

import '../../pages/school-ai-chat.css';
import './school-conversation.css';

export type { SchoolConversationProps } from './conversationTypes';

function AccountConversation(props: SchoolConversationProps) {
  const model = useAccountConversation(props);
  return <AccountConversationView model={model} />;
}

/** 页面与模块抽屉复用同一对话；授权变化重建内存，避免旧账户的流和证据进入新范围。 */
export function SchoolConversation(props: SchoolConversationProps) {
  return <AccountConversation key={conversationAccountKey(props.user)} {...props} />;
}
