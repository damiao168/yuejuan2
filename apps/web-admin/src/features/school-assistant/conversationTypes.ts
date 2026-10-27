import type { SessionUser } from "../../auth/session";
import { type SchoolConversationContext } from "../../api/schoolAiChat";

export interface SchoolConversationProps {
  readonly user: SessionUser;
  readonly initialContext?: SchoolConversationContext;
  readonly compact?: boolean;
  readonly onNavigate?: (path: string) => void;
}
