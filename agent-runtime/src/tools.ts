import { tool, type FunctionTool, type RunContext } from "@openai/agents";
import { z, type AnyZodObject } from "zod";

import { callBusinessTool } from "./toolClient.js";
import type { AgentRunContext } from "./types.js";

const entityType = z.enum(["strategy", "department", "task", "document", "workspace_document"]);
const scopedEntityType = z.enum(["workspace", "strategy", "department", "task"]);

function executeTool(toolName: string) {
  return async (input: unknown, context?: RunContext<AgentRunContext>, details?: { toolCall: { callId: string } }) => {
    if (!context?.context.accessKey || !details?.toolCall.callId) {
      throw new Error("agent_tool_context_missing");
    }
    return callBusinessTool(
      context.context.accessKey,
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
  description: "Read the current ranked tasks, strategic alignment, blockers, and the strongest available next actions. Use when deciding what should be done next.",
  parameters: z.object({
    scope_type: scopedEntityType,
    scope_id: z.number().int().nonnegative(),
    limit: z.number().int().min(1).max(30).default(10),
  }),
  execute: executeTool("get_priority_view"),
});

const searchMetricCatalog = tool<any, AgentRunContext, unknown>({
  name: "search_metric_catalog",
  description: "Search the canonical REUP.goals standard metric catalog. When the user asks for standard, canonical, catalog, or reference metrics, call this tool and use only metrics returned by it. Also use it before inventing a custom metric. Results include the exact metric name, definition, formula, unit, and preferred direction.",
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
  current: metricNumber.nullable()
    .describe("The verified current baseline. Use null when no actual baseline was supplied or retrieved; never invent zero."),
  target: metricNumber
    .describe("The numeric target or numeric change magnitude."),
  unit: z.string().max(40).nullable()
    .describe("The target unit. It may clarify a relative change, for example '% reduction from verified baseline'."),
  better_direction: z.enum(["increase", "decrease", "range"]),
  target_date: z.string().max(20).nullable()
    .describe("An exact ISO calendar date only when explicitly supplied or reliably known. Otherwise use null; never infer a date from an entity title."),
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
    draft_key: z.string().min(1).max(80).nullable(),
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
    draft_key: z.string().min(1).max(80).nullable(),
    direction_id: z.number().int().positive().nullable().describe("Existing parent direction id. Use null only when parent_draft_key points to a direction proposed in this same package."),
    parent_draft_key: z.string().min(1).max(80).nullable().describe("Draft key of a direction proposed in this same package. Use null when direction_id is present."),
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
  "Prepare creation or an update of one executable task inside a business direction for explicit user confirmation.",
  z.object({
    existing_entity_id: z.number().int().positive().nullable(),
    draft_key: z.string().min(1).max(80).nullable(),
    direction_id: z.number().int().positive().nullable().describe("Existing business direction id. Use null only when direction_draft_key points to a direction proposed in this same package."),
    direction_draft_key: z.string().min(1).max(80).nullable().describe("Draft key of a business direction proposed in this same package. Use null when direction_id is present."),
    title: z.string().min(2).max(220),
    description: z.string().min(5).max(5000),
    why_now: z.string().min(3).max(1600),
    expected_result: z.string().min(3).max(1600),
    owner_user_id: z.number().int().positive(),
    due_date: z.string().regex(/^\d{4}-\d{2}-\d{2}$/).describe("Exact due date in ISO YYYY-MM-DD format."),
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
  "Prepare creation or an update of one business direction for explicit user confirmation. A direction is a durable responsibility boundary with a clear value, owner, team, and a small set of metrics.",
  z.object({
    existing_entity_id: z.number().int().positive().nullable(),
    draft_key: z.string().min(1).max(80).nullable(),
    name: z.string().min(2).max(120),
    description: z.string().min(3).max(3000),
    responsibility: z.string().min(3).max(2000),
    manager_user_id: z.number().int().positive(),
    member_user_ids: z.array(z.number().int().positive()).max(100).nullable(),
    kpis: z.array(metricSchema).min(1).max(3),
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

const proposeDocument = proposalTool(
  "propose_document",
  "Prepare a new editable workspace knowledge-base document for explicit user confirmation. Produce a complete, readable Markdown document rather than notes about what should be written.",
  z.object({
    title: z.string().min(2).max(240),
    content: z.string().min(1).max(200_000).describe("The complete document body in readable Markdown."),
    parent_document_id: z.number().int().positive().nullable(),
    linked_department_ids: z.array(z.number().int().positive()).max(30).nullable()
      .describe("IDs of business directions linked to this document. Public directions are stored as department records."),
  }),
);

const updateDocument = proposalTool(
  "update_document",
  "Prepare a full update of an existing editable workspace knowledge-base document for explicit user confirmation. Read the current document first, preserve useful content, and return the complete revised Markdown body.",
  z.object({
    document_id: z.number().int().positive(),
    base_version: z.number().int().positive().describe("The version returned by get_entity for this workspace document."),
    title: z.string().min(2).max(240),
    content: z.string().min(1).max(200_000).describe("The complete revised document body in readable Markdown."),
  }),
);

const completeTask = proposalTool(
  "complete_task",
  "Prepare completion of an in-progress task for explicit user confirmation. Use only after the user has described a concrete observable result clearly enough to be useful to the business; otherwise ask only for the material missing result information.",
  z.object({
    task_id: z.number().int().positive(),
    task_title: z.string().min(2).max(220),
    result: z.string().min(3).max(20_000).describe("A concise factual record of what was actually produced, changed, learned, or decided."),
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
    // Legacy workstream/project/risk/hypothesis proposal tools intentionally
    // stay defined above for old run decoding, but are not exposed to new runs.
    proposeTask,
    proposeDepartment,
    proposeStrategyReview,
    proposeDocument,
    updateDocument,
    completeTask,
  ] as FunctionTool<AgentRunContext, any, any>[];
}
