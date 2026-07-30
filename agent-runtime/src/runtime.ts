import {
  Agent,
  RunState,
  fileSearchTool,
  run,
  type RunItemStreamEvent,
  type RunStreamEvent,
  type RunToolApprovalItem,
  type StreamedRunResult,
} from "@openai/agents";

import { buildInstructions } from "./prompt.js";
import { clearRunAccess, publishEvent, setRunAccess } from "./toolClient.js";
import { functionTools } from "./tools.js";
import type {
  AgentInterruption,
  AgentRunContext,
  AgentRuntimeEvent,
  AgentRuntimeResult,
  ExecuteRunRequest,
  ResumeRunRequest,
} from "./types.js";

const toolTitles: Record<string, { started: string; completed: string; type: AgentRuntimeEvent["type"] }> = {
  get_business_brief: { started: "Сверяю актуальный контекст компании", completed: "Контекст компании получен", type: "tool_started" },
  list_entities: { started: "Проверяю существующую структуру", completed: "Структура бизнеса проверена", type: "tool_started" },
  get_entity: { started: "Изучаю выбранный объект", completed: "Данные объекта получены", type: "tool_started" },
  list_workspace_members: { started: "Проверяю команду и роли", completed: "Участники workspace проверены", type: "tool_started" },
  get_priority_view: { started: "Сопоставляю текущие приоритеты", completed: "Приоритеты сопоставлены", type: "tool_started" },
  search_metric_catalog: { started: "Ищу подходящие измеримые метрики", completed: "Метрики найдены", type: "tool_started" },
};

function now(): string {
  return new Date().toISOString();
}

type AdvisorAgent = Agent<AgentRunContext, any>;
type AdvisorStream = StreamedRunResult<AgentRunContext, any>;

function createAgent(model: string, vectorStoreId?: string): AdvisorAgent {
  const tools = [...functionTools()];
  if (vectorStoreId?.trim()) {
    tools.push(fileSearchTool(vectorStoreId.trim(), {
      maxNumResults: 8,
      includeSearchResults: false,
    }) as any);
  }
  return new Agent<AgentRunContext>({
    name: "REUP.goals Executive Advisor",
    instructions: (context) => buildInstructions(context.context),
    model,
    modelSettings: {
      reasoning: { effort: "medium", summary: "auto" },
      text: { verbosity: "medium" },
      parallelToolCalls: true,
    },
    tools,
  });
}

function interruptionData(item: RunToolApprovalItem): AgentInterruption {
  const raw = item.rawItem as { callId?: string; id?: string; name?: string; arguments?: string };
  let args: Record<string, unknown> = {};
  try {
    args = raw.arguments ? JSON.parse(raw.arguments) as Record<string, unknown> : {};
  } catch {
    args = {};
  }
  return {
    call_id: raw.callId || raw.id || "",
    tool_name: item.name || raw.name || "",
    arguments: args,
  };
}

function itemTool(event: RunItemStreamEvent): { name: string; callId: string } {
  const item = event.item as unknown as {
    name?: string;
    rawItem?: { name?: string; callId?: string; id?: string };
  };
  return {
    name: item.name || item.rawItem?.name || "",
    callId: item.rawItem?.callId || item.rawItem?.id || "",
  };
}

async function consumeStream(
  runId: string,
  stream: AdvisorStream,
  events: AgentRuntimeEvent[],
): Promise<string> {
  let partialOutput = "";
  const emit = async (event: AgentRuntimeEvent) => {
    events.push(event);
    await publishEvent(runId, event);
  };

  for await (const event of stream as AsyncIterable<RunStreamEvent>) {
    if (event.type === "raw_model_stream_event") {
      const raw = event.data as unknown as { type?: string; delta?: string };
      if (raw.type === "response.output_text.delta" && typeof raw.delta === "string") {
        partialOutput += raw.delta;
      }
      continue;
    }
    if (event.type !== "run_item_stream_event") continue;
    const tool = itemTool(event);
    if (event.name === "reasoning_item_created") {
      await emit({
        type: "reasoning",
        stage: "analysis",
        title: "Сопоставляю данные, ограничения и варианты",
        created_at: now(),
      });
    } else if (event.name === "tool_called") {
      const label = toolTitles[tool.name]?.started || (tool.name === "file_search" ? "Ищу подтверждения в документах" : "Выполняю проверку данных");
      await emit({
        type: tool.name === "file_search" ? "knowledge_search" : "tool_started",
        stage: "tool",
        title: label,
        tool_name: tool.name,
        tool_call_id: tool.callId,
        created_at: now(),
      });
    } else if (event.name === "tool_output") {
      await emit({
        type: "tool_completed",
        stage: "tool",
        title: toolTitles[tool.name]?.completed || "Проверка завершена",
        tool_name: tool.name,
        tool_call_id: tool.callId,
        created_at: now(),
      });
    } else if (event.name === "tool_approval_requested") {
      await emit({
        type: "proposal_ready",
        stage: "approval",
        title: "Подготовлено изменение для подтверждения",
        detail: "Советник ничего не применит без вашего решения.",
        tool_name: tool.name,
        tool_call_id: tool.callId,
        created_at: now(),
      });
    } else if (event.name === "message_output_created") {
      await emit({
        type: "response_started",
        stage: "response",
        title: "Формирую итоговый ответ",
        created_at: now(),
      });
    }
  }
  await stream.completed;
  if (stream.error) throw stream.error;
  return partialOutput;
}

function usageResult(stream: AdvisorStream) {
  const usage = stream.state.usage;
  const cachedInputTokens = (usage.requestUsageEntries ?? []).reduce((total, entry) => {
    const details = entry.inputTokensDetails || {};
    const cached = Number(details.cached_tokens ?? details.cachedTokens ?? 0);
    return total + (Number.isFinite(cached) && cached > 0 ? cached : 0);
  }, 0);
  return {
    requests: usage.requests,
    input_tokens: usage.inputTokens,
    cached_input_tokens: cachedInputTokens,
    output_tokens: usage.outputTokens,
    total_tokens: usage.totalTokens,
  };
}

async function finalize(
  runId: string,
  stream: AdvisorStream,
  partialOutput: string,
  events: AgentRuntimeEvent[],
): Promise<AgentRuntimeResult> {
  const interruptions = stream.interruptions.map(interruptionData).filter((item) => item.call_id && item.tool_name);
  if (interruptions.length) {
    return {
      status: "waiting_approval",
      output: "",
      partial_output: partialOutput,
      previous_response_id: stream.lastResponseId,
      state: stream.state.toString(),
      interruptions,
      events,
      usage: usageResult(stream),
    };
  }
  const completedEvent: AgentRuntimeEvent = {
    type: "run_completed",
    stage: "completed",
    title: "Ответ готов",
    created_at: now(),
  };
  events.push(completedEvent);
  await publishEvent(runId, completedEvent);
  return {
    status: "completed",
    output: String(stream.finalOutput || partialOutput || "").trim(),
    partial_output: partialOutput,
    previous_response_id: stream.lastResponseId,
    interruptions: [],
    events,
    usage: usageResult(stream),
  };
}

export async function executeRun(request: ExecuteRunRequest): Promise<AgentRuntimeResult> {
  setRunAccess(request.run_id, request.run_token);
  const events: AgentRuntimeEvent[] = [];
  try {
    const started: AgentRuntimeEvent = {
      type: "run_started",
      stage: "starting",
      title: "Изучаю запрос и актуальный контекст",
      created_at: now(),
    };
    events.push(started);
    await publishEvent(request.run_id, started);
    const context: AgentRunContext = {
      runId: request.run_id,
      workspaceId: request.workspace_id,
      userId: request.user_id,
      participantRole: request.participant_role || "member",
      scope: request.scope,
      businessBrief: request.business_brief || "",
    };
    const agent = createAgent(request.model, request.vector_store_id);
    const stream = await run(agent, request.message, {
      context,
      stream: true,
      maxTurns: request.max_turns || 12,
      previousResponseId: request.previous_response_id || undefined,
      conversationId: request.conversation_id || undefined,
    });
    const partialOutput = await consumeStream(request.run_id, stream, events);
    return await finalize(request.run_id, stream, partialOutput, events);
  } catch (error) {
    const failed: AgentRuntimeEvent = {
      type: "run_failed",
      stage: "failed",
      title: "Не удалось завершить запрос",
      detail: "Запрос сохранён. Можно повторить его после восстановления соединения.",
      created_at: now(),
    };
    events.push(failed);
    await publishEvent(request.run_id, failed);
    throw error;
  } finally {
    clearRunAccess(request.run_id);
  }
}

export async function resumeRun(request: ResumeRunRequest): Promise<AgentRuntimeResult> {
  setRunAccess(request.run_id, request.run_token);
  const events: AgentRuntimeEvent[] = [];
  try {
    const agent = createAgent(request.model, request.vector_store_id);
    const state = await RunState.fromString<AgentRunContext, AdvisorAgent>(agent, request.state);
    const decisions = new Map(request.decisions.map((decision) => [decision.call_id, decision.approved]));
    for (const interruption of state.getInterruptions() as RunToolApprovalItem[]) {
      const data = interruptionData(interruption);
      const approved = decisions.get(data.call_id);
      if (approved === true) state.approve(interruption);
      if (approved === false) state.reject(interruption);
    }
    const stream = await run(agent, state, {
      stream: true,
      maxTurns: request.max_turns || 12,
    });
    const partialOutput = await consumeStream(request.run_id, stream, events);
    return await finalize(request.run_id, stream, partialOutput, events);
  } finally {
    clearRunAccess(request.run_id);
  }
}
