import { useContext, useEffect, type ContextType } from 'react';
import { conversationAccountKey } from './conversationState';
import { ConversationSessionContext } from './ConversationSessionContext';
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

function SharedAccountConversation({ session, ...props }: SchoolConversationProps & {
  session: NonNullable<ContextType<typeof ConversationSessionContext>>;
}) {
  const { activate, registerView } = session;
  useEffect(() => { activate(); return registerView(); }, [activate, registerView]);
  return <AccountConversationView model={{ ...session.model, compact: props.compact ?? false, onNavigate: props.onNavigate ?? session.model.onNavigate }} />;
}

/** 页面和抽屉仅挂载视图；账户布局继续拥有进行中的请求，授权变化时才销毁整组内存。 */
export function SchoolConversation(props: SchoolConversationProps) {
  const session = useContext(ConversationSessionContext);
  if (session) {
    // 路由切换期间的旧视图不能使用新账户的控制器，也不能回退读取旧账户缓存。
    if (session.accountKey !== conversationAccountKey(props.user)) return null;
    return <SharedAccountConversation {...props} session={session} />;
  }
  return <AccountConversation key={conversationAccountKey(props.user)} {...props} />;
}
