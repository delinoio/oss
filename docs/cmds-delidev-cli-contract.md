
## Dedicated native review — issue #2019

`session native-review create --id SESSION --revision REVISION --input TARGET.json` admits a dedicated Codex review. The closed target document contains `kind` (`uncommitted`, `base-branch`, `commit` or `custom`), `repository_id`, the original working-tree `diff_revision`, and only its applicable `reference` or `instructions`. Obtain the repository and diff revision from the original session workspace read. Creation returns an independent queued review job; it does not wait for native completion or create conversation input.

`session native-review get --id SESSION --job-id JOB` reads that original job's lifecycle, frozen target, findings, usage and uncertainty. Stop and Archive retain existing original-session controls; they do not retry a review or prove its native completion. A lost response requires observation of the original request/job. Ordinary `session review` feedback behavior remains unchanged. These commands never publish GitHub feedback, apply findings or merge changes.
