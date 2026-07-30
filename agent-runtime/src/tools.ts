import { tool, type FunctionTool, type RunContext } from "@openai/agents";
import { z, type AnyZodObject } from "zod";

import { callBusinessTool } from "./toolClient.js";
import type { AgentRunContext } from "./types.js";

const entityType = z.enum(["strategy", "workstream", "project", "department", "task", "risk", "hypothesis", "document"]);
const scopedEntityType = z.enum(["workspace", "strategy", "workstream", "project", "department", "task"]);

function executeTool(toolName: string) {
  return async (input: unknown, context?: RunContext<AgentRunContext>, details?: { toolCall: { callId: string } }) => {
    if (!context?.context.runId || !details?.toolCall.callId) {
      throw new Error("agent_tool_context_missing");
    }
    return callBusinessTool(
      context.context.runId,
      toolName,
      details.toolCall.callId,
      (input && typeof input === "object" ? input : {}) as Record<string, unknown>,
    );
  };
}

const getBusinessBrief = tool<any, AgentRunContext, unknown>({
  name: "get_business_brief",
  description: "Read the current compact company context, business goal, active strategy, current focus, constraints, and important unknowns. Use when the supplied brief may be insufficient or when a decision depends on the latest company-level state.",
  parameters: z.object({ include_open_questions: z.boolean().default(true) }),
  execute: executeTool("get_business_brief"),
});

const listEntities = tool<any, AgentRunContext, unknown>({
  name: "list_entities",
  description: "List current business entities from the source of truth. Use before proposing a new entity, when comparing priorities, or when the user refers to an entity ambiguously.",
  parameters: z.object({
    entity_type: entityType,
    parent_type: scopedEntityType.nullable(),
    parent_id: z.number().int().positive().nullable(),
    status: z.string().max(40).nullable(),
    query: z.string().max(160).nullable(),
    limit: z.number().int().min(1).max(50).default(20),
  }),
  execute: executeTool("list_entities"),
});

const getEntity = tool<any, AgentRunContext, unknown>({
  name: "get_entity",
  description: "Read one current entity with its important fields, relationships, metrics, status, and dependencies.",
  parameters: z.object({
    entity_type: entityType,
    entity_id: z.number().int().positive(),
  }),
  execute: executeTool("get_entity"),
});

const listWorkspaceMembers = tool<any, AgentRunContext, unknown>({
  name: "list_workspace_members",
  description: "List active workspace participants and their roles. Use before assigning a manager, owner, or responsible employee when the person is not already unambiguous.",
  parameters: z.object({
    query: z.string().max(160).nullable(),
    limit: z.number().int().min(1).max(50).default(20),
  }),
  execute: executeTool("list_workspace_members"),
});

const getPriorityView = tool<any, AgentRunContext, unknown>({
  name: "get_priority_view",
  description: "Read the current ranked tasks, projects, strategic alignment, blockers, and the strongest available next actions. Use when deciding what should be done next.",
  parameters: z.object({
    scope_type: scopedEntityType,
    scope_id: z.number().int().nonnegative(),
    limit: z.number().int().min(1).max(30).default(10),
  }),
  execute: executeTool("get_priority_view"),
});

const searchMetricCatalog = tool<any, AgentRunContext, unknown>({
  name: "search_metric_catalog",
  description: "Find standard business metrics with definitions and formulas. Use before inventing a custom metric.",
  parameters: z.object({
    query: z.string().min(2).max(160),
    category: z.string().max(80).nullable(),
    limit: z.number().int().min(1).max(20).default(8),
  }),
  execute: executeTool("search_metric_catalog"),
});

const metricNumber = z.string()
  .regex(/^-?\d+(?:[.,]\d+)?$/, "Use one numeric value without units, spaces, ranges, or explanatory text")
  .max(40)
  .describe("A single numeric value as text. Put currency, percent, time period, and other units in the unit field.");

const metricSchema = z.object({
  name: z.string().min(1).max(160),
  current: metricNumber.nullable(),
  target: metricNumber,
  unit: z.string().max(40).nullable(),
  target_date: z.string().max(20).nullable(),
});

function proposalTool(name: string, description: string, parameters: AnyZodObject) {
  return tool<any, AgentRunContext, unknown>({
    name,
    description,
    parameters,
    needsApproval: true,
    execute: executeTool(name),
  });
}

const proposeDirection = proposalTool(
  "propose_direction",
  "Prepare creation or an update of one strategic or tactical direction for explicit user confirmation. Use only when a direction has become sufficiently distinct and useful as a managed business entity.",
  z.object({
    existing_entity_id: z.number().int().positive().nullable(),
    title: z.string().min(2).max(180),
    description: z.string().min(10).max(5000),
    expected_result: z.string().min(3).max(1600),
    ckp: z.string().min(3).max(1200),
    rationale: z.string().min(3).max(1200),
    metrics: z.array(metricSchema).min(1).max(3),
    lead_department_id: z.number().int().positive(),
    participant_department_ids: z.array(z.number().int().positive()).max(20).nullable(),
  }),
);

const proposeProject = proposalTool(
  "propose_project",
  "Prepare creation or an update of one project for explicit user confirmation. A project should have a distinct result and execution lifecycle, and normally belongs to a direction.",
  z.object({
    existing_entity_id: z.number().int().positive().nullable(),
    direction_id: z.number().int().positive(),
    title: z.string().min(2).max(180),
    description: z.string().min(10).max(5000),
    expected_result: z.string().min(3).max(1600),
    why_needed: z.string().min(3).max(1600),
    success_criteria: z.string().min(3).max(1600),
    failure_criteria: z.string().min(3).max(1600),
    expected_value: z.string().min(1).max(400),
    metric: metricSchema,
    department_id: z.number().int().positive(),
  }),
);

const proposeTask = proposalTool(
  "propose_task",
  "Prepare creation or an update of one executable task for explicit user confirmation. Use for a concrete human action, not for a broad project or an untested idea.",
  z.object({
    existing_entity_id: z.number().int().positive().nullable(),
    project_id: z.number().int().positive().nullable(),
    project_title: z.string().max(180).nullable(),
    title: z.string().min(2).max(220),
    description: z.string().min(5).max(5000),
    expected_result: z.string().min(3).max(1600),
    department_id: z.number().int().positive(),
    owner_user_id: z.number().int().positive().nullable(),
    owner_deferred: z.boolean().default(false),
    due_date: z.string().max(20).nullable(),
    due_date_deferred: z.boolean().default(false),
    blocker_task_ids: z.array(z.number().int().positive()).max(20).nullable(),
  }),
);

const proposeRisk = proposalTool(
  "propose_risk",
  "Prepare creation or an update of one material risk for explicit user confirmation. Use when the risk affects a managed direction or project and benefits from ownership, monitoring, or mitigation.",
  z.object({
    existing_entity_id: z.number().int().positive().nullable(),
    entity_type: z.enum(["tactical_plan", "workstream", "project"]),
    entity_id: z.number().int().positive(),
    title: z.string().min(2).max(180),
    description: z.string().min(5).max(3000),
    severity: z.enum(["low", "medium", "high", "critical"]),
    probability: z.enum(["low", "medium", "high"]),
    leading_indicators: z.string().max(1600).nullable(),
    mitigation_plan: z.string().max(2000).nullable(),
    contingency_plan: z.string().max(2000).nullable(),
    owner_user_id: z.number().int().positive().nullable(),
  }),
);

const proposeHypothesis = proposalTool(
  "propose_hypothesis",
  "Prepare creation or an update of one falsifiable business hypothesis for explicit user confirmation. Include what is expected, how it will be tested, and what evidence will change the decision.",
  z.object({
    existing_entity_id: z.number().int().positive().nullable(),
    entity_type: z.enum(["workstream", "project"]),
    entity_id: z.number().int().positive(),
    title: z.string().min(2).max(180),
    statement: z.string().min(5).max(2000),
    expected_effect: z.string().min(3).max(1600),
    test_method: z.string().min(3).max(2000),
    success_signal: z.string().max(1200).nullable(),
    owner_user_id: z.number().int().positive().nullable(),
  }),
);

const proposeDepartment = proposalTool(
  "propose_department",
  "Prepare creation or an update of one organizational department for explicit user confirmation. Use only when a durable responsibility boundary improves ownership and clarity.",
  z.object({
    existing_entity_id: z.number().int().positive().nullable(),
    name: z.string().min(2).max(120),
    description: z.string().min(3).max(3000),
    responsibility: z.string().min(3).max(2000),
    manager_user_id: z.number().int().positive().nullable(),
    member_user_ids: z.array(z.number().int().positive()).max(100).nullable(),
    kpis: z.array(metricSchema).max(3).nullable(),
  }),
);

const proposeStrategyReview = proposalTool(
  "propose_strategy_review",
  "Prepare the coherent strategy discussed with the user for independent quality review and document assembly. Use only in strategy scope when the strategic logic is sufficiently complete and the user explicitly agrees to fix or submit it. This does not activate the strategy by itself.",
  z.object({
    strategic_goal: z.string().min(10).max(2000),
    current_state: z.string().min(10).max(2400),
    target_state: z.string().min(10).max(2400),
    economic_engine: z.string().min(10).max(3000),
    key_metric: z.string().min(3).max(800),
    strategic_logic: z.string().min(20).max(5000),
    deliberate_non_priorities: z.string().min(3).max(2400),
    risks_and_assumptions: z.string().min(3).max(3000),
  }),
);

export function functionTools(): FunctionTool<AgentRunContext, any, any>[] {
  return [
    getBusinessBrief,
    listEntities,
    getEntity,
    listWorkspaceMembers,
    getPriorityView,
    searchMetricCatalog,
    proposeDirection,
    proposeProject,
    proposeTask,
    proposeRisk,
    proposeHypothesis,
    proposeDepartment,
    proposeStrategyReview,
  ] as FunctionTool<AgentRunContext, any, any>[];
}
