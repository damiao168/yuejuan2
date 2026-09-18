export type WorkflowModalState = { status: "closed" } | { status: "open" };
export type WorkflowModalAction = { type: "open" } | { type: "close" };

export const closedWorkflowModal: WorkflowModalState = { status: "closed" };

export function workflowModalReducer(_state: WorkflowModalState, action: WorkflowModalAction): WorkflowModalState {
  return action.type === "open" ? { status: "open" } : closedWorkflowModal;
}
