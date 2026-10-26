---
name: csi-design-docs
description: >
  Maintains the few high-level design documents for the NVMe-oF/TCP CSI
  driver under design/ (architecture, volume lifecycle, deployment). Use when
  writing, editing, refactoring, or reviewing code in cmd/, pkg/, test/,
  charts/, deploy/ or the Dockerfile so the implementation stays aligned with
  the design docs. Also use when adding a new component, RPC, or backend.
---

# CSI Design Docs

This skill keeps a small set of overview docs in `design/` in sync with the
code. The docs explain how the system fits together and how the code flows
when a volume is created, attached and removed — not one file per package.

## The docs

| Doc | Owns |
| --- | ---- |
| `design/architecture.md` | components and topology, process startup, services per mode, request validation, naming from the volume ID |
| `design/volume-lifecycle.md` | create → stage → publish → unpublish → unstage → delete, step by step; idempotency and failure handling; reconcile; other controller RPCs; manual e2e |
| `design/deployment.md` | build, container image, CI, Helm chart, systemd controller service |
| `design/incident-*.md` | one per incident analysis; never merged into the others |

## Code area → section

| Code area | Doc § section |
| --------- | ------------- |
| `cmd/nvmeof-csi/main.go` (flags, startup, shutdown) | architecture § Process startup |
| `pkg/csi/server.go` (driver, `Run`, `EnsurePort`) | architecture § Process startup, § Services per mode |
| `pkg/csi/server.go` (`Reconcile`) | volume-lifecycle § Recovery |
| `pkg/csi/identity.go` | architecture § Services per mode |
| `pkg/csi/helpers.go` | architecture § Services per mode (request validation) |
| `pkg/ids/*` | architecture § Naming |
| `pkg/csi/controller.go`, `pkg/zfs/*`, `pkg/nvmet/*` | volume-lifecycle § Create, § Teardown, § Other controller RPCs |
| `pkg/csi/node.go`, `pkg/node/*` | volume-lifecycle § Stage and publish, § Teardown |
| `test/manual/e2e.sh` | volume-lifecycle § Manual end-to-end check |
| `Dockerfile`, `.github/workflows/*` | deployment § Container image |
| `charts/nvmeof-csi/*` | deployment § Helm chart |
| `deploy/systemd/*` | deployment § Controller as a systemd service |

## Workflow

1. **Locate** the section that owns the code you are about to touch (table
   above) and read it.
2. **Edit** the code.
3. **Update** that section in the same change:
   - a flow or step order changed → update the diagram and its table
   - flags, RPC surface, settings changed → update the table row
   - a design decision changed → update the doc and surface it to the user
4. **New code goes into an existing doc.** A new package, RPC or backend gets
   a section (or a row) in the doc that owns its flow. Create a new doc only
   for a genuinely new cross-cutting topic or an incident analysis, and ask
   the user first.

## Doc format

- Title `Design: <topic>`, then `## Purpose` (2–4 sentences, what this doc
  covers and which doc covers the rest).
- Lead with how things flow: a minimal mermaid diagram per flow.
  - Box labels 2–4 words, edge labels short verbs; no paths, flags or
    function names in the boxes.
  - Flowcharts for overviews; sequence diagrams for RPC-by-RPC flows.
- Directly below each diagram, a table mapping each box/step to the exact
  names: RPCs, functions, paths, flags, settings, error codes.
- `## Design notes` for the *why*: decisions, trade-offs, known gaps.

## Rules

- Few docs, high level. Do not create a doc per file or package; extend the
  owning section instead.
- Design leads, code follows: never ship code that contradicts a doc without
  updating the doc in the same change.
- Every flow, lifecycle or sequence that a doc describes has a diagram.
- Docs explain what happens and why; the code explains how. No code dumps.
- Stay surgical: update only the sections for code you touched.

## Checklists

Before:

- [ ] Owning section identified and read
- [ ] Design decision confirmed (or deviation approved by the user)

After:

- [ ] Section updated if behavior, flags, RPCs or design changed
- [ ] Diagrams and their tables still match the code
- [ ] No stale statements or references to removed docs
- [ ] Design-doc delta presented alongside the code change
