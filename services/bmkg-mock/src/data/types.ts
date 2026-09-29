export interface SeismicEvent {
  event_id: string;
  magnitude: number;
  depth_km: number;
  epicenter_lat: number;
  epicenter_lon: number;
  region_name: string;
  occurred_at: Date;
  potential_tsunami: boolean;
}

export type ThreatLevel = "Waspada" | "Siaga" | "Awas";

export interface TsunamiWarning {
  warning_id: string;
  related_event_id: string;
  threat_level: ThreatLevel;
  affected_zones: string[];
  estimated_arrival: Date;
  issued_at: Date; // [TIM] tambahan, dipakai untuk filter `since`
}
