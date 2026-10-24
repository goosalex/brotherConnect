# TODO

## Die-cut label heights follow-up

### Claude Code instructions

Please pick up the die-cut label-height suggestions from PR #9 discussion and implement/validate them carefully across both modern and legacy build paths.

#### Scope

- Review the prior suggestions about die-cut label-height handling and identify exactly what code paths they affect.
- Ensure behavior is consistent for:
  - modern Go path (current default toolchain)
  - legacy Go 1.20 path (`-tags legacy`, `go.legacy.mod`)
- Keep shared code compatible with Go 1.20 constraints documented in `docs/macos-legacy.md`.

#### Implementation expectations

- Apply the die-cut height logic changes in the right abstraction layer(s) so both driverless and any fallback print paths behave correctly.
- Verify the logic does not regress continuous media behavior.
- Preserve existing cut behavior (`CutMedia=EndOfPage`, IPP `finishings=60`) unless a test proves a required adjustment.

#### Tests to add (required)

Add/extend automated tests that exercise die-cut height behavior in both code paths:

1. **Unit tests for media parsing / normalization**
   - Cover die-cut dimensions and edge cases (rounding, unit conversion, missing height).
   - Include representative Brother die-cut media entries.

2. **Print option generation tests**
   - Validate generated options/attributes for die-cut labels include expected size/height semantics.
   - Confirm continuous labels are unaffected.

3. **Legacy-tag test coverage**
   - Add tests that run under `-tags legacy` and verify equivalent die-cut behavior where expected.
   - Avoid APIs unavailable in Go 1.20.

4. **Integration-style fixture tests (if present in repo patterns)**
   - Reuse existing fixture strategy to verify end-to-end request shaping for die-cut jobs.

#### CI / commands

Please run and report results for at least:

- `go test ./...`
- legacy-focused test invocation(s), e.g.:
  - `go test -tags legacy -modfile=go.legacy.mod ./...`

If some packages are intentionally excluded for legacy, document exactly which and why.

#### Deliverable

- Open a PR with:
  - code changes
  - tests for modern + legacy paths
  - a short test summary including commands run and outcomes
  - any follow-up gaps explicitly listed
