import type { SessionUser } from "../auth/session";
import { SchoolConversation } from "../features/school-assistant/SchoolConversation";
import { useSchoolAssistantContext } from "../features/school-assistant/SchoolAssistantProvider";

export function SchoolAIChatPage({ user }: Readonly<{ user: SessionUser }>) {
  const assistant = useSchoolAssistantContext();
  return <SchoolConversation user={user} initialContext={assistant?.context} onNavigate={assistant?.navigate} />;
}
