import { useRef, useSyncExternalStore } from "react";
import { ModelOperationQueue } from "../lib/modelOperationQueue";

export function useModelOperations() {
  const queueRef = useRef<ModelOperationQueue | null>(null);
  if (!queueRef.current) queueRef.current = new ModelOperationQueue(3);
  const queue = queueRef.current;
  const operations = useSyncExternalStore(queue.subscribe, queue.getSnapshot, queue.getSnapshot);
  return { queue, operations };
}
