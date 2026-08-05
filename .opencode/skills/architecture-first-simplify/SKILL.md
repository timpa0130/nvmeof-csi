---
name: architecture-first-simplify
description: >
  Ensures code is simple, humanly readable, and architecturally sound.
  Use when writing, reviewing, or refactoring code; when a change feels
  like a workaround or quick fix; when code is hard to read or maintain;
  when proposing a solution that patches a symptom instead of the design.
  The goal is never "make it work" — it is "make it correct and simple."
---

# Architecture-First Simplify

This skill exists to stop the agent from taking shortcuts. Every solution
must be *simple for a human to read* and *correct at the architectural
level*. Quick fixes that work around a design problem are failures, even
when they pass tests.

## Core Principles (never violated)

1. **Minimum code that solves the problem.** No features beyond what was
   asked. No abstractions for single-use code. If 200 lines could be 50,
   rewrite it.
2. **Architecture over patch.** If a change requires a workaround,
   special case, flag, or duplicate logic — STOP and redesign instead.
3. **Chesterton's Fence.** Never remove or bypass something you don't
   understand. Understand it first, then decide.
4. **Readability is the metric.** A change is only done when a new team
   member would understand it faster than before.
5. **One change at a time.** Simplification is incremental, tested, and
   never mixed with feature work.

## When to Use

- Writing new code or proposing a solution
- Reviewing code that accumulated complexity
- Fixing a bug that seems to need a workaround
- Refactoring code written under time pressure
- Consolidating logic scattered across files

**When NOT to use:** Never refactor unrelated code as a drive-by. Stay in
scope unless explicitly asked to broaden it.

---

## Phase 0 — Design Before Code (do this FIRST, always)

Before writing or proposing any solution:

1. **State the real problem** in one sentence. If the "problem" is a
   symptom (e.g. "this function keeps getting nulls"), find the root cause
   ("the caller can construct invalid state").
2. **Name the abstraction.** What concept does this code represent? If you
   can't name it, you don't understand it yet.
3. **Check the seams.** Does this change belong where it's being made, or
   does it cross a boundary (module, API, data model)? If it crosses a
   boundary, stop and design the boundary change first.
4. **Ask clarifying questions** if the request is underspecified. Do NOT
   silently pick an interpretation and run with it.

Present the plan before editing: the problem, the chosen design, and any
alternatives you rejected and why.

---

## Phase 1 — Chesterton's Fence Review

Before modifying any existing code, ask:

- Why does this code exist? What would break if it were gone?
- What assumptions does it encode (data shapes, edge cases, callers)?
- Is the complexity *accidental* (fixable) or *essential* (inherent to the
  problem)?

If you can't answer these, investigate first. Never delete, comment out, or
bypass code you don't understand.

---

## Phase 2 — Quick-Fix Detector (flag these, then redesign)

Treat each of these as a **red flag** that demands a design conversation,
not acceptance:

- [ ] Special-case `if` statements sprinkled in a hot path for "this one
      caller"
- [ ] Boolean/flag parameters that change behavior (`doX(..., true)`)
- [ ] `null` / `undefined` / optional chaining used to paper over missing
      data instead of making invalid states unrepresentable
- [ ] Duplicated logic copy-pasted instead of extracted to one place
- [ ] Comments explaining *what* instead of *why* (the code should say what)
- [ ] Functions over ~30 lines, nesting deeper than ~3 levels, or names
      that don't say what the function does
- [ ] Error handling that swallows failures (`catch {}`, bare `except:`)
- [ ] A "temporary" solution without a plan to remove it (TODO hacks)

**When flagged:** do NOT proceed with the quick fix. Redesign so the
problem disappears rather than being worked around. If the redesign is
larger than the task, say so explicitly and propose the smaller first step
that is still architecturally correct.

---

## Phase 3 — Simplification Pass

Apply changes one at a time:

```
FOR EACH simplification:
  1. Make the change
  2. Run the test suite (or at minimum: typecheck + lint + relevant tests)
  3. If tests pass → continue to the next simplification
  4. If tests fail → revert and reconsider
```

Simplify aggressively, in this order:

1. **Delete** — unused code, dead branches, commented-out code, redundant
   validation
2. **Rename** — names that lie or obscure (a name is documentation)
3. **Extract** — duplicated logic to one place; only extract a function or
   class if it makes the code *easier* to read
4. **Flatten** — reduce nesting (early returns over nested ifs)
5. **Remove** — unnecessary abstractions, config flags, and "flexibility"
   nothing uses. YAGNI.

Each change must pass the test: *"Would a new team member understand this
faster than the original?"* If not, it's not simplification.

---

## Phase 4 — Final Verification Checklist

Before presenting the result:

- [ ] The solution is the *minimum* code that solves the problem
- [ ] No red flags from Phase 2 remain
- [ ] No behavior changed outside the intended scope
- [ ] No new workarounds introduced to make tests pass
- [ ] A new developer could read it top-to-bottom without confusion
- [ ] Tests pass; lint/typecheck clean

## Output Format

Report concisely:

```
## Design
<one-sentence problem, the abstraction, where it lives>

## Changes
<bullet list, one per simplification>

## Red flags resolved
<what was flagged and how the redesign eliminated it>

## Verification
<tests/lint run and result>
```

## Hard Rules

- NEVER make tests pass by weakening the code (skipping tests, mocking
  away the problem, special-casing the assertion).
- NEVER ship a workaround because "the real fix is too big." Surface the
  real fix; agree on the smallest correct first step.
- NEVER refactor unrelated code without being asked.
- ALWAYS prefer the design where the bad case *cannot happen* over the
  design that checks for it.
