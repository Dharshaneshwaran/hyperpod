# HyperPot — Rubric Strategy Note (ADDRESS LATER)

> Status: PARKED. This is recorded now to act on after the core build (Phase 2/3).
> It does NOT need to be done yet. It is the plan for making the submission score well.

## Why this note exists
The official judging rubric (verified from the hackathon slide, 26 Oct 2026) weights
the score like this:

| Weight | Criterion            | What they want                                             |
|--------|----------------------|------------------------------------------------------------|
| 30%    | Impact               | Real-world potential, who benefits, fit with a problem stmt |
| 25%    | Use of Meta AI tooling | **Creative use beyond a basic integration**               |
| 20%    | Technical depth      | Sound, refined engineering with real craft                 |
| 25%    | Demo                 | A working demo that holds up + a demo video worth watching |

Scored 0–10 per criterion; same rubric in Stage 2 and the final round.

## Where HyperPot stands (honest self-assessment)
- **Technical depth (20%) — STRONG.** Fault tolerance, heartbeats, failover,
  Go+Rust+llama.cpp. This is our home turf. Already real in Phase 1.
- **Demo (25%) — STRONG.** "Kill a laptop, AI keeps talking" is tailor-made.
- **Impact (30%) — WEAK / needs work.** Biggest weight. Need a crisp
  problem statement + specific beneficiary.
- **Use of Meta tooling (25%) — WEAK.** We currently just run one Llama model =
  "basic integration", which the rubric explicitly says is NOT enough.

Combined, the two weak areas (Impact + Meta use) are **55%** of the score.
The engineering alone will not win. These two must be strengthened.

## Fix 1 — Creative Meta use (25%): tiered Meta models + Meta API fallback
Move from "basic integration" to "creative use" by using Meta tooling THREE ways,
tied to the fault-tolerance engine:
1. **Local Meta Llama** (open-weights GGUF) runs on the laptop pool normally.
2. **Meta Model API fallback** ($150 hackathon credits): when the pool degrades
   (a node dies), HyperPot can fall back to the cloud Llama to keep serving, then
   switch back to local when the pool heals.
3. **(Optional) Multi-model speculative decoding:** small Llama drafts on each
   node, larger Llama split across the pool verifies.

Why it scores: three distinct Meta usages, fused with the core feature. Hits the
rubric's exact words ("creative use beyond a basic integration"). The fallback
alone (1 + 2) already upgrades us from "basic" to "creative".

Dependency: requires Meta Model API access/credits. If unavailable, fall back to a
smaller LOCAL Llama instead (less flashy, still valid).

## Fix 2 — Impact (30%): one sharp beneficiary + problem statement
Problem statement (one line):
> "Schools, clinics, and field sites need private AI but can't send data to the
> cloud (privacy/offline) and can't afford a dedicated GPU — yet they already own
> a pile of laptops that sit idle."

Lead beneficiary (pick one, make it vivid): e.g. a rural school with ~20 idle
laptops → a private, offline AI tutor.

The hook that turns our TECH into IMPACT:
> "Those laptops are unreliable — students take them, Wi-Fi drops. Every existing
> tool crashes when that happens (we proved it in the kill-test). HyperPot is the
> only one that survives — which is what makes pooled laptops actually usable in
> the real world, not just a lab toy."

So fault tolerance (a feature) becomes the reason the idea is viable (impact).

## The single highest-leverage action
The **Meta Model API fallback** is the most valuable addition: it simultaneously
improves Meta-use (25%), reinforces Impact (30% — it means reliability), and
enriches the Demo (25%). One feature, three criteria.

## Proposed sequencing (when resumed)
1. Lock the framing (problem statement + beneficiary) — no code.
2. Commit to: local Llama + Meta API fallback.
3. Phase 2: pipeline split (as planned).
4. Phase 3: failover + Meta API fallback (fuse the Meta angle in here).
5. Demo script: kill a laptop → fall back to Meta Llama API → pool heals → back
   to local. One demo that touches all four rubric criteria.

## Open questions to answer before acting
1. Do we have Meta Model API credits ($150)? If not, use a smaller local Llama
   as the fallback.
2. Which lead beneficiary is most genuine to the builder (school tutor, clinic,
   field site)? Personal connection strengthens the pitch.

---
*Parked 26 Oct 2026. Revisit after Phase 2/3 core build is working.*

## ADDENDUM — Anti-Projects list (verified 26 Oct 2026)
Meta published a "skip these overdone projects" list. Items to avoid:
AI-generated medical advice (incl. mental-health advisors, skin detectors),
simple image analyzers, dashboards-as-main-feature, AI job screeners,
personality analyzers, basic RAG apps, Streamlit apps, **"AI for Education"
chatbots**, AI nutrition coaches, sports analyzers/coaches.

**Good news:** HyperPot is NOT on this list. Every banned item is a thin app
wrapper around a model; HyperPot is INFRASTRUCTURE (distributed fault-tolerant
inference). This list actually rewards building infra over another chatbot.

**CRITICAL framing caution:** our parked impact examples (school tutor, clinic
notes) brush against two banned items — "AI for Education" chatbots and
AI medical advice. Therefore:
- DO NOT pitch HyperPot as "an AI tutor" or "a medical tool".
- DO pitch HyperPot as "fault-tolerant distributed inference INFRASTRUCTURE",
  with the school/clinic only as an *example setting of who benefits*.
- The demo shows the infra running a Llama model and surviving a killed node —
  not an education or medical application.
Rule of thumb: the school is the *stage for the demo*, never the *product*.

