// Outbound HTTP helper: forwards the correlation ID and logs latency of every call.
// Use this instead of calling fetch() directly.

import { CORRELATION_HEADER, correlationId } from "./context.js";
import type { Logger } from "./logger.js";

export interface OutboundInit extends RequestInit {
  timeoutMs?: number;
}

export type OutboundRequest = (target: string, url: string, init?: OutboundInit) => Promise<Response>;

/** `target` is a short label for logs, e.g. "bmkg" or "aggregator". */
export function createHttpClient(logger: Logger): OutboundRequest {
  const log = logger.child({ component: "http.outbound" });

  return async (target, url, init = {}) => {
    const { timeoutMs, ...rest } = init;
    const method = rest.method ?? "GET";
    const headers = new Headers(rest.headers);
    headers.set(CORRELATION_HEADER, correlationId());
    const signal = rest.signal ?? (timeoutMs ? AbortSignal.timeout(timeoutMs) : undefined);

    const start = performance.now();
    const latency = () => Math.round((performance.now() - start) * 100) / 100;
    try {
      const res = await fetch(url, { ...rest, headers, signal });
      log.info({ target, method, url, status: res.status, latency_ms: latency() }, "outbound call");
      return res;
    } catch (err) {
      log.warn(
        { target, method, url, error: (err as Error).name, latency_ms: latency() },
        "outbound call failed",
      );
      throw err;
    }
  };
}
