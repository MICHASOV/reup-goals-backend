# Privacy request operations

The API stores requests for access, export, rectification, restriction, objection, and erasure in `privacy_requests`. Recording a request is only the start of the process.

## Daily process

1. The privacy owner reviews open requests and items approaching `due_at` every business day.
2. Verify identity using the authenticated account and, where risk requires it, a second factor through the verified account email. Never request passport scans by default.
3. Classify the request, affected workspace, legal holds, processor data, and required approvers.
4. Export or change data from the primary regional database and the approved processors. Do not email raw database dumps or secrets.
5. Store a concise resolution summary, completion time, and evidence location without copying the exported business content into the request record.
6. Notify the subject through the verified channel and close the request.

The internal target is 10 calendar days. Legal deadlines and permitted extensions are decided by the privacy owner for the applicable jurisdiction.

## Erasure order

1. Revoke sessions and stop new background/AI work.
2. Delete OpenAI files, vector stores, and other persistent processor objects.
3. Delete workspace product content and account-linked records.
4. Retain only records with a documented legal basis, such as billing and the minimal acceptance evidence.
5. Confirm how deletion propagates through backups according to the backup lifecycle.

## Operational alerts

Production monitoring must alert when:

- a new request is created;
- a request is due in less than two business days;
- a request is overdue;
- processor deletion fails;
- an export is generated but not delivered or securely destroyed.

The alert must contain only the request ID, type, status, due time, and owner. It must not contain user messages or documents.
