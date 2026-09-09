# CLI gaps

Found while deploying Chatwoot end to end from the CLI (HOS-603). Ordered by how
badly each one blocked the task.

## 1. No shell into an app

There is no `hostim exec` / `hostim shell`. Anything that needs a command inside
the container — `rails c`, a one-off migration, a database dump — has to go
through the bastion by hand.

Concretely: the Chatwoot template's own setup notes tell the user to shell in and
create the first admin with `rails c`. That instruction cannot be followed with
the CLI. The workaround was to flip `ENABLE_ACCOUNT_SIGNUP` to `true`, register
through the browser, and flip it back.

The bastion host is already exposed (`hostim regions get` prints it), so the
pieces exist.

Hit again while building the FreshRSS template (2026-09-09), for a different
reason: inspection, not command execution. The image's cron job writes its
actualize output to `/tmp/FreshRSS.log` inside the container, not to stdout, so
`hostim logs freshrss` stayed empty while cron was in fact working. Confirming
the `CRON_MIN` env var did anything meant reaching for the cluster directly to
read a file. Any app that logs to a file rather than stdout is unverifiable from
the CLI.

## 2. No way to see domains or their certificate state — DONE (2026-09-09)

`hostim domain status <app>` (aliases `ls`, `list`) is backed by `GetDomainStatus`
and prints domain, pointing, expected IP, resolved IPs. There is still no
certificate state — the API does not expose it.


`hostim domain` has only `add` and `rm`. There is no `ls` and no `status`, even
though the API has `GetDomainStatus`. To confirm a domain was attached you have
to read the `Domains` row of `hostim apps get`, and to find out whether the
certificate was issued you have to reach for `openssl` or `curl`.

Add `domain ls` and `domain status`, the latter backed by `GetDomainStatus`.

## 3. The ingress IP is hard to find — DONE (2026-09-09)

`domain add` now prints the expected A record after adding, and warns when the
domain does not resolve there yet.


A custom domain needs an A record pointing at the region's ingress IP. That IP is
printed only by `hostim regions get <region>` — not by `regions ls`, and not by
`domain add`, which is where the user actually needs it.

`domain add` should print the record it expects: name, type A, and the target IP.
Ideally it also warns when the domain does not resolve there yet.

## 4. Missing fields in list and get output — DONE (2026-09-09)

`apps ls` has a DOMAIN column. `installedExtensions` lives on `PostgresStatus`,
not `Postgres`, so it is already printed by `db postgres status <name>`; nothing
to add to `get`.


- `apps ls` does not show the built-in domain, so there is no way to get an app's
  URL without a second `apps get` call per app.
- `db postgres get` shows the requested `Extensions` but not the
  `installedExtensions` the API returns. Chatwoot installs `pg_trgm` and `vector`
  through its own migrations, and the CLI cannot show that.

## 5. Inconsistent app targeting — PARTLY DONE (2026-09-09)

`--app` now has the `-a` shorthand everywhere (`domain add/rm`, `env`). The
positional-vs-flag split is unchanged.


Three different conventions for naming the app a command acts on:

- `hostim logs chatwoot`, `hostim status chatwoot` — positional argument
- `hostim domain add <domain> --app chatwoot` — `--app` flag
- `hostim env set KEY=VALUE --app chatwoot` — `--app` flag with no `-a` shorthand

Pick one. At minimum add `-a` as the shorthand for `--app` everywhere it exists.

# CLI gaps (FreshRSS template, 2026-09-09)

Found while adding the FreshRSS entry to `backend/templates.yml` and verifying it
with a real deploy on hostim.dev. Gap 1 above was hit again; these three are new.

## 6. `templates apply -f` rejects the list form of `templates.yml` — DONE (2026-09-09)

`-f` accepts a list; `--id` picks the entry (a single-entry file needs no `--id`).


`loadTemplateFile` (`internal/cmd/templates.go:885`) aborts with
`"contains a list of templates; --file expects a single template object"`. But
the file an author edits — `backend/templates.yml` — is exactly that list, so
testing a freshly written entry means hand-copying it into a throwaway
single-template file first. That is the normal workflow for every new template,
not an edge case.

Accept a list and add `--id <name>` to pick the entry out of it.

## 7. No local `templates validate` — DONE (2026-09-09)

`hostim templates validate -f <file> [--id x]` runs `checkPlans` + `validateNames`
offline over every entry.


The only validation is `checkPlans`, called from inside `apply`
(`internal/cmd/templates.go:266`) — that is, against production. A malformed
template is discovered by a half-created project that then has to be cleaned up.

Add `hostim templates validate -f <file>` that accepts the list form, runs the
same checks offline, and makes no API call. It is the pre-commit check that does
not exist today.

## 8. `--region` should default when creating a project — DONE (2026-09-09)

Defaults to the only region the API lists; errors with the choices when there is
more than one.


`templates apply --new-project` fails with `--region is required with
--new-project` (`internal/cmd/templates.go:618`). There is one common region and
the config already resolves a current project. Default the region and let
`--region` override it.
