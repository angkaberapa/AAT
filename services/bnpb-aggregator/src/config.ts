// Read from environment variables (see .env.example at repo root).

import { z } from "zod";

const Env = z.object({
  SERVICE_NAME: z.string().default("bnpb-aggregator"),
  LOG_LEVEL: z.string().default("info"),
  PORT: z.coerce.number().int().default(8000),

  // Upstream sources. Each has its own credential; they must never be swapped.
  BMKG_BASE_URL: z.url().default("http://bmkg-mock:8081"),
  BMKG_API_KEY: z.string().min(1),
  PVMBG_BASE_URL: z.url().default("http://pvmbg-mock:8082"),
  PVMBG_TOKEN: z.string().min(1),

  POLL_INTERVAL_SECONDS: z.coerce.number().positive().default(3),
  UPSTREAM_TIMEOUT_MS: z.coerce.number().int().positive().default(5000),

  // Canonical Store (owned exclusively by this service)
  MONGO_URI: z.string().min(1),
  MONGO_DB: z.string().default("bnpb"),

  // Message broker
  KAFKA_BOOTSTRAP_SERVERS: z.string().default("kafka:9092"),
  KAFKA_HAZARD_TOPIC: z.string().default("hazard-events"),
});

const parsed = Env.safeParse(process.env);
if (!parsed.success) {
  console.error("Invalid configuration:\n" + z.prettifyError(parsed.error));
  process.exit(1);
}

export const config = parsed.data;
