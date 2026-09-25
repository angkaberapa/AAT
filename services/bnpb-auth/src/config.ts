// Read from environment variables (see .env.example at repo root).

import { z } from "zod";

const Env = z.object({
  SERVICE_NAME: z.string().default("bnpb-auth"),
  LOG_LEVEL: z.string().default("info"),
  PORT: z.coerce.number().int().default(8000),

  JWT_SIGNING_KEY: z.string().min(32, "JWT_SIGNING_KEY must be at least 32 characters"),
  ACCESS_TOKEN_TTL_SECONDS: z.coerce.number().int().positive().default(60),
  REFRESH_TOKEN_TTL_SECONDS: z.coerce.number().int().positive().default(3600),

  // Three distinct downstream identities (not one account with a role flag).
  CLIENT_MEDIA_ID: z.string().min(1),
  CLIENT_MEDIA_SECRET: z.string().min(1),
  CLIENT_FIELD_ID: z.string().min(1),
  CLIENT_FIELD_SECRET: z.string().min(1),
  CLIENT_OPS_ID: z.string().min(1),
  CLIENT_OPS_SECRET: z.string().min(1),
});

const parsed = Env.safeParse(process.env);
if (!parsed.success) {
  console.error("Invalid configuration:\n" + z.prettifyError(parsed.error));
  process.exit(1);
}

export const config = parsed.data;
