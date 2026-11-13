# Kenapa Commitnya Seperti Ini

Folder ini menjawab pertanyaan yang berbeda dari `docs/commits/`. Dokumen di
`docs/commits/` menjelaskan apa yang dikerjakan. Dokumen di sini menjelaskan
dasar keputusannya: spek atau kontrak mana yang menuntut, alternatif apa yang
ditolak, kenapa batas commit-nya di situ, dan konsekuensinya.

| Branch | Commit | File |
|---|---|---|
| P5 coastal | `docs: add M2 internal ingest contract and P4/P5/bonus plan` | `p5-coastal-01.md` |
| P5 coastal | `feat(coastal): scaffold service and internal ingest client` | `p5-coastal-02.md` |
| P5 coastal | `feat(coastal): add SQLite state, cursor, and outbox` | `p5-coastal-03.md` |
| P5 coastal | `feat(coastal): add Sensor Pantai HTTP client` | `p5-coastal-04.md` |
| P5 coastal | `feat(coastal): validate sensor contract and map COASTAL events` | `p5-coastal-05.md` |
| P5 coastal | `feat(coastal): add circuit breaker` | `p5-coastal-06.md` |
| P5 coastal | `feat(coastal): add Prometheus metrics` | `p5-coastal-07.md` |
| P5 coastal | `feat(coastal): poll, validate, and forward observations` | `p5-coastal-08.md` |
| P5 coastal | `fix(coastal): park terminal 4xx instead of stalling cursor` | `p5-coastal-09.md` |
| P5 coastal | `feat(coastal): add Kubernetes manifests and deploy wiring` | `p5-coastal-10.md` |
| P5 coastal | `docs(coastal): document operation, limitations, and AI declaration` | `p5-coastal-11.md` |
| P5 load shedding | `feat(auth): add role claim and propagate it to API principal` | `p5-load-01.md` |
| P5 load shedding | `feat(api): add priority admission control for media and field` | `p5-load-02.md` |
| P5 load shedding | `test(loadtest): add P5 media flood and field priority scenario` | `p5-load-03.md` |
| P4 satellite | `feat(satellite): scaffold service and SSE client` | `p4-satellite-01.md` |
| P4 satellite | `feat(satellite): add SQLite detection, offset, fire, and outbox state` | `p4-satellite-02.md` |
| P4 satellite | `feat(satellite): group detections by event time and distance` | `p4-satellite-03.md` |
| P4 satellite | `feat(satellite): compute severity, area, and stable fire IDs` | `p4-satellite-04.md` |
| P4 satellite | `feat(satellite): reconcile fires after retract and split` | `p4-satellite-05.md` |
| P4 satellite | `feat(satellite): add internal ingest client` | `p4-satellite-06.md` |

## Cara membaca

Mulai dari bagian **Dasar**. Di situ disebut spek, kontrak, atau batasan kode
yang membuat commit ini perlu ada. Bagian **Alternatif** menyebut pilihan yang
sempat masuk pertimbangan dan alasan penolakannya. Bagian **Batas** menjelaskan
kenapa pekerjaan tidak digabung dengan commit lain. Bagian **Konsekuensi**
menyebut risiko yang masih tersisa, supaya dokumen ini tidak terdengar seperti
klaim bahwa semuanya sudah sempurna.
