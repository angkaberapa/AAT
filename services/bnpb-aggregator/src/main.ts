import { config } from "./config.js";
import { createLogger } from "./core/logger.js";
import { buildServer, start } from "./core/server.js";

const logger = createLogger(config.SERVICE_NAME, config.LOG_LEVEL);
const app = buildServer(logger, config.SERVICE_NAME);

// TODO(P4): connect to MongoDB (Canonical Store)
// TODO(P5): start Kafka producer
// TODO(P2): start BMKG & PVMBG pollers as independent background loops
//           (one correlation ID per poll cycle, see core/context.ts)

await start(app, config.PORT);
