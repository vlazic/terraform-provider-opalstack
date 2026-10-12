# Where the work stands

Written to be read with no other context. Facts about the API are in
[`API_FINDINGS.md`](API_FINDINGS.md) and are not repeated here.

## Done (2026-10-12)

- Read-only survey of the live API, and the findings file built from it, the OpenAPI schema, the
  official Python wrapper and the owner's runbook.
- `internal/client`: one-element-array create and update, queued delete, 404/403/401 as distinct
  errors, waiting for ready and for deletion, retries, request spacing, redacted debug log, and the
  safety prefix, which also covers the objects a write refers to. Covered by offline unit tests.
- `internal/provider`: five data sources (`opalstack_server`, `opalstack_ip`, `opalstack_osuser`,
  `opalstack_domain`, `opalstack_app`) and five resources (`opalstack_domain`,
  `opalstack_dnsrecord`, `opalstack_osuser`, `opalstack_app`, `opalstack_site`), each with
  timeouts and import.
- Tests at two levels. `make test` drives every data source and resource through a real terraform
  binary against an in-memory fake API (`internal/provider/fake_test.go`), offline. `make testacc`
  runs the five `TestAcc*` tests against the real account; all five passed on 2026-10-12, and each
  ends by failing if an object with the test prefix is left.
- `make sweep` deletes leftover test objects. `make docs` regenerates `docs/` from the schemas
  and `examples/`.

## Decisions that differ from the first design notes

- **Waiting cannot fail fast.** The API's state enum has no failure value, so `WaitReady` only ends
  on ready, on an error from the read, or on the caller's deadline. Its timeout error names the
  last state seen.
- **Deletion is detected by 404 on `read`**, not by polling the list. `WaitReady` returns the object from the poll that reported ready.
- **A POST is not retried on a server error or a transport error**, only on 429. A repeated
  create could make a second object. A GET is retried on 429, on any 5xx and on a transport error.
  A `Retry-After` header is obeyed up to one minute, and a 429 holds back every other request too.
- **An id that is not a UUID is refused before any request.** The API answers such an id with 404,
  which would read as "deleted".
- **A safety-prefix refusal never matches `ErrNotFound`**, even when the lookup behind it did.
- **The safety prefix costs one extra GET per update and delete**, because those requests carry an
  id and the prefix is a property of the name. When the name cannot be read the write is refused.
- **The safety prefix also checks the new name of a rename**, and refuses any write it cannot tie
  to a name. It reads the request as it will be sent, so a new request type needs no code for it.
- **A wait sleeps before its first poll**, because the POST it follows already said "pending".
- **`dkim_privkey` is never decoded.** The client's domain type has no field for it.
- **`domain` has an update** (`delegated_dns_zone`), which the first notes ruled out.
- **`dnsrecord` update leaves out `domain` and `type`** although the API accepts them.

- **`opalstack_app.json` manages only the keys the configuration names.** The API fills in
  settings of its own (`php_version` on an `APA` app), and those never show as a diff.
- **A type change to or from `CUS` replaces the app.** In place the API changes the type but
  assigns no port.
- **A site's domains and routes are sets**, a site needs `ip4_id` or `ip6_id`, and a route `uri`
  must end with `/`, because the API appends one.
- **`opalstack_osuser` sets no password.** The API chooses one and reports it once, as
  `default_password`.
- **The framework's pointer getters turn an unknown value into a zero.** Requests are built with
  `knownBool`, `knownInt64` and `knownString` from `internal/provider/resource.go` instead.

## The test setup

- A sandbox shell user and a base domain for tests exist on the account. Their names are not in
  this repository; tests take them from `OPALSTACK_TEST_OSUSER`, `OPALSTACK_TEST_OSUSER_ID` and
  `OPALSTACK_TEST_DOMAIN_BASE`. The sandbox user's name deliberately does not start with
  `tfacc-`, so neither the safety prefix nor the sweeper can ever match it.
- The test token (`pass` entry `Opalstack/tokens/tfacc`) has the service read and write switches
  for domain, dnsrecord, osuser, app and site, and ip read. Opalstack's per-object ACL turned out
  not to allow creating anything, so **this token can change every object of those five kinds on
  the account, and the safety prefix is the only thing between a test and a real object**. Every
  acceptance test configures the provider with `safety_prefix`, and the app and site tests with
  `safety_osuser_id` as well. Narrow the token again when testing is not in progress.
- The owner's wrapper that exports those variables and the token lives outside this repository.

## Next

1. A guide for adopting an existing account piece by piece (import blocks, what to import first).
2. The remaining UNVERIFIED items in the findings file: a failed create, deleting a parent that
   has children, the 400 body shapes, name reuse right after a delete, app settings per type, and
   a site with `generate_le` on.
3. Refetch the schema (`scripts/explore.sh` saves it as `explore-out/schema.oai-yaml`) and diff it
   against `spec/openapi.yaml`. The API changed inside v1 between May and October 2026.
4. Not started, and not planned for the first version: mail (address, mailuser), databases
   (mariadb, psqldb and their users), certificates, a settable shell user password.
5. A release: `.goreleaser.yml` exists and has never been run.

## Before this repository is ever made public

- `CLAUDE.md` and the scripts under `scripts/` name `pass` entries, and one of those paths contains
  an email address.
- Run `make leaks` over the whole history.
- Decide the registry namespace. Publishing to the Terraform Registry is permanent.
