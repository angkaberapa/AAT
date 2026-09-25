import { config } from "./config.js";
import { createHttpClient } from "./core/http.js";
import { createLogger } from "./core/logger.js";
import { buildServer, start } from "./core/server.js";

const logger = createLogger(config.SERVICE_NAME, config.LOG_LEVEL);
const app = buildServer(logger, config.SERVICE_NAME);
// Outbound calls to aggregator/auth must go through this (correlation ID + latency log).
const http = createHttpClient(logger);

// TODO(P2): rate limiter / concurrency limit, stale-data response on PVMBG outage
// TODO(P3): verify JWT, enforce scope-based field filtering (media = Ringkasan only)

await start(app, config.PORT);
