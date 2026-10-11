# CLAUDE.md - terraform-provider-opalstack

## Project

Go Terraform/OpenTofu provider for Opalstack's REST API v1 (`https://my.opalstack.com/api/v1/`),
built on terraform-plugin-framework, terraform-plugin-framework-timeouts and
terraform-plugin-testing. Protocol 6, Terraform >= 1.5 and OpenTofu.

Opalstack is managed hosting. It is unrelated to "Opal" (opal.dev / opalsecurity).

**Read [`API_FINDINGS.md`](API_FINDINGS.md) before designing anything.** It is the record of what
the API does, each fact with its source. Keep it current, and mark anything not proven as
`UNVERIFIED`. [`docs/plan.md`](docs/plan.md) says where the work stands and what comes next.

## Hard safety rules

The account this provider is developed against is a real production account.
Nothing on it is a test object unless rule 2 says so.

1. **The token.** It lives in `pass` at `Opalstack/tokens/full-access`. The agent does
   not run `pass` and never holds the token: scripts that need it read it themselves, and the owner
   runs them. Never print, echo, log, commit or write the token anywhere. Do not run `env`,
   `printenv` or `set` unfiltered.
2. **While the full-access token is the only one, the live API is GET only. No exceptions.** No
   `POST` of any kind, to any path. This lifts only when the owner has created a sandbox shell user
   and a token whose ACLs are scoped to it, and has said so. From then on, writes are allowed only
   to objects whose name starts with `tfacc-` and, for apps and sites, that belong to the sandbox
   user. The sandbox user itself is never modified or deleted.
3. Never call `/login/`, `/register/`, `/payment/*`, `/token/*` or `/tokenacl/*`. The provider never
   manages tokens.
4. **Nothing from the real account goes into this repo**: no hostname, IP, domain, shell user name,
   UUID, email or password. Real account state is documented in
   the owner's private runbook, outside this repository; read it for context, copy nothing from it. Use
   `opalN.opalstack.com`, `example.com` and all-zero or `1111...` UUIDs as placeholders.
5. Redact any JSON field matching `/password|secret|key|token/` before displaying an API response.
6. Never commit Terraform state, `.terraform/`, crash logs, `.env`, `_ref/` or `explore-out/`.
7. Keep to about 3 requests per second and back off on 429 and 5xx. Acceptance tests run serially.

## Conventions

- Resources: `opalstack_osuser`, `opalstack_domain`, `opalstack_app`, `opalstack_site`,
  `opalstack_dnsrecord`. Data sources share those names, plus `opalstack_server` and `opalstack_ip`.
  Attributes that reference another object end in `_id`.
- `internal/client` imports nothing from Terraform. It sends one-element JSON arrays for create and
  update, `[{"id": ...}]` for delete, and waits for `ready == true`.
- Read on not-found removes the resource from state. Read on 403 is an error and never removes it:
  an ACL denial is not a deletion.
- Immutable fields use `RequiresReplace`; stable computed fields use `UseStateForUnknown`.
- Secrets are `Sensitive: true`.
- Test names are `tfacc-` plus six random lowercase alphanumerics. Sweepers match the prefix
  strictly and skip anything ambiguous.
- Unit-test fixtures are written by hand from the spec. Never capture one from the live account.

## Commands

```sh
make build      # go build ./...
make test       # unit tests, offline
make lint       # golangci-lint
make install    # go install, for dev_overrides
scripts/explore.sh   # read-only API survey into explore-out/. Needs pass: the owner runs it.
```

## Working here

- Semantic commit messages: `type(scope): description`. No attribution or co-author trailers.
- Do not use the em dash character in anything generated.
- Before calling code work done, run `/simplify` and then `/code-review`, in that order.
