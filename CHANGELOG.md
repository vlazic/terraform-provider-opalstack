# Changelog

## 0.3.1 (2026-10-15)

- `opalstack_app`: an update no longer deletes the app's settings that the configuration does not
  name. The API replaces the whole `json` on an update, and 0.3.0 sent the configured `json` on
  every update of an app that has the attribute, a change of `type` or `timeouts` alone included,
  so a setting made in the control panel (`gzip`, `expires`) was lost then. A key the
  configuration stopped naming was dropped the same way, and the API put back its own default:
  for `php_version` that is 73, a quiet PHP downgrade. Now `json` is sent only when a configured
  key is new or has a new value, and the resource reads the app first and sends its settings back
  with the configured keys over them. A key removed from `json` is no longer managed and keeps its
  value; to turn a setting off, set it (`external_port_access = false`) or use the control panel.
- `opalstack_app`: a change from a symlink type (`SLS`, `SLP`, `SLN`) to another type drops
  `sym_link_path`, which no other type takes.

## 0.3.0 (2026-10-14)

- `opalstack_osuser`, `opalstack_mariauser`, `opalstack_psqluser` and `opalstack_mailuser`:
  `password_wo` and `password_wo_version`, a write-only password that is sent to the API and never
  kept in the plan or the state. It is sent on create and again only when `password_wo_version`
  changes. Needs Terraform or OpenTofu 1.11 or newer; `password` stays and works as before. With
  `password_wo` and no `pgp_key`, the password the API generates on create is not kept either. An
  empty `password_wo` is refused at plan time.
- The same four: `pgp_key`, a public OpenPGP key. The password the API chooses on create is then
  kept only encrypted to that key, in `encrypted_default_password`, with `key_fingerprint`, and
  `default_password` stays null. A key that is private, expired, revoked or cannot encrypt is
  refused before anything is created.
- No token resource: the API refuses to create tokens and to set their permissions, so tokens
  are made in the Opalstack control panel.
- New guide "Managing secrets": write-only passwords, `pgp_key`, protecting the state, and feeding
  a password from a secret store.
- A configuration and a state written by 0.2.0 plan no change after the upgrade: every new
  attribute is null unless the configuration names it.

## 0.2.0 (2026-10-14)

- New resource `opalstack_osvar`: an environment variable of shell users. `global = true` has not
  been verified against the live API.
- `opalstack_osuser`: an optional `password`, changed in place. Verified by logging in over SSH.
- `opalstack_mailuser`: `procmailrc`, `autoresponder_enable`, `autoresponder_subject`,
  `autoresponder_message` and `autoresponder_noreply` manage a mailbox's filter rules and
  autoresponder. Each one that the configuration leaves out is never sent and keeps what the API
  has, so a configuration written for 0.1 plans no change after the upgrade. The data source
  `opalstack_mailuser` reports the same five.
- New data sources: `opalstack_dnsrecord`, `opalstack_cert`, `opalstack_mailuser`,
  `opalstack_address`, `opalstack_mariadb`, `opalstack_mariauser`, `opalstack_psqldb`,
  `opalstack_psqluser`.
- An update the API refuses because the object is still settling (400 `Not ready`) now waits for
  the object and is sent once more. It happened to a mailbox whose address changed in the same
  apply.
- The content of an environment variable is kept out of the provider's debug log and out of the
  body of an API error.
- With `safety_prefix` set, an update or a delete of a variable is refused when the variable is
  global already, or is given to a shell user that is neither prefixed nor the one
  `safety_osuser_id` names, whatever the request itself says.

## 0.1.2 (2026-10-13)

The first public version, under the Mozilla Public License 2.0. No change to the provider itself.

## 0.1.1 (2026-10-12)

- `opalstack_cert`: `intermediates` left out of the configuration is now kept as the API has it.
  Before, adopting a certificate with a configuration that did not name its chain planned to
  remove the chain.

## 0.1.0 (2026-10-12)

The first version. It was a private release.

**Resources**

- `opalstack_osuser`, `opalstack_domain`, `opalstack_dnsrecord`, `opalstack_app`, `opalstack_site`
- `opalstack_cert`
- `opalstack_mariauser`, `opalstack_mariadb`, `opalstack_psqluser`, `opalstack_psqldb`
- `opalstack_mailuser`, `opalstack_address`

**Data sources**

- `opalstack_server`, `opalstack_ip`, `opalstack_osuser`, `opalstack_domain`, `opalstack_app`,
  `opalstack_site`

**Provider settings**

- `read_only`: nothing but reads is sent. For adopting an existing account; see the guide
  "Adopting an existing account".
- `safety_prefix` and `safety_osuser_id`: writes are refused unless the object, and what it refers
  to, carry a name prefix. For development against an account that holds real objects.
- `request_timeout`, `base_url`, `token`.

**Known limits**

- A shell user's password cannot be set; the API's own is reported once.
- A mailbox's filter rules and autoresponder are not managed.
- `opalstack_app.json` manages only the keys the configuration names.
- Environment variables of a shell user (`osvar`), quarantined mail and notices have no resource.
