# Where the work stands

Written to be read with no other context. Facts about the API are in
[`../API_FINDINGS.md`](../API_FINDINGS.md) and are not repeated here.

## Done (2026-10-12)

- Read-only survey of the live API, and the findings file built from it, the OpenAPI schema, the
  official Python wrapper and the owner's runbook.
- `internal/client`: one-element-array create and update, queued delete, 404/403/401 as distinct
  errors, waiting for ready and for deletion, retries, request spacing, redacted debug log, and the
  safety prefix. Covered by offline unit tests.
- `internal/provider`: provider schema and configuration. No resource or data source yet.
- The built provider loads under Terraform 1.5.0 and 1.11.2 and OpenTofu 1.12.6 through
  `dev_overrides`. With no resource in the configuration neither CLI calls the provider's
  `Configure`, so that function is covered by unit tests only.

## Decisions that differ from the first design notes

- **Waiting cannot fail fast.** The API's state enum has no failure value, so `WaitReady` only ends
  on ready, on an error from the read, or on the caller's deadline. Its timeout error names the
  last state seen.
- **Deletion is detected by 404 on `read`**, not by polling the list. `WaitReady` returns the object from the poll that reported ready.
- **A POST is not retried on a gateway error or a transport error**, only on 429. A repeated
  create could make a second object. A GET is retried on 429, 502, 503 and 504.
- **The safety prefix costs one extra GET per update and delete**, because those requests carry an
  id and the prefix is a property of the name. When the name cannot be read the write is refused.
- **The safety prefix also checks the new name of a rename**, and refuses any write it cannot tie
  to a name. It reads the request as it will be sent, so a new request type needs no code for it.
- **A wait sleeps before its first poll**, because the POST it follows already said "pending".
- **`dkim_privkey` is never decoded.** The client's domain type has no field for it.
- **`domain` has an update** (`delegated_dns_zone`), which the first notes ruled out.
- **`dnsrecord` update leaves out `domain` and `type`** although the API accepts them.

## Next: resources and acceptance tests

Nothing below may start while the full-access token is the only token. Rule 2 of
[`../CLAUDE.md`](../CLAUDE.md) is the gate.

### The owner does first

1. Create a sandbox shell user for tests on the account's web server.
2. Create a token for tests. Service ACL: only the endpoints of osuser, domain, app, site,
   dnsrecord, server and ip. Object ACL: only the sandbox shell user. The `/tokenacl/` endpoints
   in the spec describe what an ACL can express.
3. Store it in `pass` under its own entry, separate from the full-access token.
4. Say which base domain test domains may be created under.

### Then, in order

1. Refetch the schema (`scripts/explore.sh` saves it as `explore-out/schema.oai-yaml`) and diff it
   against `spec/openapi.yaml`. The API changed inside v1 between May and October 2026.
2. Answer item 3 and item 10 of the UNVERIFIED list in the findings file first: can a scoped token
   create objects and then manage them, and does a hidden object answer 403 or 404? Both decide
   whether the test design holds at all.
3. Data sources: `opalstack_server`, `opalstack_ip`, `opalstack_osuser`, `opalstack_domain`,
   `opalstack_app`. These need no write.
4. Resources, one at a time, each with acceptance tests before the next: `opalstack_osuser`,
   `opalstack_domain`, `opalstack_app`, `opalstack_site`, `opalstack_dnsrecord`. Before the first
   real write of each kind, show the owner the exact request and wait for an OK.
5. The safety prefix so far checks names only. Add the second condition before any app or site
   test: the object must belong to the sandbox shell user.
6. Sweepers that delete only `tfacc-` objects, never the sandbox user.
7. Settle the remaining UNVERIFIED items with `tfacc-` objects and update the findings file.

### Design points already known for that work

- `opalstack_osuser`: `name` and `server_id` replace. The API accepts a `password` on create and on
  update and also returns a server-chosen `default_password` on create. Which of the two the
  resource exposes depends on UNVERIFIED item 4.
- `opalstack_domain`: `name` replaces, `delegated_dns_zone` updates in place.
- `opalstack_app`: `name`, `osuser_id` and `installer_url` replace; `type` and the settings update
  in place. The settings object has no schema, see UNVERIFIED item 5.
- `opalstack_site`: everything updates in place. `routes` is replaced as a whole.
- `opalstack_dnsrecord`: `domain_id` and `type` replace. A site creates DNS records on its own; the
  resource must not adopt or fight them (UNVERIFIED item 7).
- Import by name must fail on more than one match. Name uniqueness is observed, not proven.

## Before this repository is ever made public

- `CLAUDE.md` and `scripts/explore.sh` name a `pass` entry whose path contains an email address.
- Run `make leaks` over the whole history.
- Decide the registry namespace. Publishing to the Terraform Registry is permanent.
