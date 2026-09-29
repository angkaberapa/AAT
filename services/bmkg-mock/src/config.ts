// Read from environment variables (see .env.example at repo root).

import { z } from "zod";

const Env = z.object({
  SERVICE_NAME: z.string().default("bmkg-mock"),
  LOG_LEVEL: z.string().default("info"),
  PORT: z.coerce.number().int().default(8081),

  BMKG_API_KEY: z.string().min(1),

  // [SPEK] response time rendah & konsisten: delay tetap, wajib di rentang 50-150ms.
  RESPONSE_DELAY_MS: z.coerce.number().int().min(50).max(150).default(100),

  // [SPEK] laju kemunculan event baru: minimal 1 event per 10 detik.
  NEW_EVENT_INTERVAL_SECONDS: z.coerce.number().positive().max(10).default(10),
});

const parsed = Env.safeParse(process.env);
if (!parsed.success) {
  console.error("Invalid configuration:\n" + z.prettifyError(parsed.error));
  process.exit(1);
}

export const config = parsed.data;
