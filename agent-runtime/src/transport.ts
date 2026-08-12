export function isSecureInternalURL(value: string): boolean {
  let parsed: URL;
  try {
    parsed = new URL(value.trim());
  } catch {
    return false;
  }
  if (parsed.protocol === "https:") return true;
  if (parsed.protocol !== "http:") return false;
  return parsed.hostname === "127.0.0.1" || parsed.hostname === "localhost" || parsed.hostname === "::1";
}

export function durationMilliseconds(value: string | undefined, fallback: number): number {
  const normalized = (value || "").trim().toLowerCase();
  if (!normalized) return fallback;
  const match = normalized.match(/^(\d+)(ms|s|m|h)$/);
  if (!match) return fallback;
  const amount = Number(match[1]);
  const multiplier = match[2] === "ms" ? 1 : match[2] === "s" ? 1_000 : match[2] === "m" ? 60_000 : 3_600_000;
  const result = amount * multiplier;
  return Number.isSafeInteger(result) && result > 0 ? result : fallback;
}
