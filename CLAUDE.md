# CLAUDE.md - terraform-provider-opalstack

## Project

Go Terraform/OpenTofu provider for Opalstack's REST API v1 (`https://my.opalstack.com/api/v1/`),
built on terraform-plugin-framework, terraform-plugin-framework-timeouts and
terraform-plugin-testing. Protocol 6, Terraform >= 1.5 and OpenTofu.

Opalstack is managed hosting. It is unrelated to "Opal" (opal.dev / opalsecurity).

**Read [`API_FINDINGS.md`](API_FINDINGS.md) before designing anything.** It is the record of what
the API does, each fact with its source. Keep it current, and mark anything not proven as
`UNVERIFIED`. [`PLAN.md`](PLAN.md) says where the work stands and what comes next.

## Hard safety rules

The account this provider is developed against is a real production account.
Nothing on it is a test object unless rule 2 says so.

1. **The tokens.** The full-access one lives in `pass` at `Opalstack/tokens/full-access`,
   the scoped test one at `Opalstack/tokens/tfacc`. The agent
   never holds a token: scripts that need one read it from `pass` themselves. The owner runs the
   ones that use the full-access token. The agent may run the ones that use the test token
   (owner's decision, 2026-10-12). Never print, echo, log, commit or write the token anywhere. Do not run `env`,
   `printenv` or `set` unfiltered.
2. **With the full-access token the live API is GET only. No exceptions.** No `POST` of any kind,
   to any path. Writes use the test token only, and only to objects whose name starts with `tfacc-`
   and, for apps and sites, that belong to the sandbox shell user or to a shell user that is itself
   a `tfacc-` test object. The test token's write switches
   are off except while a test of that kind runs (see "The test setup" in `PLAN.md`). The sandbox user and the base
   domain for tests are never modified or deleted; their names come from `OPALSTACK_TEST_OSUSER`
   and `OPALSTACK_TEST_DOMAIN_BASE` and are not written in this repository.
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
  `opalstack_dnsrecord`, `opalstack_cert`, `opalstack_mariauser`, `opalstack_mariadb`,
  `opalstack_psqluser`, `opalstack_psqldb`, `opalstack_mailuser`, `opalstack_address`. Data
  sources: `opalstack_server`, `opalstack_ip`, `opalstack_osuser`, `opalstack_domain`,
  `opalstack_app`, `opalstack_site`.
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
make check      # lint, offline tests and a secret scan. Before every commit; stage first
make test       # unit tests and the fake-API provider tests, offline
make testacc    # acceptance tests against the real account, test token only. RUN=TestAccSite picks one.
                # RUN=^TestFinding runs the probes of API behaviour instead
make sweep      # delete tfacc- objects a failed acceptance run left behind
make docs       # regenerate docs/ from the schemas and examples/
make install    # go install, for dev_overrides
scripts/explore.sh       # read-only API survey with the full-access token. The owner runs it.
scripts/probe-token.sh   # what the test token can see. GET only.
```

## Working here

- Semantic commit messages: `type(scope): description`. No attribution or co-author trailers.
- Do not use the em dash character in anything generated.
- Before calling code work done, run `/simplify` and then `/code-review`, in that order.
