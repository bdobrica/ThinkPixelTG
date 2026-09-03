# GitHub pull comment retry qualification

Status: reference connector qualification for `github.pull.comment`; reviewed 2026-09-03

## Operation and classification

The connector creates an issue comment on a pull request with
`POST /repos/{owner}/{repo}/issues/{issue_number}/comments`. The immutable tool
version MUST declare `side_effect: true` and `retry_class: non_retryable`.

GitHub's [create-comment endpoint](https://docs.github.com/en/rest/issues/comments#create-an-issue-comment)
documents no native idempotency key. Its [REST API guidance](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api#use-conditional-requests-if-appropriate)
also says conditional requests for unsafe methods are unsupported unless an
endpoint specifically documents support. ThinkPixelTG does not synthesize or
send an idempotency header, and it does not claim gateway deduplication or
authoritative reconciliation. The operation therefore cannot be automatically
replayed after request transmission may have begun.

## Outcome mapping

- Local schema, resource, credential, and request-construction failures occur
  before send and return a connector error.
- HTTP `201 Created` is `confirmed_success`.
- Provider-documented HTTP `403`, `404`, `410`, and `422` rejection responses,
  plus a `429` rate-limit rejection, are `definitely_rejected`; no provider
  body is exposed.
- Any other response status, transport failure, timeout, or cancellation after
  the secured client is invoked is `unknown`, even when no response was received.

An `unknown` outcome MUST enter the gateway's ambiguous/manual-review flow and
MUST NOT be blindly retried. Repeating the logical call at the HTTP or worker
layer can create a duplicate comment.

## Evidence and residual risk

On success the bounded result retains only the repository, pull number, GitHub
comment ID, public comment URL, and creation timestamp. Provider request IDs and
additional bounded evidence are deferred to CRED-011. There is no deterministic
reconciliation query because the endpoint accepts no caller-selected unique
comment identifier; searching comment text is neither unique nor authoritative.
The only compensating action is a separately authorized deletion of a confirmed
comment ID, which this connector does not implement.

Hermetic tests cover pre-send validation, secret-header lifetime, confirmed
success, provider-response minimization, and ambiguous transport/`5xx` outcomes.
