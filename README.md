# terraform-provider-opalstack

A Terraform and OpenTofu provider for [Opalstack](https://www.opalstack.com) managed hosting, built
on terraform-plugin-framework. Opalstack is unrelated to Opal (opal.dev).

**Status: early, version 0.1 (see [`CHANGELOG.md`](CHANGELOG.md)).** It is a personal project, not
an Opalstack product. Twelve resources and six data sources exist, are documented in
[`docs/`](docs/index.md), and pass acceptance tests against a real account. [`PLAN.md`](PLAN.md)
says what is decided and what comes next.

```hcl
terraform {
  required_providers {
    opalstack = {
      source  = "vlazic/opalstack"
      version = "~> 0.1"
    }
  }
}
```

To bring an existing account under Terraform without changing it, set `read_only = true` on the
provider and follow [the adoption guide](docs/guides/adopting-an-account.md).

| Resources | Data sources |
|---|---|
| `opalstack_domain`, `opalstack_dnsrecord`, `opalstack_osuser`, `opalstack_app`, `opalstack_site`, `opalstack_cert`, `opalstack_mariauser`, `opalstack_mariadb`, `opalstack_psqluser`, `opalstack_psqldb`, `opalstack_mailuser`, `opalstack_address` | `opalstack_server`, `opalstack_ip`, `opalstack_osuser`, `opalstack_domain`, `opalstack_app`, `opalstack_site` |

- [`API_FINDINGS.md`](API_FINDINGS.md): what the API does, each fact with its source
- [`spec/openapi.yaml`](spec/openapi.yaml): the OpenAPI schema as served by the API
- [`internal/client`](internal/client): the HTTP client, free of Terraform imports
- [`internal/provider`](internal/provider): the provider

## Requirements

Go 1.25 or newer. Terraform 1.5 or newer, or OpenTofu.

## Development

```sh
make check    # lint, unit tests and a secret scan. Offline apart from fetching tools once.
make install  # build the provider into $(go env GOPATH)/bin
```

`make test` never contacts Opalstack: it drives the provider through a real `terraform` binary
against an in-memory fake API, and skips those tests when no binary is found.

`make testacc` runs the acceptance tests against a real account. They create and delete objects
named `tfacc-...` and need `OPALSTACK_TOKEN`, `OPALSTACK_TEST_DOMAIN_BASE`, `OPALSTACK_TEST_OSUSER`
and `OPALSTACK_TEST_OSUSER_ID`. `make sweep` removes what a failed run left behind. Read
[`CLAUDE.md`](CLAUDE.md) before running either.

### Using a local build

Tell the CLI to use the binary from `make install` instead of a registry. Put this in
`~/.terraformrc` for Terraform and in `~/.tofurc` for OpenTofu, with the output of
`go env GOPATH` in place of `GOPATH`:

```hcl
provider_installation {
  dev_overrides {
    "vlazic/opalstack" = "GOPATH/bin"
  }
  direct {}
}
```

Then, in a directory holding [`examples/provider/provider.tf`](examples/provider/provider.tf), run
`terraform plan` or `tofu plan`. Skip `init`: with an override in effect it is not needed. Both
print a warning that development overrides are in effect, which is the sign it worked.

To keep the override out of your global configuration, save the block to any file and point
`TF_CLI_CONFIG_FILE` at it. Both CLIs honour that variable.

## Configuration

```hcl
provider "opalstack" {
  # token           = "..."      # default: the OPALSTACK_TOKEN environment variable
  # safety_prefix   = "tfacc-"   # refuse writes to objects not named with this prefix
  # safety_osuser_id = "..."     # with safety_prefix: the one unprefixed shell user apps may use
  # request_timeout = "30s"
}
```

## License

[Mozilla Public License 2.0](LICENSE).
