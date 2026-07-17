# Privacy compliance baseline

Status: technical baseline, not a legal opinion or certification. The final controller/processor roles, lawful bases, public documents, regulator filings, contracts, and country scope must be approved by the privacy owner and Russian/EU counsel before accepting real customer data.

## 1. Scope and product data

REUP.goals processes account data, billing metadata, business chat history, uploaded files, transcriptions, knowledge-base facts, strategy, tactics, tasks, technical logs, and AI-derived documents. Business content can include personal data of employees, customers, suppliers, and other third parties even when the account owner does not label it as personal data.

Data classes:

| Class | Examples | Default control |
| --- | --- | --- |
| Restricted secrets | passwords, JWT signing secret, API and DB credentials | secret manager only; never logs or backups outside the approved region |
| Confidential business data | messages, files, documents, strategy, finances | workspace isolation, TLS, encrypted storage/backups, least privilege |
| Personal data | email, authors, assignees, content about identifiable people | legal basis, transparency, rights workflow, retention and transfer controls |
| Operational metadata | request IDs, paths, latency, token usage, costs | no prompt bodies; automatic retention |
| Legal evidence | document acceptance and withdrawal receipts | immutable event history, pseudonymous subject key, limited retention |

Special-category, biometric, state-secret, medical, or criminal-offence data is not an intended product input. The product must warn customers not to upload it unless a separately approved processing mode exists.

## 2. Russian 152-FZ baseline

Before a Russian production launch, the operator must:

1. Appoint a person responsible for personal-data processing and approve local acts, processing purposes, categories, retention, destruction, access, incident, and internal-audit procedures. Article 18.1 requires these organizational measures and publication of the policy: <https://www.consultant.ru/document/cons_doc_LAW_61801/eeeebe22bf738fd65bb66b95cc278911ae2525ee/>.
2. File or update the operator notification with Roskomnadzor before processing unless a narrow statutory exception applies. The notification includes purposes, categories, security measures, database location, and cross-border transfer information: <https://www.consultant.ru/document/cons_doc_LAW_61801/d996966e22e1320c9de1ab82d9f6be12c3d9d765/>.
3. Record, systematize, accumulate, store, update, and retrieve personal data of Russian citizens using databases in Russia. `PRIVACY_MODE=ru_152fz` and `dual` therefore require a `ru-*` primary region. A German staging database is test-only.
4. Complete the separate cross-border-transfer assessment and Roskomnadzor notification before sending user content to foreign AI or infrastructure providers. Article 12 requires a separate notification and information about the recipient's safeguards: <https://www.consultant.ru/document/cons_doc_LAW_61801/e4ebbe1780de623c7cf32a59ca82a7bb523a25dd/>.
5. Use a separate personal-data consent rather than embedding it in the offer or another document. Federal Law No. 156-FZ of 24 June 2025 introduced this rule: <https://publication.pravo.gov.ru/document/0001202506240021>.
6. Maintain the 24-hour initial and 72-hour investigation notification path for a qualifying personal-data incident. Use the incident runbook in this repository.

The application now records separate, versioned events for the offer, privacy notice, personal-data consent, and optional marketing consent. Consent alone does not replace the operator notification, localization, processor contracts, or cross-border notification.

## 3. GDPR baseline

GDPR applies only where its territorial scope is met, for example through an EEA establishment, offering goods/services to people in the EEA, or monitoring their behavior. If it applies, each processing purpose needs an Article 6 basis and the controller must implement transparency, data-subject rights, privacy by design/default, processor terms, records of processing, security, breach response, and international-transfer safeguards. The authoritative regulation is: <https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:32016R0679>.

The application provides:

- separate consent evidence and withdrawal history;
- an authenticated request registry for access, export, rectification, restriction, objection, and erasure;
- account deletion with PostgreSQL and persistent OpenAI file/vector-store cleanup;
- minimized operational AI logs without prompt or response bodies;
- configurable retention and production residency gates;
- workspace-bound product data and auditable request IDs.

Organizational work still required before claiming GDPR readiness:

- determine whether ООО «РЕАП» is controller, processor, or both for each purpose;
- create and maintain Article 30 records;
- sign Article 28 processor terms with every processor;
- select and document the Chapter V transfer mechanism, normally SCCs plus a transfer impact assessment where required;
- complete a DPIA before high-risk profiling, large-scale sensitive data, or materially expanded AI use;
- identify the competent supervisory authority and whether an Article 27 EU representative is required;
- establish an identity-verification and fulfilment process for privacy requests.

EDPB guidance confirms that international transfers require both a lawful processing basis and a Chapter V transfer mechanism: <https://www.edpb.europa.eu/sme/be-compliant/international-data-transfers_en>.

## 4. Launch gates

The backend refuses to start in `APP_ENV=production` when:

- `PRIVACY_MODE` is `development` or `test`;
- the data-residency region or privacy contact is absent;
- a Russian/dual mode does not use a `ru-*` primary region;
- Russian/dual mode has not explicitly attested the cross-border registration;
- GDPR/dual mode has no documented transfer mechanism.

These are deployment assertions, not legal conclusions. They prevent accidental launch with a known-invalid topology while keeping local and staging work unblocked.

## 5. Mandatory manual decisions

The following cannot be solved by code and remain launch blockers:

- confirm or update the Roskomnadzor operator notification;
- submit the separate cross-border-transfer notification for every destination and recipient;
- select the Russian production hosting, object storage, backup, log, and disaster-recovery locations;
- approve OpenAI, Unisender, CloudPayments, hosting, email, monitoring, and support contracts and processor clauses;
- confirm the public policy against the actual provider list and infrastructure immediately before launch;
- approve retention for accounting/payment records and legal evidence;
- appoint the privacy/security owners and incident contacts;
- complete the Russian information-system threat model and required protection-level assessment with a qualified specialist.
- run a one-time acceptance migration for existing accounts: show the current offer, notice, and separate consent, then store the same versioned evidence used for new registrations;
- configure an operational alert and named owner for new or overdue `privacy_requests`; the API registry is not an automatic fulfilment service;
- verify the processor register against the actual production topology immediately before every material provider change.
