import { useEffect, useRef } from "react";
import type { SessionUser } from "../../auth/session";
import { useOnboardingReadiness } from "./queries";

export function OnboardingGate({ user, currentPath, navigate }: { user: SessionUser; currentPath: string; navigate: (path: string) => void }) {
  const isAdmin = user.roles.some((role) => ["platform_admin", "tenant_admin", "school_admin"].includes(role));
  const readiness = useOnboardingReadiness(isAdmin);
  const handled = useRef(false);

  useEffect(() => {
    if (handled.current || currentPath !== "/dashboard" || !readiness.data) return;
    handled.current = true;
    if (readiness.data.scope === "platform") {
      const firstSchool = readiness.data.checks.find((check) => check.key === "first_school");
      if (firstSchool?.state === "action_required") navigate("/platform/getting-started");
      return;
    }
    if (readiness.data.checks.some((check) => check.severity === "blocking" && check.state === "action_required")) {
      navigate("/organization/setup");
    }
  }, [currentPath, navigate, readiness.data]);

  return null;
}
