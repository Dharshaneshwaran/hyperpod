# HyperPod technical report

LaTeX source for the measured technical report. Produces `main.pdf`, 15 pages.

## Build

```bash
tectonic main.tex
```

`pdflatex main.tex` run twice also works if you prefer a TeX Live installation.
Figures are drawn with TikZ and pgfplots, so no external image files are needed.

## Layout

```text
main.tex                 master file
preamble.tex             packages, plot palette, shared axis style
sections/01-introduction.tex   problem and scope
sections/02-background.tex     llama.cpp RPC and its failure mode
sections/03-design.tex         components, telemetry, scheduler, failure signal
sections/04-implementation.tex Go, Rust, dashboard, tests
sections/05-setup.tex          hardware, software versions, network, method
sections/06-results.tex        telemetry, placement, performance, recovery, loads
sections/07-limitations.tex    what the measurements do not show
sections/08-related.tex        prior work and what is not ours
sections/09-conclusion.tex     summary and next steps
```

## Figures

| Figure | Content |
|---|---|
| 1 | Component and recovery-path diagram |
| 2 | Per-device model weights for four measured configurations |
| 3 | Five-run latency and decode throughput, three configurations |
| 4 | Timeline of the three physical failure experiments |
| 5 | Measured time to a ready model |

## Data provenance

Every number in the report comes from a committed log in the main repository:

- `test/phase5-telemetry-scheduler.log` — telemetry and derived tensor weights
- `test/phase6-three-node-recovery.log` — three-machine to two-machine recovery
- `test/phase7-native-benchmark.log` — five-run benchmarks and both same-endpoint
  failure experiments
- `BENCHMARKS.md` — method, per-run values and stated limitations

Nothing in the report is estimated, projected or averaged across conditions. The
plots show individual runs where only individual runs were measured, and the
text says so where a figure is a single observation.

## Style note

The figure conventions here follow the usual single-column arXiv layout: boxed
axes, light grids, small fonts, one solid series with filled markers against one
dashed series with open markers. Only those conventions are shared; all prose,
data and figure content is original to this project.
