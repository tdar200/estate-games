# Security

## Reporting a problem

Please don't open a public issue for a security problem. Report it privately instead:

1. Open the **Security** tab of this repository.
2. Choose **Report a vulnerability**.
3. Describe the problem, how to reproduce it, and what someone could do with it.

Only the maintainers can see the report. We'll reply there, work on a fix with you, and credit you
when it is released, unless you'd rather not be named.

## Supported versions

Only the latest code on `main` is supported. Fixes are not backported to older builds.

## What counts

Estate is often run on a public link, so these are all worth reporting:

- Getting another player's private information: their cards, quiz bets, guesses, or device secret.
- Acting for a seat or a table you don't own, or changing money, dice or cards from a client.
- Getting past the room limits or rate limits, or crashing or freezing the server.
- Reading or writing files outside the server's data folder.

Things that are not security problems: computer players being too easy or too hard to beat, and
bugs in game rules that don't give one player hidden information or control. Please report those
as normal issues.
