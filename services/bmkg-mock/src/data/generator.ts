// Random SeismicEvent / TsunamiWarning generation. Magnitude bands and tsunami odds
// are tuned so all three canonical severity branches (NORMAL/WASPADA/SIAGA/AWAS,
// see M1 spec mapping rules) show up across a run, not left to chance alone.

import { REGIONS } from "./regions.js";
import type { SeismicEvent, ThreatLevel, TsunamiWarning } from "./types.js";

function randomFloat(min: number, max: number): number {
  return min + Math.random() * (max - min);
}

function randomInt(min: number, max: number): number {
  return Math.floor(randomFloat(min, max + 1));
}

function pick<T>(items: readonly T[]): T {
  return items[randomInt(0, items.length - 1)]!;
}

function weightedPick<T>(options: Array<[T, number]>): T {
  const total = options.reduce((sum, [, weight]) => sum + weight, 0);
  let roll = Math.random() * total;
  for (const [value, weight] of options) {
    roll -= weight;
    if (roll <= 0) return value;
  }
  return options[options.length - 1]![0];
}

export type MagnitudeBand = "low" | "mid" | "high";

function randomMagnitude(band: MagnitudeBand): number {
  const ranges: Record<MagnitudeBand, [number, number]> = {
    low: [3.0, 4.9],
    mid: [5.0, 6.4],
    high: [6.5, 8.0],
  };
  const [min, max] = ranges[band];
  return Math.round(randomFloat(min, max) * 10) / 10;
}

function isTsunamiPotential(band: MagnitudeBand): boolean {
  const chance: Record<MagnitudeBand, number> = { low: 0, mid: 0.15, high: 0.65 };
  return Math.random() < chance[band];
}

export interface SeismicEventOverrides {
  band?: MagnitudeBand;
  potentialTsunami?: boolean;
}

export function generateSeismicEvent(
  sequence: number,
  occurredAt: Date,
  overrides: SeismicEventOverrides = {},
): SeismicEvent {
  const band =
    overrides.band ??
    weightedPick<MagnitudeBand>([
      ["low", 55],
      ["mid", 30],
      ["high", 15],
    ]);
  const region = pick(REGIONS);

  return {
    event_id: `BMKG-EQ-${String(sequence).padStart(6, "0")}`,
    magnitude: randomMagnitude(band),
    depth_km: Math.round(randomFloat(5, 300) * 10) / 10,
    epicenter_lat: Math.round((region.lat + randomFloat(-0.3, 0.3)) * 1000) / 1000,
    epicenter_lon: Math.round((region.lon + randomFloat(-0.3, 0.3)) * 1000) / 1000,
    region_name: region.name,
    occurred_at: occurredAt,
    potential_tsunami: overrides.potentialTsunami ?? isTsunamiPotential(band),
  };
}

const THREAT_LEVELS: Array<[ThreatLevel, number]> = [
  ["Waspada", 40],
  ["Siaga", 35],
  ["Awas", 25],
];

export function generateTsunamiWarning(
  sequence: number,
  event: SeismicEvent,
  overrides: { threatLevel?: ThreatLevel } = {},
): TsunamiWarning {
  const zoneCount = randomInt(1, 3);
  const affectedZones = Array.from(new Set(Array.from({ length: zoneCount }, () => pick(REGIONS).name)));
  const issuedAt = new Date(event.occurred_at.getTime() + randomInt(60, 300) * 1000);
  const estimatedArrival = new Date(event.occurred_at.getTime() + randomInt(10, 60) * 60 * 1000);

  return {
    warning_id: `BMKG-TW-${String(sequence).padStart(6, "0")}`,
    related_event_id: event.event_id,
    threat_level: overrides.threatLevel ?? weightedPick(THREAT_LEVELS),
    affected_zones: affectedZones,
    estimated_arrival: estimatedArrival,
    issued_at: issuedAt,
  };
}
