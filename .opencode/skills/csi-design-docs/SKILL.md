---
name: csi-design-docs
description: >
  Creates and maintains design documents for the NVMe-oF/TCP CSI driver
  under design/. Use when writing, editing, refactoring, or reviewing code
  in cmd/, pkg/, or test/ so the implementation stays aligned with the
  design docs. Also use when adding a new component, RPC, or backend.
---

# CSI Design Docs

This skill keeps the design documents in `design/` in sync with the code in
`cmd/`, `pkg/`, and `test/`, so the implementation never drifts from the
intended design.

## Design doc map

| Code area | Design doc |
| --------- | ---------- |
| `cmd/nvmeof-csi/main.go` | `design/csi-main-design.md` |
| `pkg/csi/server.go` | `design/csi-server-design.md` |
| `pkg/csi/controller.go` | `design/csi-controller-design.md` |
| `pkg/csi/node.go` | `design/csi-node-design.md` |
| `pkg/csi/identity.go` | `design/csi-identity-design.md` |
| `pkg/csi/helpers.go` | `design/csi-helpers-design.md` |
| `pkg/nvmet/*` | `design/csi-nvmet-design.md` |
| `pkg/zfs/*` | `design/csi-zfs-design.md` |
| `pkg/node/*` | `design/csi-nvme-design.md` |
| `pkg/ids/*` | `design/csi-ids-design.md` |
| `test/manual/e2e.sh` | `design/csi-e2e-design.md` |
| container image (`Dockerfile`, build workflow) | `design/csi-container-design.md` |
| systemd controller service (`deploy/systemd/*`) | `design/csi-systemd-design.md` |
| incident analysis: >4 MiB write rejection | `design/incident-nvmet-tcp-4mib-write-rejection.md` |
| cross-cutting / whole driver | `design/csi-driver-design.md` |

## Workflow

1. **Locate** the design doc(s) for the code you are about to touch.
2. **Read** them before editing.
3. **Edit** the code.
4. **Update** the design doc in the same change:
   - behavior, flow, or sequence changed → update the mermaid diagram / steps
   - flags, RPC surface, or options changed → update the doc
   - a design decision changed → update the doc (and surface it to the user)
5. **Create** a design doc if none exists for a newly added component/RPC, using
   the existing format (Purpose → details → mermaid → notes).

## Design doc format

- Title: `Design: <thing>`
- `## Purpose`
- Body sections with detail
- One or more mermaid diagrams for flows / lifecycles / sequences
- `## Design notes` / observations where relevant

## Rules

- Never implement code that contradicts an existing design doc without updating
  the design doc in the same change. Design leads; code follows.
- Mermaid diagrams are required for any flow, lifecycle, or sequence described
  in a design doc.
- Design docs describe *why / what the design is*; code describes *how*.
  Keep the split clean — don't turn design docs into code dumps.
- Stay surgical: update only the design docs for components you actually
  touched. Do not rewrite unrelated docs as a drive-by.

## Pre-change checklist

- [ ] Design doc for the target component identified and read
- [ ] Design decision confirmed (or deviation consciously approved)

## Post-change checklist

- [ ] Design doc updated if behavior/flags/RPCs/design changed
- [ ] Mermaid diagrams still accurate
- [ ] No stale statements remain in docs
- [ ] Present the design-doc delta alongside the code change
