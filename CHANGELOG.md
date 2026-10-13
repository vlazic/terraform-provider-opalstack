# Changelog

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
