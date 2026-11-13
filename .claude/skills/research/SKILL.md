---
name: research
description: Research one CNCF or Linux Foundation project (or spec) for ideas that apply to token/cost budgets for AI agents, score them, and add them to research/IDEAS.md. Use when the user says "research", "/research", or asks for new ideas from CNCF projects.
---

# Research loop

Goal: borrow the best (and boldest) ideas from the cloud-native ecosystem and turn them into features for this project. One project per run, done properly, beats five done shallowly.

## Steps

1. **Pick a target.** If the user named one, use it. Otherwise read `research/RESEARCH_LOG.md` and choose the next unstudied entry from the candidates list in `research/IDEAS.md`, or a new project you have good reason to believe is relevant.
2. **Check it is current.** Search the web for the project's current status (CNCF maturity level, latest release, whether it is still maintained). Projects change fast; never rely on memory for this.
3. **Study the design, not the marketing.** Read the architecture/design docs and proposals (`/docs`, `/design`, `/proposals`, `/enhancements`, KEPs, ADRs). Note the core mechanism in plain words.
4. **Extract ideas.** For each mechanism, ask: how would this help per-agent attribution, request-time enforcement, budgets as Kubernetes resources, or agent ownership? Wild ideas are welcome; mark them as such.
5. **Check for prior art.** For each idea, search whether a gateway or tool already does it. If yes, say where and whether we should integrate instead of building.
6. **Score and write.** Append each idea to `research/IDEAS.md` using the format there. Add one dated entry to `research/RESEARCH_LOG.md` with the sources you actually read.
7. **Propose one spike.** Pick the best idea by impact and learning value and propose a spike (an experiment of a few hours) that proves or kills it: what to build, how to measure success, what we learn. **Do not build it until the user says yes.**

## Output to the user

- Explain the studied project's core mechanism with an everyday analogy.
- List the ideas found with their scores, best first.
- The proposed spike.
- Sources (links you actually read).

## Rules

- Every claim about another project needs a source link. If you could not find something, say so; never fill gaps from memory.
- Do not change application code during research.
- Keep each idea entry short; link to sources instead of copying them.
