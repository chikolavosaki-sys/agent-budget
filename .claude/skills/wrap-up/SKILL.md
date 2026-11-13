---
name: wrap-up
description: End-of-session routine — update roadmap, decisions and research log, summarize what was built and learned, and draft a short build-in-public post. Use when the user says "wrap up", "/wrap-up", or is ending a work session.
---

# Session wrap-up

1. **Verify.** Run `make lint test` (or the commands that exist so far) and report pass/fail honestly.
2. **Update files.**
   - `ROADMAP.md`: tick finished items, write the single next step at the top of "Next".
   - `docs/DECISIONS.md`: add any decision made this session (context, options, choice, why).
   - `research/RESEARCH_LOG.md`: add anything studied.
3. **Summarize for the user** (in the language and style from `CLAUDE.local.md`):
   - What we built or changed (2–4 bullets, with file names).
   - The one main concept learned, explained with an everyday analogy.
   - What is next and roughly how long it will take.
   - Anything risky or unfinished.
4. **Draft one public post** under 280 characters about what was learned today — specific, no hype, no secrets or keys, and nothing the user has not decided to share.
5. Suggest a commit message in Conventional Commits format and remind the user to sign off with `git commit -s` if there are uncommitted changes.
