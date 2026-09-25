// Fastify instance with correlation ID handling, request logging with latency, and /health.

import Fastify, { LogController, type FastifyBaseLogger, type FastifyInstance } from "fastify";
import { CORRELATION_HEADER, newCorrelationId, withCorrelationId } from "./context.js";

export function buildServer(logger: FastifyBaseLogger, service: string): FastifyInstance {
  const app = Fastify({
    loggerInstance: logger,
    logController: new LogController({
      disableRequestLogging: true, // replaced by the single onResponse log below
      requestIdLogLabel: "correlation_id",
    }),
    requestIdHeader: CORRELATION_HEADER,
    genReqId: () => newCorrelationId(),
  });

  // Enter the async context so code that doesn't have `req` (e.g. the outbound
  // HTTP helper) can still read the correlation ID.
  app.addHook("onRequest", (req, reply, done) => {
    reply.header(CORRELATION_HEADER, req.id);
    withCorrelationId(req.id, done);
  });

  app.addHook("onResponse", async (req, reply) => {
    const path = req.url.split("?")[0];
    if (path === "/health") return; // probes are noisy
    req.log.info(
      {
        method: req.method,
        path,
        status: reply.statusCode,
        latency_ms: Math.round(reply.elapsedTime * 100) / 100,
      },
      "request handled",
    );
  });

  app.get("/health", async () => ({ status: "ok", service }));

  return app;
}

/** Listen and close gracefully on SIGTERM/SIGINT (docker compose stop). */
export async function start(
  app: FastifyInstance,
  port: number,
  onClose: () => Promise<void> = async () => {},
): Promise<void> {
  const shutdown = async (signal: string) => {
    app.log.info({ signal }, "service stopping");
    await app.close();
    await onClose();
    process.exit(0);
  };
  process.once("SIGTERM", shutdown);
  process.once("SIGINT", shutdown);

  await app.listen({ host: "0.0.0.0", port });
  app.log.info({ port }, "service started");
}
