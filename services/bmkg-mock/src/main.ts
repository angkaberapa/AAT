import { config } from "./config.js";
import { createLogger } from "./core/logger.js";
import { buildServer, start } from "./core/server.js";
import { registerRoutes } from "./routes.js";
import { seed, startGenerating } from "./data/store.js";

const logger = createLogger(config.SERVICE_NAME, config.LOG_LEVEL);
const app = buildServer(logger, config.SERVICE_NAME);

seed(20);
registerRoutes(app, { apiKey: config.BMKG_API_KEY, responseDelayMs: config.RESPONSE_DELAY_MS });
const generator = startGenerating(config.NEW_EVENT_INTERVAL_SECONDS, logger);

await start(app, config.PORT, async () => clearInterval(generator));
