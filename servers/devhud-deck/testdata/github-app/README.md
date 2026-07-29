# Deck GitHub App fixture

This directory is test input only. The manifest deliberately names itself
`do not register` and must not be submitted to GitHub or used as a production
application registration.

The permission set is closed to repository metadata read, contents write,
pull-request write, checks read, and organization members read. Contents write
is used only for the advertised merge and native auto-merge actions. Deck
invokes member/team APIs only for an explicit team-reviewer candidate or
mutation flow. It subscribes only to installation lifecycle events;
pull-request, check, and status webhooks are not refresh inputs.
