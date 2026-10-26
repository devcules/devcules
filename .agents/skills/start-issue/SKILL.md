---
name: start-issue
description: Start work on a Devcules GitHub issue. Reads the issue, checks the previous module is merged, and creates a users/kiran/<module>-<slug> branch from a fresh main. Use when asked to start, pick up or work on an issue number.
---

# Start an issue

Input: an issue number, e.g. `/start-issue 3`.

1. Run `gh issue view <number>`. Note the module id from the title (e.g. `M0.3`), the scope, the "Depends on" line and the test gate.
2. Check the dependency is done: the previous module's issue must be closed (`gh issue view <previous> --json state -q .state` shows `CLOSED`). If it isn't, stop and say so.
3. Check the working tree is clean (`git status --porcelain` prints nothing). If it isn't, stop and list the changes. Never stash or discard them yourself.
4. Run `git fetch origin`, then `git switch -c users/kiran/<module>-<slug> origin/main`. Make `<module>` the lowercase module id (`m0.3`) and `<slug>` two to four lowercase words from the title, joined by hyphens. If the branch already exists, switch to it instead, then run `git log origin/main..HEAD --oneline` to see what's on it.
5. Check `git config user.email` is not a work address.
6. Reply with: the branch name, a three-line summary of the scope, the test gate as a checklist, and your plan in at most five steps. Then wait for the go-ahead before writing code.
