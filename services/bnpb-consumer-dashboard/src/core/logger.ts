// Structured JSON logger (pino). Every line carries `service` and `correlation_id`.

import { pino, stdTimeFunctions, type Logger } from "pino";
import { correlationId } from "./context.js";

export type { Logger };

export function createLogger(service: string, level: string): Logger {
  return pino({
    level: level.toLowerCase(),
    base: { service },
    messageKey: "msg",
    timestamp: stdTimeFunctions.isoTime,
    formatters: { level: (label) => ({ level: label }) },
    // Fastify's per-request child logger already binds correlation_id; only add it
    // for logs outside a request (background jobs) to avoid a duplicate key.
    mixin: (_obj, _level, logger) =>
      "correlation_id" in logger.bindings() ? {} : { correlation_id: correlationId() },
  });
}
