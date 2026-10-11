# API findings

What the Opalstack REST API v1 does, as far as this project has established. Every fact names its
source. Anything not proven is marked **UNVERIFIED**.

Sources, by short name:

| Name | What it is |
|---|---|
| `spec` | [`spec/openapi.yaml`](spec/openapi.yaml), OpenAPI 3.0.3, fetched from `GET /schema/` on 2026-10-11 |
| `spec-05` | a copy saved from the dashboard on 2026-05-22, kept outside this repo |
| `spec-2023` | the copy inside `vlazic/opalstack-go` (`api/openapi.yaml`), August 2023 |
| `wrapper` | `opalstack/opalstack-python` at `aa3fc78` (2024-02-02), `src/opalstack/api.py` |
| `runbook` | the owner's operational runbook for the real account, kept outside this repo. Facts cited from it were observed in production use during 2026 |
| `explore` | output of [`scripts/explore.sh`](scripts/explore.sh), a GET-only survey of the live API, run 2026-10-11. `explore:<file>` names a file in the git-ignored `explore-out/` |

## Basics

| Fact | Source |
|---|---|
| Base URL `https://my.opalstack.com/api/v1/` | spec, runbook |
| Auth is the header `Authorization: Token <40 hex>`. The spec models it as an `apiKey` in the header, so the word `Token` is part of the value | spec `securitySchemes.tokenAuth` |
| Only `GET` and `POST` exist. There is no `PUT`, `PATCH` or `DELETE` | spec, wrapper `request()` |
| The standard set per kind is `GET /<kind>/list/`, `GET /<kind>/read/{uuid}` (no trailing slash), `POST /<kind>/create/`, `/update/`, `/delete/` | spec |
| Create and update take a JSON **array** of objects and return an array of response objects, status 200 | spec, wrapper |
| Delete takes an array of `{"id": uuid}` and returns 200 with a JSON **object**, not an array. Observed body: `{"ok":"Queued for deletion."}` | spec (`200: {}`), runbook |
| Update is partial: only `id` is required in every `*Update` schema | spec, wrapper README |
| List endpoints return a bare array. The spec declares no pagination parameter on any list path | spec |
| `?embed=<key>[,<key>]` on a GET replaces a foreign-key UUID with the nested object | spec `info.description`, wrapper |
| Documented error statuses are 400 on create, 400 and 404 on update and delete, 404 on read. No body schema is given for any of them (`{}`) | spec |

## States and waiting

`StateEnum` is `READY`, `PENDING_CREATE`, `PENDING_UPDATE`, `PENDING_DELETE` (spec). There is **no
failure state** in the enum. Every response object of a managed kind carries `state` and `ready`.

| Fact | Source |
|---|---|
| An update returns immediately with `state: PENDING_UPDATE`, then becomes `READY`. A site `disabled` flip usually lands within 25 s | runbook |
| A delete is queued; teardown takes 15 to 60 s, after which the object is absent from `/list/` | runbook |
| The official wrapper waits for ready by polling `GET /<kind>/read/{uuid}` every 5 s for `ready == true`, with no timeout by default | wrapper `wait_ready` |
| The official wrapper waits for deletion by polling the same path until it answers **404** | wrapper `wait_deleted` |
| The runbook's scripts wait for deletion by polling `/list/` until the id is gone, 5 s interval, 180 s limit | runbook |

Consequence: `WaitReady` polls `read` for `ready`; `WaitDeleted` treats 404 on `read` as done. Since
no failure state exists, a create that never becomes ready can only surface as a timeout, so the
timeout diagnostic must include the last `state` seen.

## Kinds

Field tables give the spec type. `*` marks a field the spec lists as required.

### osuser (shell user)

| | Fields |
|---|---|
| Create `OSUserCreate` | `server`* uuid, `name` string, max 16, `^[a-z0-9_-]+$`, `password` string, max 64 |
| Update `OSUserUpdate` | `id`*, `password` |
| Response `OSUserResponse` | `id`, `state`, `ready`, `name`, `server` |
| Delete | `[{"id"}]` |

- `name` and `server` are not in the update schema: both are RequiresReplace (spec).
- The create response carries a `default_password` that is **not in the spec's response schema**
  (runbook, and Opalstack's own installers read `.[0].default_password`). The runbook records that
  a `password` sent on create is overridden by that server-chosen value, and that a specific
  password is then set through update.
- **UNVERIFIED:** whether `default_password` is returned when `password` is omitted versus
  supplied, and whether any later read returns it. The spec says no read does.
- The API has no way to manage SSH keys on a shell user (runbook).

### domain

| | Fields |
|---|---|
| Create `DomainCreate` | `name`* string, max 255, `delegated_dns_zone` bool, default false |
| Update `DomainUpdate` | `id`*, `delegated_dns_zone` |
| Response `DomainResponse` | `id`, `state`, `ready`, `name`, `delegated_dns_zone`, `dkim_record`, `dkim_privkey`, `dkim_pubkey`, `is_valid_hostname` |
| Delete `DomainDelete` | `[{"id"}]` |

- `name` is RequiresReplace. `delegated_dns_zone` is updatable in place: `/domain/update/` and the
  field both appeared between May and October 2026 (see the spec comparison below). Every earlier
  source, including this project's research notes, says a domain cannot be updated.
- The three `dkim_*` fields are null on some domains (explore:`domain.list.txt`).
- **`dkim_privkey` is a private key in every domain response.** The provider must not expose it as
  an attribute and the client's debug log must redact it. The redaction pattern `key` covers it.

### app

| | Fields |
|---|---|
| Create `ApplicationCreate` | `name`* max 64, `^[a-z0-9_-]+$`, `osuser`* uuid, `type`* enum, `installer_url` nullable uri max 1024, `json` object |
| Update `ApplicationUpdate` | `id`*, `type`, `json` |
| Response `ApplicationResponse` | `id`, `state`, `ready`, `name`, `server`, `osuser`, `type`, `port`, `installer_url`, `json` |
| Delete | `[{"id"}]` |

- `AppTypeEnum`: `STA` `NPF` `APA` `CUS` `SLS` `SLP` `SLN` `SVN` `DAV`.
- `name`, `osuser` and `installer_url` are not updatable: RequiresReplace. An app cannot be moved
  to another shell user (spec, and confirmed the hard way in the runbook).
- `type` **is** updatable in place, which the research notes had as replace.
- `port` is server-assigned and null for types that have none (explore:`app.list.txt`).
- The live response also carries `osuser_name`, which the spec omits. `installer_url` can be null.
- `json` on the response holds per-type settings: `php_version`, `fpm_*`, `app_exec`, `url_fopen`,
  `sym_link_path`, `auto_site_url`, `add_https_security_headers`, `external_port_access`,
  `subroot`, and `db_*`/`app_*` keys used by installers. The spec gives the write-side `json` no
  schema at all. **UNVERIFIED:** which keys are accepted on create and update per type, and
  whether the server fills defaults into keys the caller omitted (which decides how the provider
  avoids a permanent diff).
- Deleting an app removes its directory on the server (runbook).
- `POST /app/installed/` and `/app/failed/` are callbacks for installer scripts. Not for Terraform.

### site

| | Fields |
|---|---|
| Create `SiteCreate` | `domains`* array of uuid, `routes`* array of `{app* uuid, uri max 255}`, `name` max 225, `^[a-z0-9_-]+$`, `ip4` nullable uuid, `ip6` nullable uuid, `cert` nullable uuid, `redirect` bool, `generate_le` bool, `le_http_challenge_tokens` array of string, `disabled` bool, `primary_domain` nullable uuid, `json` object |
| Update `SiteUpdate` | `id`* plus every create field |
| Response `SiteResponse` | `id`, `state`, `ready`, `name`, `server`, `ip4`, `ip6`, `disabled`, `domains`, `routes` (each `{id, app, uri}`), `generate_le`, `cert`, `redirect`, `le_http_challenge_tokens` |
| Delete | `[{"id"}]` |

- Everything is updatable in place, including `name` (spec).
- `routes` is replaced as a whole on update (runbook). A response route has its own `id`.
- The live response carries `primary_domain` (null or uuid) and `json` (object), although the
  spec's `SiteResponse` lists `json` only and omits `primary_domain` (explore:`site.list.txt`). So
  `primary_domain` can be read back and is a candidate attribute; site `json` stays out of the
  first version because nothing documents its keys.
- `ip6` is null on every site of the surveyed account and `ip4` is always set.
- `disabled: true` takes the site offline completely (runbook).
- Let's Encrypt results appear as entries in `/notice/list/`, not on the site (runbook).
- **UNVERIFIED:** whether the order of `routes` and of `domains` is preserved by the server.

### dnsrecord

| | Fields |
|---|---|
| Create `DNSRecordCreate` | `domain`* uuid, `type`* enum, `content`* max 64000, `priority` int, `ttl` int |
| Update `DNSRecordUpdate` | `id`*, `domain`, `type`, `content`, `priority`, `ttl` |
| Response `DNSRecordResponse` | `id`, `state`, `ready`, `domain`, `type`, `content`, `priority` (default 10), `ttl` (default 3600) |
| Delete | `[{"id"}]` |

- `DNSRecordTypeEnum`: `A` `AAAA` `CNAME` `MX` `TXT` `SRV` `CAA`.
- The spec lets `domain` and `type` change in place. Keep both RequiresReplace anyway: a record
  that changes type or zone is a different record, and this is the conservative reading.

### server and ip (read only)

- `GET /server/list/` returns an **object**, not an array: `{web_servers, imap_servers,
  smtp_servers}`, each an array. A web server is `{id, hostname, type}` in the spec; live, every entry also has `os`
  (string) and `vendor` (object), and `type` is `web`, `imap` or `smtp` (explore:`server.list.txt`).
- `GET /server/read/{uuid}` returns one web-server-shaped object. The spec documents no 404 for it.
- `IPAddressResponse` is `{id, ip, server, type, primary}` with `type` `"4"` or `"6"` as strings.

## Changes between spec copies

Derived with `diff` over the sorted path and schema names of each pair.

| From | To | Added | Removed |
|---|---|---|---|
| `spec-2023` | `spec-05` | `/tokenacl/{create,delete,list,read/{token},update}/`, `/register/`, `/payment/coupon_verify/`, `TokenACL*` schemas | `/usage/web/latest/`, `WebUsage*` schemas |
| `spec-05` | `spec` | `/domain/update/`, `DomainUpdate`, `delegated_dns_zone` on domain | `/token/list/`, `/token/read/{key}` |

The paths of the seven kinds this provider uses did not change in three years, apart from the
domain update added in 2026. **The API does change within v1 without a version bump**
(`info.version` is still `1.0.0`), so `spec/openapi.yaml` should be refetched before each phase:
`scripts/explore.sh` saves the live copy as `explore-out/schema.oai-yaml`.

## From the live survey

`scripts/explore.sh`, 2026-10-11, GET only. Every file it wrote holds field names, types, enum
values and counts; no object of the account is identifiable from them.

### The schema endpoint

`GET /schema/` answers 200 with a valid token. With `Accept: application/vnd.oai.openapi` it
returns YAML (125 KB); with `application/json`, `?format=json` or
`application/vnd.oai.openapi+json` it returns JSON (266 KB) (explore:`schema-status.txt`).

### Status codes and error bodies

| Request | Status | Body |
|---|---|---|
| `read` of a well-formed UUID that does not exist, for osuser, ip, domain, app, site, dnsrecord | 404 | JSON `{"id":["Not found."]}` |
| the same for server | 404 | JSON `{"errors":["Not found."]}` |
| `read` of an id that is not a UUID, all seven kinds | 404 | **HTML**, `content-type: text/html` |
| any request with an invalid token | 401 | JSON `{"detail":"Invalid token."}` |

Consequences for the client:

- Not-found is **404 on `read`**, whatever the body. `ErrNotFound` is decided on the status alone.
- Error bodies come in at least three JSON shapes (`{"<field>": [..]}`, `{"errors": [..]}`,
  `{"detail": ".."}`) and one non-JSON shape. The client keeps the raw body for the diagnostic
  and extracts messages on a best-effort basis; it never assumes JSON.
- 401 is its own error (`ErrUnauthorized`): a bad token must stop the run, not be retried and not
  be read as "object gone".
- **UNVERIFIED:** what a 403 looks like. An object hidden by a token ACL may answer 403 or 404;
  only a scoped token can tell, and the answer decides whether the provider can distinguish
  "deleted" from "not allowed" at all.

### Lists, headers, embed

- Every list of 17 to 126 objects came back as one bare array. No `Link`, `X-Total-Count` or other
  pagination header, and no rate-limit header of any kind (explore:`list-headers.txt`). Rate limits,
  if any, are invisible until hit.
- `allow: OPTIONS, GET` on a list path.
- `?embed=` works as documented: `server` on osuser and ip, `osuser,server` on app, `domain` on
  dnsrecord, and `domains,ip4` on site (where `domains` becomes an array of objects) each turn the
  UUID into an object. An unknown embed key is ignored silently, with status 200
  (explore:`embed.txt`).

### Names

In an account with 17 shell users, 37 apps, 35 sites and 126 domains: no two shell users, sites or
domains share a name, and no app name is used twice, either within one shell user or across two
(explore:`app.uniqueness.txt`, and `duplicate_names` in each `*.list.txt`). That is consistent with
names being unique account-wide per kind, but it is an observation, not a rule: **UNVERIFIED** until
a duplicate create is attempted. Import by name must therefore fail on more than one match rather
than pick one.

### Values seen

- `state` was `READY` and `ready` true on every object of every kind. No pending state was observed.
- App types in use: `APA`, `CUS`, `NPF`, `STA`. DNS record types in use: `A`, `AAAA`, `CNAME`, `TXT`.
- IP: one address, type `"4"`, primary.

## UNVERIFIED, needs a write test

None of these can be answered with GET. Each is to be settled against `tfacc-` objects only, once a
sandbox shell user and a scoped token exist.

1. What a failed create looks like, given that `StateEnum` has no failure value.
2. Deleting a parent that still has children: a shell user with apps, an app still routed by a
   site, a domain attached to a site. Rejected, cascaded or orphaned?
3. Whether a token restricted by object ACL can create objects, and then read and delete them.
4. `default_password`: present with and without a caller-supplied `password`? Ever readable later?
5. Which `json` keys an app accepts per type, and whether the server adds defaults.
6. Order preservation of `site.routes` and `site.domains`.
7. Which DNS records a site creates on its own, and whether they appear in `/dnsrecord/list/`.
8. The 400 body for a validation error (duplicate name, bad enum, bad uuid).
9. Whether a name can be reused immediately after its delete is queued, or only after teardown.
10. What an ACL-hidden object answers on `read`: 403 or 404.
