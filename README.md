# art

Personal email triage agent. It triages your work and personal Gmail
inboxes with Gemini Flash, under a daily spend cap: archives bulk mail and labels
what needs a reply, reading, or thought. Its only actions are applying labels
and archiving — it never drafts, sends, or deletes mail. Message bodies are read
only from the inbox. Every action is reversible and recorded.

Single-user, self-hosted. *(Working on the code? See [AGENTS.md](AGENTS.md).)*

## Install

```sh
brew install icco/tap/art        # the `art` TUI
```

## Run the server

```sh
docker compose up -d db          # Postgres
cp .env.example .env             # fill in — every var is documented inline
task run
```

Link each Google account (browser-consent personal, then work):

```sh
TOKEN="$(gcloud auth print-identity-token)"
curl -s -X POST "http://localhost:8080/oauth/start?account=personal" \
  -H "Authorization: Bearer $TOKEN" | jq -r .url   # open URL; repeat ?account=work
```

`gcloud` user ID tokens carry gcloud's own OAuth client ID as their audience
(a user token can't carry an arbitrary one), so set `OIDC_AUDIENCE` to that
client ID — see `.env.example`.

## Settings

`GET`/`PUT /settings` edits the knobs Art reads on every run: the triage
switches (enabled, dry run, confidence threshold, backfill and reconcile days)
and the daily LLM budget. The env vars above seed them, so nothing changes until you write
a value; after that the stored value wins, no redeploy needed.

Only those keys exist. Owner emails, the OIDC audience, the encryption key, the
database URL and the Google/Vertex credentials stay env-only, and so does
`ART_TIMEZONE`, which sets the day boundary for the daily budget.

## Use the TUI

```sh
ART_API_URL=https://art.example.com art
```

The TUI authenticates as *you* via `gcloud` — no stored secrets. `t` triages,
`2` shows the triage digest and `3` edits the settings above.

## Email triage

Runs every 30 minutes over **inbox mail only**. Archive removes `INBOX`
and adds `Art/Archived` (mail stays in All Mail); mail needing a response is
labeled `Art/Reply` for you to handle (Art never writes the reply); `keep` is
left in place with `Art/Triaged`. Labeling and archiving are the only actions
Art takes — it never drafts, sends, or deletes mail. Mail you have labeled
`mailinglist` is never auto-archived, however confident the classifier is — it
still gets triaged and labeled, just left in the inbox. **Responses to mail you
sent are never auto-archived.** Before archiving, Art checks the conversation's
message labels and keeps it if any message is marked `SENT`, regardless of the
classifier's confidence. This protects the whole conversation, including mail
sent from aliases, without reading sent-message bodies. If the check fails,
Art leaves the message untouched for a later retry. Decisions you reverse via
the TUI are fed back into the next run as corrections.

Gmail uses the `gmail.modify` **restricted scope**, so:

- Re-link both accounts after enabling (the scope was added to the shared
  consent; existing links break until re-linked).
- The consumer account needs you added as a Test User on the OAuth consent
  screen; the Workspace account may need the OAuth client allowlisted by an admin.
- Run `TRIAGE_DRY_RUN=true` first — Art classifies and writes audit rows without
  touching Gmail. Inspect `GET /emails`, then turn it off. All `TRIAGE_*` knobs
  are in `.env.example`.

## Security

The CLI is safe to distribute — it ships no secrets, minting a Google ID token
for *whoever runs it*. The server enforces the token audience (`OIDC_AUDIENCE`)
and only serves emails in `OWNER_EMAILS`; anyone else gets `403`. Requests are
rate-limited per client IP (`RATE_LIMIT_RPM`, default 120), and a verified,
allow-listed email is required for all data endpoints. `/`, `/healthz`,
`/oauth/callback`, and `/metrics` are unauthenticated.

## Out of scope for v1

Multi-user, mobile/web UI, push notifications, Sentry/OTel exporters,
service-account domain delegation.
