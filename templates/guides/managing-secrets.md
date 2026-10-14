---
page_title: "Managing secrets"
subcategory: ""
description: |-
  How to keep the passwords of shell users, database users and mailboxes out of the Terraform state, or protect the state that holds them.
---

# Managing secrets

Four resources carry a password: `opalstack_osuser`, `opalstack_mariauser`, `opalstack_psqluser`
and `opalstack_mailuser`. A password reaches the Terraform state in two ways:

- **one you choose**, given in `password`, which the state keeps in plain text;
- **one the API chooses** when it creates the object, reported once and kept in
  `default_password`, also in plain text.

There are three ways to keep either out of reach. They combine.

| | Choose the password yourself | Let the API choose it |
|---|---|---|
| Kept out of the state | `password_wo` (write-only) | `pgp_key`: only an encrypted copy is kept |
| Kept in a protected state | `password` | `default_password` |

## 1. Write-only passwords: nothing in the state

`password_wo` is sent to the API and never written to the plan or the state.
`password_wo_version` is a number that is kept, and the two are given together. The password is
sent when the object is created, and afterwards only when `password_wo_version` changes: to change
the password, give the new one and raise the number.

```terraform
variable "blog_db_password" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "opalstack_mariauser" "blog" {
  name                = "blog"
  server_id           = data.opalstack_server.web.id
  password_wo         = var.blog_db_password
  password_wo_version = 1
}
```

Opalstack also generates a password of its own when the object is created, and reports it once.
With `password_wo` and no `pgp_key` (section 2) that one is not kept either, so `default_password`
stays null; add `pgp_key` to keep an encrypted copy of it. An empty `password_wo`, for example
from a secret store that answered nothing, is refused at plan time.

`password_wo` conflicts with `password`. To move an existing object from one to the other, replace
`password` with the two new attributes; the next apply sends the write-only password once. Taking
both attributes out of the configuration changes nothing: the object keeps its password.

Write-only attributes need **Terraform 1.11 or newer, or OpenTofu 1.11 or newer**. An older version
refuses a configuration that sets `password_wo`; every other attribute keeps working with it.
`ephemeral = true` on a variable keeps the value out of saved plan files as well.

### A password nobody types

The `random_password` ephemeral resource of the `hashicorp/random` provider makes a new password
on every run and never stores it. That is useful where the same run also hands the password to
the place that needs it, for example a write-only attribute of a secret store's provider:

```terraform
ephemeral "random_password" "shop" {
  length  = 32
  special = false
}

resource "opalstack_psqluser" "shop" {
  name                = "shop"
  server_id           = data.opalstack_server.web.id
  password_wo         = ephemeral.random_password.shop.result
  password_wo_version = 1
}
```

The value is different on every run, but it is sent only when `password_wo_version` changes.
Whatever else receives it must be sent it on the same occasions, so give it the same version
number, or the two drift apart.

## 2. Passwords the API chooses, encrypted to your key

Without a password of your own, the API chooses one on create and reports it once. With `pgp_key`
set, the provider encrypts it to that public OpenPGP key: `default_password` stays null,
`encrypted_default_password` holds the encrypted password, and `key_fingerprint` names the key.

```terraform
resource "opalstack_osuser" "deploy" {
  name      = "deploy"
  server_id = data.opalstack_server.web.id
  pgp_key   = file("${path.module}/ops.asc")
}

output "deploy_password_encrypted" {
  value = opalstack_osuser.deploy.encrypted_default_password
}
```

`pgp_key` takes the key ASCII-armored, as `gpg --export --armor ops@example.com > ops.asc` writes
it, or as base64 of the binary form, as `gpg --export ops@example.com | base64 -w0` prints it. A
private key, an expired or revoked key, and a key that cannot encrypt are refused before anything
is created. Only the private key that belongs to it can read the password:

```shell
terraform output -raw deploy_password_encrypted | base64 -d | gpg --decrypt
```

The key is used once, when the object is created, because that is the only time the API reports
the password. Changing `pgp_key` later encrypts nothing again and replaces nothing. An imported
object has none of the three values. To have a password readable with a new key, set one of your
own with `password_wo` instead.

A shell user gets a `default_password` even when it is created with a password of its own. With
`pgp_key`, that one is encrypted too.

## 3. Protecting the state itself

When a password stays in the state, through `password` or `default_password`, protect the state:

- **OpenTofu** can encrypt the state and plan files itself, with a key from a passphrase or a key
  management service. See the `encryption` block of the `terraform` block in OpenTofu's
  documentation.
- **Terraform** writes the state in plain text. Keep it in a remote backend that encrypts it at
  rest and limits who can read it, such as an S3 bucket with server-side encryption, a Google
  Cloud Storage bucket, or HCP Terraform.

Either way, nobody should be able to read the state who should not know the passwords in it.

## Feeding a variable from a secret store

A variable can be filled from wherever the password lives. These are interchangeable examples;
none of them is required, and the provider knows nothing about any of them.

An environment variable, set by whatever runs Terraform:

```shell
export TF_VAR_blog_db_password='...'
```

[pass](https://www.passwordstore.org/), the standard Unix password manager:

```shell
export TF_VAR_blog_db_password="$(pass show opalstack/blog-db | head -n 1)"
```

The 1Password CLI:

```shell
export TF_VAR_blog_db_password="$(op read 'op://Infrastructure/blog-db/password')"
```

HashiCorp Vault:

```shell
export TF_VAR_blog_db_password="$(vault kv get -field=password secret/opalstack/blog-db)"
```

With `password_wo`, a value fed this way reaches the API and nothing else Terraform writes.
