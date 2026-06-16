Looking at the current lifecycle, you already have the core destructive/publishing spine:

`login`, `deploy`, `revisions`, `rollback`, `delete`

The commands I’d add next are:

`quack sites`
List published sites, with current version, owner/publisher, file count, byte count, update time, runtime status, and policy reason. The admin page already has most of this data; the CLI needs the same inventory view.

`quack status <site>`
Show the current deployed version, whether it is active/blocked by policy, when it was published, who published it, and basic serving health. This is the first command people reach for during maintenance.

`quack inspect <site> [--version N]`
Show a site manifest: files, sizes, hashes, upload settings, runtime flags, maybe policy violations. This answers “what exactly is live?”

`quack validate <directory>`
Run the archive/path/file-count/size/policy checks without uploading. This is different from deploy because it gives fast local feedback before touching server state.

`quack diff <directory> <site> [--version N]`
Compare a local directory against the current or chosen deployed revision: added, removed, changed files, size delta. Very useful before deploys and rollbacks.

`quack deploy --dry-run <directory> <site>`
Server-side validation without publishing. This catches limits and policy checks that local validation cannot know.

`quack rollback <site> --to <version>`
Current rollback appears to mean “go to previous older revision.” That is useful, but lifecycle maintenance usually needs explicit rollback to a known-good version.

`quack unpublish <site>`
Take a site offline without deleting its history/blobs. This is the missing middle state between “live” and “gone.”

`quack publish <site> --version N`
The inverse of unpublish/rollback: make a retained revision current. You could fold this into `rollback --to`, but `publish --version` reads better when moving forward again.

`quack prune <site> --keep N`
Retention exists internally via `max_retained_versions`, but manual cleanup is a normal maintenance action. It should report which versions would be removed, ideally with `--dry-run`.

`quack export <site> [--version N]` and `quack import <archive> <site>`
Needed for backup, migration, disaster recovery, and moving sites between environments.

`quack doctor`
Check server reachability, auth, configured limits, storage/database health if exposed, and whether the current CLI config is valid.

`quack audit <site>` or `quack events <site>`
Show lifecycle events: deploys, rollbacks, deletes, policy changes, publisher, timestamps. This becomes important as soon as more than one person can deploy.

I’d treat these as the minimum coherent lifecycle:

```text
discover:    sites, status
prepare:     validate, diff, deploy --dry-run
publish:     deploy
observe:     status, inspect, audit
recover:     revisions, rollback --to, publish --version
suspend:     unpublish
cleanup:     prune, delete
backup:      export, import
diagnose:    doctor
```

The most urgent additions are probably `sites`, `status`, `validate`, `diff`, `rollback --to`, and `unpublish`. Those close the biggest operational gaps without expanding into full server administration.
