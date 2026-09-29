// In-memory store: seed + background generator + query helpers.
// bmkg-mock is a passive read-only source (spec forbids it pushing to BNPB), so a
// process-local array is enough — there is nothing here that needs to survive a restart.

import type { Logger } from "../core/logger.js";
import { newCorrelationId, withCorrelationId } from "../core/context.js";
import { generateSeismicEvent, generateTsunamiWarning, type MagnitudeBand } from "./generator.js";
import type { SeismicEvent, ThreatLevel, TsunamiWarning } from "./types.js";

const seismicEvents: SeismicEvent[] = [];
const tsunamiWarnings: TsunamiWarning[] = [];
let eventSequence = 0;
let warningSequence = 0;

/** Guarantees every severity branch (magnitude band x threat level) shows up in the seed,
 *  instead of leaving that to chance across just 20 random draws. */
const GUARANTEED_SEEDS: Array<{ band: MagnitudeBand; tsunami?: ThreatLevel }> = [
  { band: "low" },
  { band: "mid" },
  { band: "high" },
  { band: "mid", tsunami: "Waspada" },
  { band: "high", tsunami: "Siaga" },
  { band: "high", tsunami: "Awas" },
];

function addEvent(occurredAt: Date, overrides: { band?: MagnitudeBand; tsunami?: ThreatLevel } = {}): void {
  const event = generateSeismicEvent(++eventSequence, occurredAt, {
    band: overrides.band,
    potentialTsunami: overrides.tsunami !== undefined ? true : undefined,
  });
  seismicEvents.push(event);

  if (event.potential_tsunami) {
    tsunamiWarnings.push(generateTsunamiWarning(++warningSequence, event, { threatLevel: overrides.tsunami }));
  }
}

/** Spread `count` historical events over the past 2 hours, oldest first. */
export function seed(count: number): void {
  const now = Date.now();
  const spanMs = 2 * 60 * 60 * 1000;
  const stepMs = spanMs / count;

  for (let i = 0; i < count; i++) {
    const occurredAt = new Date(now - spanMs + i * stepMs);
    const guaranteed = GUARANTEED_SEEDS[i];
    addEvent(occurredAt, guaranteed ? { band: guaranteed.band, tsunami: guaranteed.tsunami } : {});
  }
}

/** One new event per cycle, wrapped in its own correlation ID (it's not a request). */
export function startGenerating(intervalSeconds: number, logger: Logger): NodeJS.Timeout {
  return setInterval(() => {
    withCorrelationId(newCorrelationId(), () => {
      addEvent(new Date());
      const event = seismicEvents[seismicEvents.length - 1]!;
      logger.info(
        { event_id: event.event_id, magnitude: event.magnitude, potential_tsunami: event.potential_tsunami },
        "new seismic event generated",
      );
    });
  }, intervalSeconds * 1000);
}

export function getSeismicEventsSince(since?: Date): SeismicEvent[] {
  return seismicEvents
    .filter((e) => since === undefined || e.occurred_at.getTime() > since.getTime())
    .sort((a, b) => a.occurred_at.getTime() - b.occurred_at.getTime());
}

export function getTsunamiWarningsSince(since?: Date): TsunamiWarning[] {
  return tsunamiWarnings
    .filter((w) => since === undefined || w.issued_at.getTime() > since.getTime())
    .sort((a, b) => a.issued_at.getTime() - b.issued_at.getTime());
}
