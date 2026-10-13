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

**The live API has states the spec does not list** (`TestFinding*` probes, 2026-10-12):

- `PENDING_START`: what the answer to an app create carries.
- `PENDING_INSTALL`: an app created with an `installer_url`, while the installer has not reported.
- `INSTALL_FAILED`: the same app when the installer never succeeds. With a URL that answers 404,
  and with one on a host that does not exist, the app sat in `PENDING_INSTALL` for more than three
  minutes and was `INSTALL_FAILED`, `ready` false, some minutes later (seen at 11 minutes). It
  stays there, and it can be deleted. So there **is** a failure state, and `WaitReady` stops on any state ending in
  `_FAILED` instead of running into its timeout.

| Fact | Source |
|---|---|
| An update returns immediately with `state: PENDING_UPDATE`, then becomes `READY`. A site `disabled` flip usually lands within 25 s | runbook |
| A delete is queued; teardown takes 15 to 60 s, after which the object is absent from `/list/` | runbook |
| The official wrapper waits for ready by polling `GET /<kind>/read/{uuid}` every 5 s for `ready == true`, with no timeout by default | wrapper `wait_ready` |
| The official wrapper waits for deletion by polling the same path until it answers **404** | wrapper `wait_deleted` |
| The runbook's scripts wait for deletion by polling `/list/` until the id is gone, 5 s interval, 180 s limit | runbook |

Consequence: `WaitReady` polls `read` for `ready`; `WaitDeleted` treats 404 on `read` as done. A
create that neither becomes ready nor reaches a failed state can only surface as a timeout, so the
timeout diagnostic includes the last `state` seen.

**A delete can be lost.** Sent while the create of an app is still pending, `POST /app/delete/`
answers 200, the app reads `PENDING_DELETE`, and about 6 seconds later it is `READY`: the create
finished and the delete is gone. A domain deleted in `PENDING_CREATE` did go (404 after 11 s).
`WaitDeleted` therefore sends the delete again when a poll finds the object settled, ready or
failed. Any other state is waited out, since a state the schema does not list may be a deletion.

### Deleting something that is still referred to

`TestFindingParents` and `TestFindingShellUserWithApp`, 2026-10-12:

| Delete of | Answer |
|---|---|
| a domain a site serves | 400 `[{"id":["Domain <name> is attached to site <site>. Please remove it from the site first."]}]` |
| an app a site routes to | accepted. The app goes, and **the route is silently removed from the site**, which stays `READY` with one route fewer (none, in the test) |
| a shell user that has an app | accepted, and **the app is deleted with it**: once the user was gone, a read of the app answered 404 |

### Names

- A second domain with a name in use answers 400 `[{"name":["Domain <name> already exists."]}]`.
- A second app with a name the same shell user already uses answers 400
  `[{"non_field_errors":["Name already in use by this osuser."]}]`.
- **A name is not free while its delete is queued**: both creates above answer the same 400 until
  the first object is gone. Once a read of it answers 404, the name can be used again at once.
  Replacing an object under the same name must therefore wait for the deletion, which
  `WaitDeleted` does; `create_before_destroy` cannot work for a resource that keeps its name.

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
- Observed on 2026-10-12, creating the sandbox shell user with a caller-chosen `password`: the
  create response had a `default_password`, it was **not** the password sent, the response state
  was `PENDING_CREATE`, and a `read` once the user was ready did not return `default_password`.
  So the password sent on create is ignored, and the server's one is available exactly once.
- `default_password` is also returned when no `password` is sent (`TestAccOSUser`, 2026-10-12).
  The provider sends none and exposes the value as a sensitive computed attribute.
- **UNVERIFIED:** whether a password set through update takes effect. The resource does not offer it.
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
  schema at all.
- Observed live by `TestAccApp`, 2026-10-12:
  - An `STA` app created without `json` has `json` `{}` and no port.
  - An `APA` app created without `json` has `{"php_version": 73}`: the server fills a default, as
    a number. Sending `{"php_version": 83}` on update is accepted and reads back as the number 83.
  - `STA` to `APA` in place works and gains the `php_version` default.
  - `APA` to `CUS` in place changes `type` but assigns **no port** and keeps the stale
    `php_version`. A `CUS` app created as such has a port and `json` `{}`. The resource therefore
    replaces the app on a change to or from `CUS`.
  - The resource manages only the `json` keys the configuration names, so server-filled defaults
    never show as a diff.
- `json` is checked against a schema per type (`TestFindingAppJSON`, 2026-10-12):
  - Created without `json`: `STA` `{}`, no port. `NPF` and `APA` `{"php_version": 73}`, no port.
    `CUS`, `SVN` and `DAV` `{}` and a port. `SLS`, `SLP` and `SLN` are refused, 400
    `[{"json":["sym_link_path is required."]}]`.
  - An unknown key answers 400 `{"errors":["Additional properties are not allowed ('<key>' was
    unexpected)"]}`.
  - `php_version` must be an integer, one of 56, 73, 74, 80, 81, 82, 83, 84, 85 on the server
    tested. A string, even `"83"`, answers 400 `'83' is not of type 'integer'`.
  - `external_port_access: true` was accepted on an `APA` app, so the schema is not strictly per
    type. An update with `{}` changed nothing: `php_version` stayed.
  - Still not known: the complete list of keys.
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
- A create with neither address answers 400 with a message under both fields: `ip4: An IPv4 or
  IPv6 address is required.` and the same under `ip6` (`TestAccSite`, 2026-10-12). The resource
  requires one of the two.
- `disabled: true` takes the site offline completely (runbook).
- Let's Encrypt results appear as entries in `/notice/list/`, not on the site (runbook).
- Observed live by `TestAccSite`, 2026-10-12:
  - A site created with `generate_le: false` and one address has `ip6`, `cert` and
    `primary_domain` null, `redirect` and `disabled` false.
  - **A route `uri` without a trailing slash gets one**: `/two` is stored as `/two/`. The resource
    requires `uri` to start and end with `/`.
  - Creating the site made no DNS record for its domain visible in `/dnsrecord/list/` right after
    the site became ready.
  - Name, domains, routes and `disabled` all update in place.
- A site created with `generate_le: true` for a domain that resolves nowhere
  (`TestFindingGenerateLE`, 2026-10-12): it becomes `READY` as usual, `cert` stays null for the two
  minutes watched, no DNS record appears, and turning `generate_le` off again is accepted. Nothing
  on the site says that no certificate was issued; the runbook's note about `/notice/list/` is the
  only place to look.
- Order of `routes` and `domains`: not relied upon. The resource models both as sets.

### dnsrecord

| | Fields |
|---|---|
| Create `DNSRecordCreate` | `domain`* uuid, `type`* enum, `content`* max 64000, `priority` int, `ttl` int |
| Update `DNSRecordUpdate` | `id`*, `domain`, `type`, `content`, `priority`, `ttl` |
| Response `DNSRecordResponse` | `id`, `state`, `ready`, `domain`, `type`, `content`, `priority` (default 10), `ttl` (default 3600) |
| Delete | `[{"id"}]` |

- Confirmed live by `TestAccDNSRecord`, 2026-10-12: a record created without `priority` and `ttl`
  reads back 10 and 3600; `content` and `ttl` update in place; an `A` and a `TXT` record read back
  the content they were given, the `TXT` one with spaces and without added quotes.
- `DNSRecordTypeEnum`: `A` `AAAA` `CNAME` `MX` `TXT` `SRV` `CAA`.
- The spec lets `domain` and `type` change in place. Keep both RequiresReplace anyway: a record
  that changes type or zone is a different record, and this is the conservative reading.

### mariauser, mariadb, psqluser, psqldb

The two engines have the same shape under different paths (spec; `TestFindingMaria`,
`TestFindingPsql`, `TestAccMaria`, `TestAccPsql`, 2026-10-12).

| | Fields |
|---|---|
| User create | `server`* uuid, `name`* max 32 `^[a-z0-9_-]+$`, `password` max 64, `external` bool |
| User update | `id`*, `password`, `external` |
| User response | `id`, `state`, `ready`, `name`, `server`, `external`, `has_all_password_hashes` |
| Database create | `name`* max 32, `server`* uuid, `charset` enum, `dbusers_readwrite`, `dbusers_readonly` (arrays of user ids) |
| Database update | `id`*, `dbusers_readwrite`, `dbusers_readonly` |
| Database response | `id`, `state`, `ready`, `name`, `server`, `charset`, both user arrays |

- The answer to a user create carries `default_password`, whether or not a `password` was sent.
  No later answer does, and no answer carries `password`.
- A password change through update is accepted (`PENDING_UPDATE`, then ready). Whether the create
  honours a `password` is not established, so the provider sets it again through an update.
- A database created without `charset` gets `utf8` on both engines.
- An update with an empty array removes every user from that list.
- A second database of a name in use answers 400
  `[{"non_field_errors":["Name already in use on this server."]}]`: names are unique per server.
- Deleting a user that a database still names is accepted.
- `charset` and `name` cannot be updated: RequiresReplace.

### mailuser and address

`TestFindingMail` and `TestAccMail`, 2026-10-12. The spec calls an address `VirtualAlias`.

| | Fields |
|---|---|
| Mailbox create | `imap_server`* uuid, `name`* max 32 `^[a-z0-9_-]+$`, `password` max 64, `procmailrc`, `autoresponder_*` |
| Mailbox update | `id`*, `password`, `procmailrc`, `autoresponder_*` |
| Mailbox response | `id`, `state`, `ready`, `name`, `imap_server`, `procmailrc`, `autoresponder_enable` (a boolean, the spec says string), `autoresponder_subject`, `autoresponder_message`, `autoresponder_noreply` |
| Address create | `source`* email, `forwards`* array of emails, `destinations` array of mailbox ids |
| Address update | `id`*, `source`, `destinations`, `forwards` |
| Address response | `id`, `state`, `ready`, `source`, `destinations`, `forwards` |

- The account tested has four IMAP and four SMTP servers in `/server/list/`; a mailbox names one
  IMAP server.
- The answer to a mailbox create carries `default_password`; no later answer does.
- An address has no name and no domain id. Its domain must be a domain of the account: otherwise
  400 `[{"source":["Source address domain not found: <domain>."]}]`.
- A second address with a source in use answers 400
  `[{"source":["Address with this source already exists."]}]`.
- An address may have neither a mailbox nor a forward: the update is accepted.
- **Deleting a mailbox takes it out of every address that delivered to it**, silently: the address
  goes to `PENDING_UPDATE` and has one destination fewer.
- **Filter rules and autoresponder** (`TestFindingMailFilter`, 2026-10-13). A mailbox created
  with none of the fields reads back `procmailrc` `""`, `autoresponder_enable` `false`,
  `autoresponder_subject` and `autoresponder_message` `""`, `autoresponder_noreply` `[]`. All five
  can be set on create and on update, and every read returns them. A field left out of an update
  keeps its value: switching the autoresponder off keeps its subject, message and list, and a
  password change touches none of them. An empty string or an empty list clears a field.
- **`procmailrc` comes back without its trailing newline**: `"# tfacc\n"` is stored as `"# tfacc"`.
- **UNVERIFIED:** whether `autoresponder_message` loses white space around it as well: the probe
  sent none. The resource accepts either answer for both texts, and the filter steps of
  `TestAccMail` send a message that ends in a newline.
- `/mailuser/update_public/` is the mailbox owner's own change, with the current password. The
  provider does not call it.
- For the safety prefix an address is judged by its `source`, and by the names of the mailboxes
  it delivers to.

### osvar (environment variable of shell users)

`TestFindingOSVar`, 2026-10-13.

| | Fields |
|---|---|
| Create | `name`* max 128 `^[A-Za-z0-9_+-]*$`, `content` max 1024, `osusers` array of shell user ids, `global` |
| Update | `id`*, `content`, `osusers`, `global`. No `name`: a renamed variable is another variable |
| Response | `id`, `state`, `ready`, `name`, `content`, `osusers`, `global` |

- A variable has a state like the other kinds: `PENDING_CREATE` and `PENDING_UPDATE`, then
  `READY`. The provider waits.
- A field left out of an update keeps its value.
- `osusers` may be emptied, and a variable may be created with no shell user and no content: it
  then belongs to nobody.
- **A name is not unique**: a second variable with the same name for the same shell user is
  accepted. Which of the two the shell user gets was not looked at.
- The content is returned by every read, in the clear.
- For the safety prefix a variable is judged by its own name and by the shell users it names. A
  global one is refused while the prefix is set, and the tests never create one, so
  **`global = true` is unverified against the live API**.

### cert

`TestFindingCert` and `TestAccCert`, 2026-10-12, with self-signed certificates.

| | Fields |
|---|---|
| Create | `name` max 255 `^[-a-zA-Z0-9_]+$`, `cert`* PEM, `key`* PEM, `intermediates` nullable |
| Update | `id`*, `name`, `cert`, `intermediates`, `key` |
| Response | `id`, `name`, `cert`, `intermediates`, `key`, `exp_date`, `listed_domains`, `is_opalstack_letsencrypt`, `is_opalstack_shared_cert`, `sha1_fingerprint`, `sha256_fingerprint` |

- **A certificate has no `state` and no `ready`**: create and update answer with the finished
  object, and a delete is gone at once.
- **Every read returns the private key.** The client's type has no field for it.
- The `cert` text comes back without the white space around it.
- A key that does not fit answers 400 `{"errors":["Certs or key invalid."]}`.
- Names are not unique: a second certificate of the same name is accepted.
- Certificate, key and name all update in place, and the id stays, so a site keeps its reference.
- **Deleting a certificate that a site serves is accepted**, and the site's `cert` is null
  afterwards.
- `listed_domains` was empty for a self-signed certificate with one DNS name.

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

Validation errors, from `TestFindingErrors`, 2026-10-12. All are 400. A create or update answers
with an **array**, one object per element of the request, mapping a field to a list of messages:

| Request | Body |
|---|---|
| app or DNS record with a `type` outside the enum | `[{"type":["\"XXX\" is not a valid choice."]}]` |
| domain name with invalid characters | `[{"name":["Domain name <name> invalid."]}]` |
| site with an `ip4` id that does not exist | `[{"ip4":["Invalid pk \"<uuid>\" - object does not exist."]}]` |
| an error on no one field | `[{"non_field_errors":["..."]}]` |
| `A` record whose content is no address | `{"errors":["Enter a valid IPv4 address."]}`, an object and not an array |

`errorMessages` in `internal/client/errors.go` reads all of these shapes.

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
- A 403 is `{"detail":"You do not have permission to perform this action."}`, for a list and for
  a read alike (probe 2026-10-12, see "What a scoped token sees"). An object outside the token's
  ACL answers 403, not 404, so `ErrForbidden` is real and distinct from "deleted".

### Lists, headers, embed

- Every list of 17 to 126 objects came back as one bare array. No `Link`, `X-Total-Count` or other
  pagination header, and no rate-limit header of any kind (explore:`list-headers.txt`). Rate limits,
  if any, are invisible until hit.
- `allow: OPTIONS, GET` on a list path.
- `?embed=` works as documented: `server` on osuser and ip, `osuser,server` on app, `domain` on
  dnsrecord, and `domains,ip4` on site (where `domains` becomes an array of objects) each turn the
  UUID into an object. An unknown embed key is ignored silently, with status 200
  (explore:`embed.txt`).

### Names, as surveyed

In an account with 17 shell users, 37 apps, 35 sites and 126 domains: no two shell users, sites or
domains share a name, and no app name is used twice, either within one shell user or across two
(explore:`app.uniqueness.txt`, and `duplicate_names` in each `*.list.txt`). That is consistent with
names being unique account-wide per kind. A duplicate create has since been tried for domains and
apps and was refused (see "Names" under "States and waiting"). Import by name must therefore fail on more than one match rather
than pick one.

### Values seen

- `state` was `READY` and `ready` true on every object of every kind. No pending state was observed.
- App types in use: `APA`, `CUS`, `NPF`, `STA`. DNS record types in use: `A`, `AAAA`, `CNAME`, `TXT`.
- IP: one address, type `"4"`, primary.

## Token ACLs, as the dashboard presents them

Read from the token form of the dashboard on 2026-10-12. This is the UI's model, not yet tested
against the API.

- A token has a **service ACL**: one read and one write switch per kind, for domain, ip,
  dnsrecord, site, address, mailuser, quarantinedmail, app, osuser, osvar, mariadb, mariauser,
  psqldb, psqluser, cert, notice and account. The dashboard describes a switch as "full access to a
  given API endpoint", meaning every object of that kind. A new token has all of them on. There
  is no switch for server, and the write switch for ip cannot be turned on.
- A token also has **individual object ACLs**: a kind, one existing object of that kind, and a read
  and a write box. The dashboard recommends these over service switches.
- The two are alternatives for the same access, not a filter on each other: a service switch that
  is on already covers every object. A token limited to test objects therefore has the service
  switches **off** and object entries for the test objects only.
- The ACL dialog shows the token's key in clear text. Do not read that dialog's text.

### What a scoped token sees

`scripts/probe-token.sh`, 2026-10-12, with a token whose only service switch is ip read and whose
object entries are one shell user (read) and one domain (read and write). Output in
`explore-out/probe-token.txt`.

| Request | Answer |
|---|---|
| `server/list` | 200. There is no switch for server and none is needed |
| `ip/list` | 200, the one address |
| `osuser`, `domain`, `app`, `site`, `dnsrecord` `/list/` | 403 each |
| `read` of the shell user and the domain named in the ACL | 200, with every field, `dkim_privkey` included |
| `read` of a shell user, app, site and domain outside the ACL | 403 each |

- **A list needs the service read switch.** It is refused outright, not filtered down to the
  objects the token may read. Looking an object up by name, and importing by name, therefore need
  service-level read on that kind; by id they work with an object entry alone.
- An object entry does give a read by id, and nothing else was visible.
- An id that does not exist answers 403 as well, not 404 (`scripts/probe-write.sh`, 2026-10-12,
  `explore-out/probe-write.txt`). Without the service read switch, "gone" and "not allowed" look
  the same, so `WaitDeleted` cannot work with such a token.
- **An object entry does not allow a create.** `POST /domain/create/` for a subdomain of the
  domain the token has read and write on answered 403. Creating needs the service write switch of
  the kind, which covers every object of that kind.

With the service read and write switches for domain turned on (same day, same two scripts):

| Request | Answer |
|---|---|
| `domain/list` | 200, every domain of the account |
| `read` of a domain outside the object entries | 200 |
| `read` of a missing id | 404 `{"id":["Not found."]}` |
| `POST /domain/create/` with only `name` | 200, `PENDING_CREATE`, `ready` false, `delegated_dns_zone` false, `is_valid_hostname` true |
| `read` of the new domain | `PENDING_CREATE` on the first read, `READY` about 6 seconds later |
| `POST /domain/delete/` | 200 `{"ok":"Queued for deletion."}` |
| `read` after the delete | 200 `PENDING_DELETE` once, then 404 about 6 seconds later |

The other kinds stayed at 403: a service switch covers its own kind only.

So a token cannot be limited to test objects and still create them. Whatever runs the acceptance
tests holds service-level write on each kind it tests, and the safety prefix in the client is the
only thing between a test and a real object.

## UNVERIFIED

Settled on 2026-10-12 by the `TestFinding*` probes and recorded above: what a failed create looks
like, deleting an object that is still referred to, the 400 bodies, name reuse after a delete, app
settings per type, and `generate_le` on a domain that does not resolve.

Still open:

1. Whether a password set through `osuser/update/` takes effect. Checking it needs a login.
2. The complete list of `json` keys per app type.
3. `generate_le` for a domain that does resolve to the server: when `cert` appears, and whether a
   renewal replaces the certificate object.
4. `delegated_dns_zone` set in place: refused for every name that has an ancestor domain in the
   account, which every test domain has.
5. Names are unique per shell user for apps and account-wide for domains (both seen refused).
   Sites and shell users were not tried.
