# HyperPod Release and Submission Checklist

No CI is part of this project. Every validation below is run locally and its
output must be retained.

## Official requirements verified publicly

- [ ] Submit to the Indie Developer track only.
- [ ] Use the official Cerebral Valley submission route.
- [ ] Submit by November 4, 2026, 9:00 PM ET (November 5, 7:30 AM IST).
- [ ] Internal deadline: November 4, 6:00 PM IST.
- [ ] Confirm entrant is at least 18 and otherwise eligible.
- [ ] Identify every team member if not submitting alone.
- [ ] Submission is original/owned and does not infringe third-party rights.
- [ ] Follow all open-source/model licenses.
- [ ] Accept organizer rights concerning submission and demo recordings.

The authenticated form's exact fields, character limits and video format are not
publicly visible. Add them here after opening the participant portal; do not
invent them.

## Repository

- [x] Canonical name is HyperPod / `hyperpod`.
- [x] Public README reflects current behavior.
- [x] `BENCHMARKS.md` contains method, per-run values and limitations.
- [x] `PROTOCOL.md` documents telemetry schema and APIs.
- [x] `THIRD_PARTY_NOTICES.md` includes Llama and llama.cpp notices.
- [x] `Built with Llama` appears in README and dashboard.
- [ ] Choose project source license: Apache-2.0, MIT, or intentionally unlicensed.
- [x] Add final architecture SVG.
- [x] Add final dashboard screenshots.
- [x] Check every README link from a fresh clone.
- [ ] Add repository description and topics (requires owner approval).
- [ ] Confirm no models, secrets, private IP credentials or temporary files are tracked.
- [ ] Confirm `git status` is clean and local HEAD equals `origin/main`.

## Local validation

Run from a fresh clone:

```bash
cd controller
go test -v ./...
go vet ./...
go build -o hyperpod-controller .

cd ../worker
cargo fmt --check
cargo test --locked
cargo clippy --locked -- -D warnings
cargo build --release --locked

cd ../frontend
npm ci
npm run build
npm run lint
```

Expected current state:

- [x] Go: 7 tests pass; vet/build pass (fresh clone `4cb7f4d`).
- [x] Rust: 5 tests pass; clippy/release build pass (fresh clone `4cb7f4d`).
- [x] React: `npm ci`, production build and lint pass; zero warnings after fix.
- [x] Resolve the React `set-state-in-effect` lint warning.
- [ ] Windows package starts on TUF.
- [ ] macOS Rust worker and b11909 RPC server start on M4.

## Functional rehearsal

- [ ] ROG, TUF and M4 register with unique IDs.
- [ ] Dashboard shows measured TUF telemetry.
- [ ] Dashboard shows Mac RAM/battery and unavailable GPU values honestly.
- [ ] Three-machine 3B pipeline reaches `ready`.
- [ ] Actual placement contains ROG, TUF and M4.
- [ ] Long request starts.
- [ ] Stop Mac RPC only; keep Mac agent running.
- [ ] Generation 1 abort is detected.
- [ ] Mac RPC is skipped although heartbeat remains healthy.
- [ ] ROG+TUF generation 2 reaches ready.
- [ ] Same HTTP request returns.
- [ ] Recovery log and final dashboard screenshot are saved.

## Benchmark evidence

- [x] Five-run ROG-only baseline.
- [x] Five-run direct native split baseline.
- [x] Five-run HyperPod split benchmark.
- [x] Native physical same-TUF failure.
- [x] HyperPod physical same-TUF same-request recovery.
- [x] Heterogeneous ROG+TUF+M4 same-request recovery.
- [x] 8B capacity placement.
- [ ] Rerun HyperPod performance benchmark with an untouched raw controller log
      and commit the raw file (the existing performance summary was transcribed).

## Video and screenshots

- [x] Three-machine topology screenshot.
- [x] TUF measured telemetry screenshot.
- [x] Mixed scheduler fallback screenshot (shown on Overview).
- [x] Three-machine actual placement screenshot.
- [ ] Recovered ROG+TUF placement screenshot.
- [x] Native-vs-HyperPod benchmark graphic.
- [x] Architecture SVG.
- [ ] Main 100–120 second demo take.
- [ ] Backup demo take.
- [ ] Verify text is readable at normal player size.
- [ ] Verify audio level and remove notifications/private information.
- [ ] Upload using the format allowed by the authenticated portal.
- [ ] Test the video link in a private browser window if a link is used.

## Submission copy

- [x] Lead beneficiary: student and indie AI builders.
- [x] Short, medium and full drafts prepared in `docs/SUBMISSION_DRAFT.md`.
- [x] Meta open-weight usage explanation.
- [x] Technical implementation explanation.
- [x] Verified metrics and prohibited-claim list.
- [ ] Map drafts to authenticated form fields and character limits.
- [ ] Final human proofreading.
- [ ] Confirm all claims match committed evidence.

## Final release

- [ ] Stop feature development.
- [ ] Complete fresh-clone validation.
- [ ] Commit final screenshots/docs.
- [ ] Create a release tag only after owner approval.
- [ ] Save a local copy of every portal answer.
- [ ] Submit by internal deadline.
- [ ] Verify confirmation email/page.
