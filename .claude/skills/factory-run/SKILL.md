---
name: factory-run
description: Runs one software-factory cycle (Planner → human gate on the spec → Builder → Verifier with a correction loop) on a requirement or an approved spec.
disable-model-invocation: true
argument-hint: "<requirement | path to an approved spec>"
---

You drive a full software-factory cycle for the **opnview** repository (Go,
SQLite, single binary, embedded frontend). You orchestrate: you do not write
the spec yourself, you do not implement yourself, you do not verify yourself.
Each role runs in a subagent with its own context.

## Language — absolute rule

**Every artifact this cycle produces is written in English**: the spec, its
status line, the code, the comments, the commit-ready diff, the reports. You
may talk to the user in French — files are English. Pass this rule on to every
subagent you launch, and reject a deliverable that violates it.

## Step 1 — Input

The argument you receive is either a file path or a plain-text requirement.

- If it is the path of an existing file under `specs/` whose status line reads
  `Status: APPROVED`: go straight to **step 3**.
- If it is the path of a file under `specs/` whose status is not `APPROVED`:
  resume at **step 2** from its content, to get it approved.
- Otherwise: treat the argument as a **requirement** and continue to step 2.

## Step 2 — Plan phase (mandatory human gate)

**If no user interaction is possible (headless run, `claude -p`), stop
immediately with an explicit error:** a CI cycle requires the path of an
already-approved spec. Never build without an approved spec, under any
pretext.

1. Launch the `factory-planner` subagent (Agent tool, `subagent_type:
   "factory-planner"`), passing the requirement **in full**, neither
   summarised nor reworded.
2. On return, read the *Open questions* section of the draft and put them to
   the user with `AskUserQuestion`, grouping as many questions as possible per
   call and offering concrete options for each.
3. Fold the answers into the spec. If an answer opens a new ambiguity, run
   another round of questions rather than deciding alone.
4. Write `specs/SPEC-<requirement-slug>.md` (lowercase slug, words separated by
   hyphens, ASCII only), whose **first line after the title** is exactly:

       Status: PROPOSED

5. Present the spec to the user and ask for **explicit approval**.
6. On approval: replace the status line with `Status: APPROVED`. Otherwise:
   collect the corrections, update the spec and ask again. Loop until approval
   is given.

## Step 3 — Build phase

Launch the `factory-builder` subagent (fresh context) with the **path** to the
approved spec and the instruction to read it in full. Do not pass it your own
interpretation of the spec.

## Step 4 — Verify phase, correction loop (3 iterations maximum)

Keep an iteration counter, initialised to 1.

1. Launch a **new** `factory-verifier` subagent (blank context, independent of
   the Builder; never reuse a previous Verifier through SendMessage) with the
   path to the spec. Take the final JSON block from its reply.
2. If `verdict` is `APPROVED` **and** `tests_passed` is `true`: leave the loop,
   the cycle succeeded.
3. Otherwise, if the counter is strictly below 3: relaunch `factory-builder`
   with the path to the spec **and the complete list of `issues`** (file,
   severity, description, none omitted, none summarised), plus the instruction
   to fix every issue without regressing on acceptance criteria already
   satisfied. Increment the counter and go back to sub-step 1 with a new
   Verifier.
4. If the counter reaches 3 without approval: **fail explicitly**. Publish the
   remaining issues as they are, along with the state of the repository. Never
   approve out of exhaustion, never shrink the spec to make the verdict pass.

## Step 5 — Final report

Close with a structured report:

- path of the spec used and its status;
- files modified — output of `git status --porcelain`;
- results of the project commands as reported by the Verifier: `gofmt -l .`,
  `go vet ./...`, `go build ./...`, `go test ./...`, with their exit codes;
- final verdict and the number of iterations consumed;
- remaining issues, if any;
- suggested next action: **the user reviews the diff**. Do not commit on their
  behalf, do not push, do not create a branch.
