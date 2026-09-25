import { config } from "./config.js";
import { createLogger } from "./core/logger.js";
import { buildServer, start } from "./core/server.js";

const logger = createLogger(config.SERVICE_NAME, config.LOG_LEVEL);
const app = buildServer(logger, config.SERVICE_NAME);

// TODO(P3): POST /token (client credentials -> access + refresh token with scope)
// TODO(P3): POST /token/refresh (rotate refresh token, reject old access token)
// TODO(P3): expose verification key for bnpb-api

await start(app, config.PORT);
