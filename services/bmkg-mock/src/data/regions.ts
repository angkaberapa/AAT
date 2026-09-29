// Static reference table: plausible epicenter regions along Indonesia's seismic belt.

export interface Region {
  name: string;
  lat: number;
  lon: number;
}

export const REGIONS: Region[] = [
  { name: "Selat Sunda", lat: -6.95, lon: 105.2 },
  { name: "Nias, Sumatera Utara", lat: 1.05, lon: 97.4 },
  { name: "Mentawai, Sumatera Barat", lat: -2.3, lon: 99.6 },
  { name: "Bengkulu", lat: -3.8, lon: 102.3 },
  { name: "Pangandaran, Jawa Barat", lat: -7.7, lon: 108.5 },
  { name: "Palu, Sulawesi Tengah", lat: -0.9, lon: 119.9 },
  { name: "Ambon, Maluku", lat: -3.7, lon: 128.2 },
  { name: "Flores, Nusa Tenggara Timur", lat: -8.6, lon: 121.9 },
  { name: "Biak, Papua", lat: -1.2, lon: 136.1 },
  { name: "Banyuwangi, Jawa Timur", lat: -8.4, lon: 114.3 },
];
