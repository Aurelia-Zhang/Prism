# Prism Collaboration Contract

This repository is an interview-oriented personal project. Optimize for a real,
explainable core path rather than production-scale infrastructure.

## Communication

- Use concise, professional Chinese for decisions, reviews, and user-facing status.
- Use English for code identifiers, public APIs, commit subjects, and durable technical terms.
- Lead with repository facts. Separate observations from proposals and assumptions.
- Keep each implementation session inside one Roadmap item unless the user changes the scope.

## Evidence states

Every capability claim uses exactly one state:

- `planned`: documented intent without sufficient implementation evidence.
- `partial`: some implementation or tests exist, but an important path, integration, or
  required evidence is missing.
- `verified`: the current commit has the implementation, relevant tests, and any required
  real integration, Trace, or metric evidence. A fake Provider can verify runtime behavior,
  but cannot verify OpenAI, Anthropic, or MCP compatibility.

Lost implementations, reference repositories, design notes, mocks, and test doubles are not
evidence that Prism currently implements a capability. Never fabricate collaboration history,
reviews, live compatibility, benchmark numbers, commits, or PRs.

## Roadmap and claim maintenance

- Use the B / T / C / O / E item IDs in `ROADMAP.md`.
- Keep `docs/resume-evidence.md` aligned with code, tests, Trace or metric artifacts,
  Commit/PR links, and known limitations.
- README describes only current repository behavior. Future work belongs in `ROADMAP.md`.
- A claim may move to `verified` only after its stated evidence is reproducible.

## Change workflow

- Read this file, the assigned Roadmap item, `docs/resume-evidence.md`, and the repository-level
  `prism-mr` skill before implementing a feature MR.
- Prefer one reviewable vertical MR over unrelated cleanup or speculative abstractions.
- Reuse current contracts. If a contract must change, explain why and update callers and tests
  in the same MR.
- Run the narrow tests while iterating and the repository-wide validation before handoff.
- Report exact commands and results. Report unavailable credentials or live systems as limits.

## Permissions

- Inspection, scoped file edits, and local tests are allowed when assigned.
- The main controller may create branches, commit, push, open or update PRs, and merge an assigned
  Roadmap MR after its required review and validation. Do not interrupt the owner for each routine
  Git step.
- Implementation Sessions follow the permissions in their Prompt. When a Prompt withholds Git
  publication rights, hand the verified working tree back to the main controller for delivery.
- Force-push, history rewrite, destructive deletion, tags, releases, and publication outside the
  assigned MR require explicit owner authorization.
- Never rewrite user-owned history or discard unrelated working-tree changes.
