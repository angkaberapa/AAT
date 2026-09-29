// [SPEK] GET /seismic-events, GET /tsunami-warnings.
// [TIM] envelope {"data": [...]}, urut naik berdasarkan waktu, `since` ISO 8601 UTC
// (mis. 2026-09-25T10:00:00Z), invalid -> 400, absent -> semua data.

import type { FastifyInstance } from "fastify";
import { requireBmkgKey } from "./auth.js";
import { getSeismicEventsSince, getTsunamiWarningsSince } from "./data/store.js";
import type { SeismicEvent, TsunamiWarning } from "./data/types.js";

const ISO_UTC = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,3})?Z$/;

type SinceResult = { ok: true; value?: Date } | { ok: false };

function parseSince(raw: unknown): SinceResult {
  if (raw === undefined) return { ok: true };
  if (typeof raw !== "string" || !ISO_UTC.test(raw)) return { ok: false };

  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return { ok: false };
  return { ok: true, value: date };
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function seismicEventToApi(e: SeismicEvent) {
  return { ...e, occurred_at: e.occurred_at.toISOString() };
}

function tsunamiWarningToApi(w: TsunamiWarning) {
  return {
    ...w,
    estimated_arrival: w.estimated_arrival.toISOString(),
    issued_at: w.issued_at.toISOString(),
  };
}

export function registerRoutes(app: FastifyInstance, opts: { apiKey: string; responseDelayMs: number }): void {
  const auth = { preHandler: requireBmkgKey(opts.apiKey) };

  app.get<{ Querystring: { since?: string } }>("/seismic-events", auth, async (req, reply) => {
    const since = parseSince(req.query.since);
    if (!since.ok) {
      reply.code(400).send({ error: "invalid 'since' parameter, expected ISO 8601 UTC e.g. 2026-09-25T10:00:00Z" });
      return;
    }

    await sleep(opts.responseDelayMs);
    reply.send({ data: getSeismicEventsSince(since.value).map(seismicEventToApi) });
  });

  app.get<{ Querystring: { since?: string } }>("/tsunami-warnings", auth, async (req, reply) => {
    const since = parseSince(req.query.since);
    if (!since.ok) {
      reply.code(400).send({ error: "invalid 'since' parameter, expected ISO 8601 UTC e.g. 2026-09-25T10:00:00Z" });
      return;
    }

    await sleep(opts.responseDelayMs);
    reply.send({ data: getTsunamiWarningsSince(since.value).map(tsunamiWarningToApi) });
  });
}
