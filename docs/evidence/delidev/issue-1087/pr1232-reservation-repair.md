# PR #1232 superseded browser reservation repair

Parent: `cca2f7ff148f451fd2341c4fde05b6059b434846`, branch
`kdy1/delidev-browser-1087-replacement`, 2026-09-30.

The [review finding](https://github.com/delinoio/oss/pull/1232#discussion_r4144983905)
identified an asynchronous Retry/Hide race: a new reservation rejected the old
Hide, but an authority or storage failure before native open left the previous
child above the trusted interface. Reservation now runs on the UI loop, removes
the old view before authorization, requests its native close, and invalidates
late creation callbacks. Storage and authority reads remain on workers.

Focused validation passed:
`cargo test -p delidev-desktop --features desktop-host,custom-protocol failed_retry_releases_old_view_before_authority_or_storage_work`
with the existing pinned CEF cache and shared target directory. The fixture
retains an old presentation, replaces its reservation, rejects the replacement
URL during preparation, and verifies that the old generation is already absent.
It controls native state without launching a real renderer; it does not establish
real browser close/flush or platform acceptance. Git LFS integrity, required
asset preparation, API-client build and frontend build also passed. Full repair
validation is recorded separately after the remaining independent findings.
