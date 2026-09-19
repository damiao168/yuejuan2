import { useState } from "react";
import { Button } from "antd";
import { Building2, Plus } from "lucide-react";
import type { OnboardingCheck } from "../../api/onboarding";
import { CreateSchoolForm } from "../platform/schools/CreateSchoolForm";
import { OnboardingCheckCard } from "./OnboardingCheckCard";

export function FirstSchoolCard({ check, onCreated }: { check: OnboardingCheck; onCreated: () => Promise<void> | void }) {
  const [open, setOpen] = useState(check.state === "action_required");
  return (
    <OnboardingCheckCard
      check={check}
      action={check.state === "ready" || open ? undefined : <Button type="primary" icon={<Plus size={16} />} onClick={() => setOpen(true)}>创建学校</Button>}
    >
      {open && check.state !== "ready" ? (
        <div className="onboarding-inline-form">
          <div className="onboarding-inline-form-heading"><Building2 size={18} /><strong>学校与管理员</strong><span>创建后立即重新检查启用状态</span></div>
          <CreateSchoolForm compact submitLabel="创建学校并继续" onCreated={async () => { setOpen(false); await onCreated(); }} />
        </div>
      ) : null}
    </OnboardingCheckCard>
  );
}
