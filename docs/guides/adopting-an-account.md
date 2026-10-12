---
page_title: "Adopting an existing account"
subcategory: ""
description: |-
  How to bring the objects of an existing Opalstack account under Terraform, piece by piece, without changing any of them.
---

# Adopting an existing account

An account that was built by hand in the dashboard can be brought under Terraform one service at a
time. Nothing has to be recreated: each object is imported, its configuration is generated, and the
work is done when `terraform plan` reports no changes.

## Work read-only until the plan is empty

Set `read_only = true` on the provider while adopting. The provider then sends nothing but reads:
planning, refreshing and importing work, and every create, update and delete fails before a
request is made. A mistake in a half-written configuration cannot touch the account.

```terraform
provider "opalstack" {
  read_only = true
}
```

Remove the setting only when the plan for everything you imported says `No changes`.

## What to import, in which order

Import a service from the bottom up, so that every reference can be written against an object that
is already in the configuration:

1. the shell user (`opalstack_osuser`),
2. the domains (`opalstack_domain`),
3. the apps (`opalstack_app`),
4. the site that connects them (`opalstack_site`),
5. DNS records (`opalstack_dnsrecord`), if you want them managed.

Shell users, domains, apps and sites can be imported by name or by UUID. A DNS record has no name
and is imported by UUID only.

An import by name lists every object of that kind, so the token needs read access to the whole
kind. If a name is used more than once, the import fails and asks for the UUID.

## Generate the configuration

Write one `import` block per object and let Terraform write the resource blocks:

```terraform
import {
  to = opalstack_osuser.files
  id = "files"
}

import {
  to = opalstack_domain.files
  id = "files.example.com"
}

import {
  to = opalstack_app.files
  id = "files"
}

import {
  to = opalstack_site.files
  id = "files"
}
```

```shell
terraform plan -generate-config-out=generated.tf
```

Read `generated.tf` before going on, and tidy it:

- It holds literal UUIDs where the objects refer to each other. Replace them with references, such
  as `osuser_id = opalstack_osuser.files.id`, so that Terraform knows the order of things.
- Delete the lines that say `= null`, such as `timeouts`, `json` and `installer_url`.
- On a site with `generate_le = true`, delete `cert_id`. A renewal can attach a new
  certificate, and a literal id in the configuration would then make Terraform put the old one back. Left
  out, the attribute follows whatever the API attaches.

Then run `terraform apply` to record the imports in state (with
`read_only = true` this changes nothing on the account), and `terraform plan` again. It must say
`No changes`.

## What does not come back from an import

- **A shell user's password.** `default_password` is reported once, when the provider creates a
  user. It is empty for an imported one.
- **App settings.** `json` on `opalstack_app` manages only the keys the configuration names. An
  imported app starts with none, so its settings are left as they are until you name the ones you
  want managed.
- **`installer_url`** is kept as the API reports it. Changing it replaces the app, and so does a
  new name or another shell user, because the API cannot change those.
- **Private keys.** The API returns a domain's DKIM private key with every read. The provider never
  stores it.

## What to watch for afterwards

- A site sends its whole route list on every change. Keep all routes of a site in the one resource.
- Deleting an app deletes its directory on the server. Before removing the `read_only` setting,
  check that the plan has no replacement in it: `terraform plan` marks one with `-/+`.
- To stop managing an object without deleting it, use a `removed` block with
  `lifecycle { destroy = false }`.
