# Design: shared CSI helpers

## Purpose

Small shared functions used by controller and node servers.

## `requireVolumeID(req)`

Extracts `volume_id` from the request; errors with `InvalidArgument` when
missing.

## `validateCapabilities(caps)`

Enforces v1 scope:

- at least one capability required
- `mount` (filesystem) or `block` access type required
- access mode must be `SINGLE_NODE_WRITER`

## Design notes

Single choke point for access-mode policy — keeps the "block or filesystem
+ `SINGLE_NODE_WRITER` only" contract in one place.
