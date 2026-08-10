import { z } from "zod";

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

const scopeSchema = z.object({
  type: z.enum(["workspace", "strategy", "workstream", "project", "department", "document", "task"]),
  id: z.number().int().nonnegative(),
  label: z.string().max(300),
}).strict().superRefine((scope, context) => {
  const rootScope = scope.type === "workspace" || scope.type === "strategy";
  if ((rootScope && scope.id !== 0) || (!rootScope && scope.id <= 0)) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      message: "scope id does not match scope type",
      path: ["id"],
    });
  }
});

export const executeRunRequestSchema = z.object({
  run_id: z.string().min(1).max(160),
  workspace_id: z.number().int().positive(),
  user_id: z.number().int().positive(),
  participant_role: z.string().max(80).optional(),
  scope: scopeSchema,
  message: z.string().min(1).max(120_000),
  business_brief: z.string().max(120_000).optional(),
  model: z.string().min(1).max(160),
  previous_response_id: z.string().max(300).optional(),
  conversation_id: z.string().max(300).optional(),
  continuity_context: z.string().max(120_000).optional(),
  vector_store_id: z.string().max(300).optional(),
  run_token: z.string().min(1).max(8192),
  max_turns: z.number().int().min(1).max(120).optional(),
}).strict();

export const resumeRunRequestSchema = z.object({
  run_id: z.string().min(1).max(160),
  model: z.string().min(1).max(160),
  vector_store_id: z.string().max(300).optional(),
  state: z.string().min(1).max(1_900_000),
  run_token: z.string().min(1).max(8192),
  decisions: z.array(z.object({
    call_id: z.string().min(1).max(300),
    approved: z.boolean(),
  }).strict()).min(1).max(64),
  max_turns: z.number().int().min(1).max(120).optional(),
}).strict();
