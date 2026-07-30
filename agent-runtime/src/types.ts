export type AgentScope = {
  type: "workspace" | "strategy" | "workstream" | "project" | "department" | "document" | "task";
  id: number;
  label: string;
};

export type AgentRunContext = {
  runId: string;
  workspaceId: number;
  userId: number;
  participantRole: string;
  scope: AgentScope;
  businessBrief: string;
};

export type AgentRuntimeEvent = {
  type:
    | "run_started"
    | "reasoning"
    | "knowledge_search"
    | "tool_started"
    | "tool_completed"
    | "proposal_ready"
    | "response_started"
    | "run_completed"
    | "run_failed";
  stage: string;
  title: string;
  detail?: string;
  tool_name?: string;
  tool_call_id?: string;
  created_at: string;
};

export type AgentInterruption = {
  call_id: string;
  tool_name: string;
  arguments: Record<string, unknown>;
};

export type ExecuteRunRequest = {
  run_id: string;
  workspace_id: number;
  user_id: number;
  participant_role?: string;
  scope: AgentScope;
  message: string;
  business_brief?: string;
  model: string;
  previous_response_id?: string;
  conversation_id?: string;
  continuity_context?: string;
  vector_store_id?: string;
  run_token: string;
  max_turns?: number;
};

export type ResumeRunRequest = {
  run_id: string;
  model: string;
  vector_store_id?: string;
  state: string;
  run_token: string;
  decisions: Array<{
    call_id: string;
    approved: boolean;
  }>;
  max_turns?: number;
};

export type AgentRuntimeResult = {
  status: "completed" | "waiting_approval";
  output: string;
  partial_output: string;
  previous_response_id?: string;
  state?: string;
  interruptions: AgentInterruption[];
  events: AgentRuntimeEvent[];
  usage: {
    requests: number;
    input_tokens: number;
    cached_input_tokens: number;
    output_tokens: number;
    total_tokens: number;
  };
};
