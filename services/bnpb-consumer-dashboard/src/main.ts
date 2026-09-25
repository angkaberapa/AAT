import { config } from "./config.js";
import { createLogger } from "./core/logger.js";
import { buildServer, start } from "./core/server.js";

const logger = createLogger(config.SERVICE_NAME, config.LOG_LEVEL);
// HTTP server only serves /health; the real work is the Kafka consumer loop.
const app = buildServer(logger, config.SERVICE_NAME);

// TODO(P5): start Kafka consumer (idempotent handling, one correlation ID per event)

await start(app, config.PORT);
