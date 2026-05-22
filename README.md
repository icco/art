# art

Art is an agent for managing my work and personal calendars.

## Design

Art is written in Go. Art has two parts: a server and a CLI.

The goal of this is to block off focus time for me to work on both work and personal projects. An example might be to play music for an hour or to spend two hours designing a new service for work, or maybe making sure I go for a walk before work three times a week.

### Server

The Art server is an API written with Google ADK, chi, and other common Go packages similar to other Go packages from @icco.

The API server lets me upload new projects and habits via API to schedule in the calendar.

Data is stored in Postgres.

There are cron jobs that scrape my google calendars for information of what's scheduled.

We use the latest gemini models through vertex.

### CLI

The CLI is a TUI built with Charm. It lets me view my calendar for the week and add new things I need scheduled to work on.
