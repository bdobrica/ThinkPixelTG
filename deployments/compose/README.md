# Local Compose environment

From the repository root, start PostgreSQL with `make compose-up`. Add optional
services with `COMPOSE_PROFILES=cache,governance make compose-up`. Ports bind only
to loopback and can be overridden through a private `.env` copied from
`.env.example`; that file must not be committed.

The default passwords are deliberately local-only. Set replacements when the
host is shared. AG listens on `58081`, GR on `58082`, PostgreSQL on `55432`, and
Valkey on `56379` by default. The fakes validate bounded JSON and strict known
fields, echo contract correlation/digest fields, and return deterministic allow
semantics suitable only for local contract tests.

Stop services with `make compose-down`. Use `make compose-clean` to also delete
the project volumes and orphan containers; this destroys only the named
`thinkpixeltg-dev` local Compose data.
