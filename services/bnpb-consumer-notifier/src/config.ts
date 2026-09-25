// Read from environment variables (see .env.example at repo root).

import { z } from "zod";

const Env = z.object({
  SERVICE_NAME: z.string().default("bnpb-consumer-notifier"),
  LOG_LEVEL: z.string().default("info"),
  PORT: z.coerce.number().int().default(8000),

  KAFKA_BOOTSTRAP_SERVERS: z.string().default("kafka:9092"),
  KAFKA_HAZARD_TOPIC: z.string().default("hazard-events"),
  // Each consumer has its own group so every one of them receives every event.
  KAFKA_GROUP_ID: z.string().default("notifier"),
});

const parsed = Env.safeParse(process.env);
if (!parsed.success) {
  console.error("Invalid configuration:\n" + z.prettifyError(parsed.error));
  process.exit(1);
}

export const config = parsed.data;
