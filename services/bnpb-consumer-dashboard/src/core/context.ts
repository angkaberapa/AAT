// Correlation ID bound to the current async context (request or background job).
//
// Everything in src/core/ is copied (not shared) into every BNPB service on purpose:
// services must not share libraries, and this code has no business logic.
// If you change it, change it in every service.

import { AsyncLocalStorage } from "node:async_hooks";
import { randomUUID } from "node:crypto";

export const CORRELATION_HEADER = "x-correlation-id";

const storage = new AsyncLocalStorage<{ correlationId: string }>();

export function correlationId(): string {
  return storage.getStore()?.correlationId ?? "-";
}

export function newCorrelationId(): string {
  return randomUUID();
}

/** Run fn with the given correlation ID. Use one new ID per background cycle (e.g. a poll). */
export function withCorrelationId<T>(id: string, fn: () => T): T {
  return storage.run({ correlationId: id }, fn);
}
