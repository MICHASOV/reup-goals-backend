import { setDefaultModelProvider, setTracingDisabled } from "@openai/agents-core";
import { OpenAIProvider } from "@openai/agents-openai";
import OpenAI from "openai";
import { durationMilliseconds } from "./transport.js";

let configured = false;

export function configureOpenAIProvider(): void {
  if (configured) return;
  const apiKey = (process.env.OPENAI_API_KEY || "").trim();
  if (!apiKey) throw new Error("OPENAI_API_KEY is required");

  const client = new OpenAI({
    apiKey,
    maxRetries: 2,
    // Long analyses remain supported, while a broken network path can no
    // longer keep a background run alive forever.
    timeout: durationMilliseconds(process.env.AGENT_PROVIDER_TIMEOUT, 45 * 60 * 1000),
  });
  setDefaultModelProvider(new OpenAIProvider({ openAIClient: client as never }));
  // REUP.goals records tool stages and usage in PostgreSQL. SDK tracing would
  // duplicate sensitive business payloads in another observability store.
  setTracingDisabled(true);
  configured = true;
}
