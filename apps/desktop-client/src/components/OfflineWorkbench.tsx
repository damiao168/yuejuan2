import type { OfflineWorkbenchProps } from './offlineWorkbench/types';
import { useOfflineWorkbench } from './offlineWorkbench/useOfflineWorkbench';
import { OfflineWorkbenchView } from './offlineWorkbench/OfflineWorkbenchView';

export type { OfflineLeaveGuard } from './offlineWorkbench/types';

export function OfflineWorkbench(props: Readonly<OfflineWorkbenchProps>) {
  const model = useOfflineWorkbench(props);
  return <OfflineWorkbenchView model={model} />;
}
