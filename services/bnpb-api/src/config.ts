// Read from environment variables (see .env.example at repo root).

import { z } from "zod";

const Env = z.object({
  SERVICE_NAME: z.string().default("bnpb-api"),
  LOG_LEVEL: z.string().default("info"),
  PORT: z.coerce.number().int().default(8080),

  // Canonical data is read ONLY through the aggregator's API, never from Mongo.
  AGGREGATOR_BASE_URL: z.url().default("http://bnpb-aggregator:8000"),
  // Used to fetch the token verification key from the auth service.
  AUTH_BASE_URL: z.url().default("http://bnpb-auth:8000"),

  UPSTREAM_TIMEOUT_MS: z.coerce.number().int().positive().default(2000),
});

const parsed = Env.safeParse(process.env);
if (!parsed.success) {
  console.error("Invalid configuration:\n" + z.prettifyError(parsed.error));
  process.exit(1);
}

export const config = parsed.data;
