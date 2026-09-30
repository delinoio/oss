# Issue #1095: original generation after confirmed pre-native failure

[PR #1233 feedback](https://github.com/delinoio/oss/pull/1233#discussion_r4144990750)
correctly identified destructive cleanup after a failed execution that had not
gained native ownership. On revision `a00176407`, new temporary Connect/SQLite
fixtures reproduced deletion of the original vault generation with no queued
operation, queued refresh and queued logout. Missing bundle evidence also deleted
the blocked generation, while changed bytes were incorrectly accepted as an
acknowledged failed completion.

The Worker now returns byte-identical unused original material over protected
Finish only after unchanged-file removal and retained-file scanning confirm
pre-native cleanup. This includes a definitively closed failed Open. It keeps
success and refresh false; native success is not inferred.

The server compares these bytes with the original immutable vault generation
before releasing that execution lease. It preserves the generation, connection,
health and independently queued operation. Missing/changed material or absent
cleanup retains recovery ownership and blocked original material. Explicit logout
keeps its separately authorized removal behavior. The existing wire fields encode
this closed outcome; no schema number, migration or generated declaration changes.

Executed checks after the fix:

- `GOMAXPROCS=4 go test -race -p 1 -timeout=10m
  ./cmds/delidev-cli/internal/server -run 'Subscription' -count=1`: passed
  (86.115-second package). Positive fixtures acquire the preserved generation
  through the next authorized refresh/logout; negative fixtures cannot release
  its ownership. Existing lifecycle, revocation and late-Finish controls pass.
- `GOMAXPROCS=4 go test -race -p 1 -timeout=5m
  ./cmds/delidev-cli/internal/worker
  -run '^TestManagedExecutionPreNativeFailureRemovesAuthentication$' -count=1`:
  passed (5.962-second package), including publisher/registration/failed-Open
  cleanup and uncertain protected Take/Finish cases, with original bytes returned
  only on the confirmed pre-native path.

These are controlled fixture results. The final composed implementation still
requires its own complete validation; real-account, platform and release
acceptance and complete uncertain-lease recovery remain separate.
