# Provisional captures — SG-MOM-OT-001 (Stage 1 retrieval)

Captured representations of official sources, not original source bytes.
SHA-256 covers the captured artifact only. Byte-grade verification (Stage 2)
is still required before any source counts as pinned.

## P1 — MOM hours/overtime page

- source_id: P1
- issuing_authority: Ministry of Manpower (MOM), Singapore
- document_title: Hours of work, overtime and rest day
- official_url: https://www.mom.gov.sg/employment-practices/hours-of-work-overtime-and-rest-days
- retrieval_timestamp_utc: 2026-11-08T09:05:55Z (second of two identical fetches this session; first minutes earlier, same 67705-byte payload)
- retrieval_method: model-side page fetch, processed-text transcript
- retrieval_tool: web_fetch (HTTP 200, text/html)
- document_publication_date: Last Updated 24 July 2025 (page footer)
- document_effective_date: unstated on page
- source_locator: sections Who is covered, Definitions, Normal hours of work, Overtime pay, Calculate your overtime pay, Maximum hours of work, Rest day
- artifact_type: processed-text transcript (15017 bytes; fetched HTML was 67705 bytes)
- artifact_sha256: 2048e745dbf499be1dc2d5c34fc0731ceb96e49af4fcdf19c7beee31b23d0bd6
- hash_scope: exact bytes of P1_mom-hours-overtime.txt as saved
- provenance_grade: provisional_transcript
- limitations: transcript, not source HTML; markup, scripts, styling, and any untranscribed elements absent; page gives no effective-date history

## P2 — MOM who-is-covered page

- source_id: P2
- issuing_authority: Ministry of Manpower (MOM), Singapore
- document_title: Employment Act: who it covers
- official_url: https://www.mom.gov.sg/employment-practices/employment-act/who-is-covered
- retrieval_timestamp_utc: 2026-11-08T09:06:39Z
- retrieval_method: model-side page fetch, processed-text transcript
- retrieval_tool: web_fetch (HTTP 200, text/html)
- document_publication_date: Last Updated 24 July 2025 (page footer)
- document_effective_date: unstated on page
- source_locator: sections Who is covered under Part 4, Who is a manager or executive, Who is a workman
- artifact_type: processed-text transcript (8664 bytes; fetched HTML was 57961 bytes)
- artifact_sha256: bddfd246d650e93c3808bc007ba1d405d2bf3a944e1e0514c8d4f539df55a6ed
- hash_scope: exact bytes of P2_mom-who-is-covered.txt as saved
- provenance_grade: provisional_transcript
- limitations: transcript, not source HTML; no effective-date history; links to SSO Act noted but not followed (P6)

## P3 — MOM FAQ salary limit

- source_id: P3
- issuing_authority: Ministry of Manpower (MOM), Singapore
- document_title: Is there a salary limit for overtime pay entitlement?
- official_url: https://www.mom.gov.sg/faq/hours-of-work-overtime-and-rest-days/is-there-a-salary-limit-for-overtime-pay-entitlement
- retrieval_timestamp_utc: 2026-11-08T09:07:01Z
- retrieval_method: model-side page fetch, processed-text transcript
- retrieval_tool: web_fetch (HTTP 200, text/html)
- document_publication_date: Last Updated 14 March 2024 (page footer)
- document_effective_date: unstated on page
- source_locator: full answer (two threshold bullets)
- artifact_type: processed-text transcript (4162 bytes; fetched HTML was 46509 bytes)
- artifact_sha256: 1802efcb24ee660174258a43fdd2056724030798e122c406b382f55c115daede
- hash_scope: exact bytes of P3_mom-faq-salary-limit.txt as saved
- provenance_grade: provisional_transcript
- limitations: transcript, not source HTML; corroborates thresholds only

## P4 — MOM overtime calculator page

- source_id: P4
- issuing_authority: Ministry of Manpower (MOM), Singapore
- document_title: Calculate overtime pay for a monthly-rated employee
- official_url: https://www.mom.gov.sg/employment-practices/salary/calculate-overtime-pay
- retrieval_timestamp_utc: 2026-11-08T09:07:07Z
- retrieval_method: model-side page fetch, processed-text transcript
- retrieval_tool: web_fetch (HTTP 200, text/html)
- document_publication_date: Last Updated 24 July 2025 (page footer)
- document_effective_date: unstated on page
- source_locator: eligibility bullets; overtime-work definition (excluding breaks)
- artifact_type: processed-text transcript (5841 bytes; fetched HTML was 52391 bytes)
- artifact_sha256: adb2bb9ac72c55b4c40063387ebcf12ebd027d210eadae590991388808ded227
- hash_scope: exact bytes of P4_mom-calculator.txt as saved
- provenance_grade: provisional_transcript
- limitations: transcript, not source HTML; calculator is client-side JS — its rounding/logic is NOT captured and must not be inferred from this page

## P5 — MOM OT poster PDF

- source_id: P5
- issuing_authority: Ministry of Manpower (MOM) / TADM, Singapore
- document_title: Get The Correct Overtime (OT) Pay On Time
- official_url: https://www.mom.gov.sg/-/media/mom/documents/employment-practices/get-correct-ot-pay-on-time.pdf
- retrieval_timestamp_utc: 2026-11-08T09:07:14Z
- retrieval_method: model-side binary fetch, saved bytes
- retrieval_tool: web_fetch (HTTP 200, application/pdf, 1041950 bytes)
- document_publication_date: PDF CreationDate 2019-11-19, ModDate 2020-01-16 (embedded metadata, unverified against any registry)
- document_effective_date: unstated
- source_locator: single-page poster; relevant line: contractual hours <= 44/week, beyond-contractual = OT hours
- artifact_type: PDF bytes as saved (1041950 bytes; rendered page 1 inspected, no formulas/tables present)
- artifact_sha256: bac2d0ad428765aab38bdbfa30b3ed25b16016235f2e25bd9362373b66cfdaf8
- hash_scope: exact bytes of P5_mom-ot-poster.pdf as saved
- provenance_grade: provisional_source_capture
- limitations: employee-awareness poster, not a formula source; saved bytes, not a transcript — but identity against the original source delivery is unverified, so Stage 2 re-fetch must still confirm it

## P6 — SSO Employment Act attempt (failed)

- source_id: P6
- issuing_authority: Attorney-General's Chambers (Singapore Statutes Online)
- document_title: Employment Act 1968 (attempted)
- official_url: https://sso.agc.gov.sg/Act/EmA1968
- retrieval_timestamp_utc: 2026-11-08 ~09:06Z
- retrieval_method: model-side page fetch
- retrieval_tool: web_fetch
- document_publication_date: n/a (not retrieved)
- document_effective_date: n/a
- source_locator: n/a
- artifact_type: failed-retrieval record (this file's P6 section + P6_sso-act-403.txt) — NOT a source artifact; digest covers the record only
- artifact_sha256: 967190fb55af9e67e2f3f3e6ae569cdfa2562839f2fa8647dcc56fc7ccc92d11
- hash_scope: exact bytes of P6_sso-act-403.txt as saved
- provenance_grade: provisional_transcript
- limitations: HTTP 403 Forbidden — legislation text, Fourth Schedule, and amendment history NOT captured; requires Option 2 (user-provided snapshot) or unimpaired network
