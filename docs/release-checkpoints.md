# Release checks and records

The release contract is [.forgejo/release-profile.yml](../.forgejo/release-profile.yml).
The rendered [workflow](../.forgejo/workflows/release.yml) builds and publishes
tags matching `v*` on Forgejo.

## Release checks

The portable CI checks are `./scripts/verify-release-checks.sh portable`,
`go vet ./...`, and the Linux/amd64
distribution build. The package contains the static `hjh` and `hjh-tui`
executables, editable extensions, dependency licenses, and a manifest of file
hashes. Both executables embed the tag version and source revision.
The upload directory contains `hjh-linux-amd64.tar.gz` and `SHA256SUMS`.
The release action runs only after all checks and packaging succeed.

Packaging runs with `go run ./scripts/package-linux`; Go's ELF reader checks
that the executables have no interpreter dependency. Python and readelf are
not required on the runner. The optional Python entrypoint invokes the same
Go implementation.

Before tagging, run `./scripts/verify-release-checks.sh reference` on the
documented Linux/amd64 development VM. This mandatory group contains seven
existing integration tests that use native GNU grep or copy `/bin/sh` under a
fixture name. The Forgejo runner provides BusyBox grep and shell, which cannot
execute those GNU options or the renamed-shell fixture. The portable command
runs `go test ./...` excluding exactly this reference group; no test is removed.

Interactive TUI and real-provider observations remain in the linked product
results; these checks do not establish behavior across terminals, providers,
or operating systems. No race, paid API, tmux, or GUI checks run in the workflow.

Test, vet, and build output stays in native Forgejo Actions logs. Failed runs
do not create diagnostic releases. `actionlint` can validate the workflow with
`-ignore 'undefined variable "forgejo"'`, because its GitHub expression checker
does not recognize the Forgejo context.

## v1.0.0

Preparation started on 2026-10-16. Release and CI results are pending.

- The release profile uses `apply-forgejo-go-release-profile`, contract version 2,
  required checksums, native logs, and the automatic Forgejo release token.
- Forgejo reports `16.0.3+gitea-1.22.0`. Native run ZIP logs and job text logs
  for Henji run 173/job 194 returned HTTP 200. That successful release confirms
  `ubuntu-latest`, checkout v4, setup-go v5, and forgejo-release v2 on this server.
- The hjh [environment probe](https://forge.harakara.site/littleisland/hjh/actions/runs/1)
  confirmed Go 1.27.0 and shell/archive/hash tools, and found Python/readelf
  unavailable. The distribution packager now uses Go for this runner.
- Current product observations include [child-agent lifecycle](results/i19-child-agent-control.md),
  [QuickJS tools](results/quickjs-stdlib-tools.md),
  [compaction strategy](results/js-compaction-strategy.md), and
  [model behavior after compaction](results/js-compaction-behavior.md).
- Focused build-info and packaging tests, actionlint with the documented
  Forgejo-context exclusion, and the whitespace diff check passed.
- Independent review found no actionable issue in the profile, workflow,
  packaging implementation, and release instructions. The local pre-tag bundle
  contains 172 hash-verified files, both executable binaries reporting `v1.0.0`,
  and licenses for 40 linked Go dependencies. This local bundle records a dirty
  development tree; the public artifact must come from the clean tag CI build.
- The first [tag CI run](https://forge.harakara.site/littleisland/hjh/actions/runs/2)
  (API run 183/job 204, source `28ddce5`) stopped before vet/build/upload because
  the seven native-tool tests encountered BusyBox grep and shell. No Release
  was published. The profile now makes those tests a mandatory pre-tag
  reference check instead of requiring extra runner packages.
- `./scripts/verify-release-checks.sh reference` passed with Go 1.27.2,
  GNU grep 3.11, ripgrep and `/bin/sh` resolving to Dash. The app group took
  6.155 seconds and the JS runtime group 0.238 seconds. Scratch files used
  `/var/tmp` because the development VM's `/tmp` tmpfs was full.
