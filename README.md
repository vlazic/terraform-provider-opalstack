# terraform-provider-opalstack

A Terraform and OpenTofu provider for [Opalstack](https://www.opalstack.com) managed hosting, built
on terraform-plugin-framework. Opalstack is unrelated to Opal (opal.dev).

**Status: work in progress.** The API client and the provider configuration exist and are tested.
No resource or data source is implemented yet. [`docs/plan.md`](docs/plan.md) says what comes next.

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

The unit tests never contact Opalstack.

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
