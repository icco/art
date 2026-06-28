# art

Personal scheduling agent. Blocks focus time on your work + personal Google
Calendars for **projects** (target hours toward a deadline) and **habits**
(recurring practice) — writing new events only, never touching events you made.

It also triages both Gmail inboxes with Gemini: archives bulk mail, drafts
replies, and labels what needs reading or thought. Every action is reversible
and recorded.

Single-user, self-hosted. *(Working on the code? See [AGENTS.md](AGENTS.md).)*

## Install

```sh
brew install icco/tap/art        # the `art` TUI
```

## Run the server

```sh
docker compose up -d db          # Postgres
cp .env.example .env             # fill in — every var is documented inline
make run
```

Link each Google account (browser-consent personal, then work):

```sh
TOKEN="$(gcloud auth print-identity-token --audiences=$OIDC_AUDIENCE)"
curl -s -X POST "http://localhost:8080/oauth/start?account=personal" \
  -H "Authorization: Bearer $TOKEN" | jq -r .url   # open URL; repeat ?account=work
```

Set working hours (minutes past midnight in `ART_TIMEZONE`):

```sh
curl -s -X PUT http://localhost:8080/working-hours \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '[{"slot_kind":"work","day_of_week":1,"start_minute":540,"end_minute":1080}]'
```

## Use the TUI

```sh
ART_API_URL=https://art.example.com ART_API_AUDIENCE="$OIDC_AUDIENCE" art
```

The TUI authenticates as *you* via `gcloud` — no stored secrets. `r` replans,
`t` triages.

## Email triage

Runs hourly after the planner. Archive removes `INBOX` and adds `Art/Archived`
(mail stays in All Mail); replies are saved as **drafts** (Art never sends);
`read`/`thinking`/`keep` are labeled in place. Reversals you make (un-archive,
discard a draft) are detected and fed into the next run.

Gmail uses the `gmail.modify` **restricted scope**, so:

- Re-link both accounts after enabling (the scope was added to the shared
  consent; existing calendar links break until re-linked).
- The consumer account needs you added as a Test User on the OAuth consent
  screen; the Workspace account may need the OAuth client allowlisted by an admin.
- Run `TRIAGE_DRY_RUN=true` first — Art classifies and writes audit rows without
  touching Gmail. Inspect `GET /emails`, then turn it off. All `TRIAGE_*` knobs
  are in `.env.example`.

## Security

**The CLI is safe to distribute.** It ships no secrets — it mints a Google ID
token for *whoever runs it* and sends it as a bearer token. The server enforces
the token audience (`OIDC_AUDIENCE`) and rejects any email not in `OWNER_EMAILS`,
so anyone else just gets `403`. Every request is also rate-limited per client IP
(`RATE_LIMIT_RPM`, default 120) and gets security headers; a **verified**,
allow-listed email is required for all data endpoints.

`/`, `/healthz`, `/oauth/callback`, and `/metrics` are public — restrict
`/metrics` at your reverse-proxy edge.

**Deployment checklist:**

1. Keep secrets out of git (`TOKEN_ENCRYPTION_KEY`, `GOOGLE_OAUTH_CLIENT_SECRET`,
   DB password) — use a secret manager or an untracked `.env`. If any were ever
   committed, rotate them. Rotating `TOKEN_ENCRYPTION_KEY` invalidates stored
   refresh tokens, so re-link both accounts afterward.
2. Restrict `/metrics` at the proxy (scraper / LAN only).
3. Keep Postgres off the public internet; prefer `sslmode=require` across hosts.

## Out of scope for v1

Multi-user, mobile/web UI, push notifications, Sentry/OTel exporters,
service-account domain delegation.
