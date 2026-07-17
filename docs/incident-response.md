# Personal-data incident response

## Severity trigger

Open an incident immediately for unauthorized access, disclosure, alteration, loss, encryption, or destruction of personal or confidential business data; leaked credentials; workspace isolation failures; unexpected foreign-region processing; or inability to delete provider-side data.

## First 60 minutes

1. Name an incident commander and recorder.
2. Preserve audit evidence without copying user content into ordinary chat or tickets.
3. Revoke affected sessions and rotate compromised credentials.
4. Contain the path: disable the endpoint, provider integration, account, or deployment when necessary.
5. Identify affected workspaces, data categories, countries, approximate subjects/records, time window, and processors.
6. Contact the privacy owner and legal counsel.

## Notification clocks

- Russia: prepare the initial Roskomnadzor notification within 24 hours and investigation results within 72 hours for a qualifying incident under Article 21 of 152-FZ: <https://www.consultant.ru/document/cons_doc_LAW_61801/d3fe43a7c415353b17faab255bc0de92bea127da/>.
- GDPR: where the breach is likely to risk individuals' rights and freedoms, notify the competent authority without undue delay and, where feasible, within 72 hours; communicate to affected people without undue delay where high risk exists: <https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32016R0679>.
- Processor contracts may impose shorter notification windows. Follow the shortest applicable clock.

Legal/privacy decides whether notification is legally required. Engineering must not wait for perfect certainty before escalating.

## Required incident record

- discovery and occurrence times;
- systems, regions, workspaces, users, and processors affected;
- data categories and approximate volume;
- cause, attack path, and compromised credentials;
- containment and recovery steps;
- likely impact and risk assessment;
- notification decisions, recipients, timestamps, and copies;
- corrective actions, owners, deadlines, and verification evidence.

## Recovery

Restore from a verified backup only after closing the attack path. Validate tenant isolation, session revocation, external-file cleanup, background queues, and audit continuity. Perform a blameless review and add regression tests for the failure mode.
