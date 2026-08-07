import { setDefaultModelProvider, setTracingDisabled } from "@openai/agents-core";
import { OpenAIProvider } from "@openai/agents-openai";
import fetch from "node-fetch";
import OpenAI from "openai";
import { SocksProxyAgent } from "socks-proxy-agent";

let configured = false;

export function normalizeProxyURL(value: string | undefined): string {
  const normalized = (value || "").trim();
  return normalized.toLowerCase() === "direct" ? "" : normalized;
}

export function configureOpenAIProvider(): void {
  if (configured) return;
  const apiKey = (process.env.OPENAI_API_KEY || "").trim();
  if (!apiKey) throw new Error("OPENAI_API_KEY is required");

  const proxyURL = normalizeProxyURL(process.env.OPENAI_PROXY_URL);
  const proxyAgent = proxyURL ? new SocksProxyAgent(proxyURL) : undefined;
  const proxiedFetch: typeof globalThis.fetch = async (url, init) => {
    const options = {
      ...(init || {}),
      agent: proxyAgent,
    } as any;
    const response = await fetch(url as any, options);
    return response as unknown as globalThis.Response;
  };
  const client = new OpenAI({
    apiKey,
    fetch: proxiedFetch,
    maxRetries: 2,
    // The provider owns the lifecycle. A business analysis may legitimately
    // take much longer than a normal web request.
    timeout: 24 * 60 * 60 * 1000,
  });
  setDefaultModelProvider(new OpenAIProvider({ openAIClient: client as never }));
  // REUP.goals records tool stages and usage in PostgreSQL. SDK tracing would
  // duplicate sensitive business payloads in another observability store.
  setTracingDisabled(true);
  configured = true;
}
