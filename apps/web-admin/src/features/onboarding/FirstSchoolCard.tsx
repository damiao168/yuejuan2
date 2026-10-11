import { useState } from "react";
import { Button } from "antd";
import { Building2, Plus } from "lucide-react";
import type { OnboardingCheck } from "../../api/onboarding";
import { CreateSchoolForm } from "../platform/schools/CreateSchoolForm";
import { SchoolCreatedReceipt, type SchoolDelivery } from "../platform/schools/SchoolCreatedReceipt";
import { OnboardingCheckCard } from "./OnboardingCheckCard";

export function FirstSchoolCard({ check, onCreated }: Readonly<{ check: OnboardingCheck; onCreated: () => Promise<void> | void }>) {
  const [open, setOpen] = useState(check.state === "action_required");
  const [delivery, setDelivery] = useState<SchoolDelivery | null>(null);
  return (
    <OnboardingCheckCard
      check={check}
      action={check.state === "ready" || open ? undefined : <Button type="primary" icon={<Plus size={16} />} onClick={() => setOpen(true)}>创建学校</Button>}
    >
      {/* 创建后重新检查可能变为 ready；交接回执仍保留到用户明确完成。 */}
      {delivery && <SchoolCreatedReceipt delivery={delivery} onDone={() => { setDelivery(null); setOpen(false); }} />}
      {!delivery && open && check.state !== "ready" && (
        <div className="onboarding-inline-form">
          <div className="onboarding-inline-form-heading"><Building2 size={18} /><strong>学校与管理员</strong><span>创建后立即重新检查启用状态</span></div>
          <CreateSchoolForm compact submitLabel="创建学校并继续" onCreated={async (_, receipt) => { setDelivery(receipt); await onCreated(); }} />
        </div>
      )}
    </OnboardingCheckCard>
  );
}
