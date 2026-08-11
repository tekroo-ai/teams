# Repository bootstrap authority

**Date:** 2026-08-11

**Status:** AUTHORIZED

## Observed principal authorization

After approving the controlled transition of the prior repository to
`tekroo-ai/teams-v3`, the principal was told that the next action was to create a
fresh `tekroo-ai/teams` repository and local directory for Tekroo v4. The
principal replied: `Proceed.`

## Authorized scope

- create the fresh private `tekroo-ai/teams` repository;
- preserve the exact accepted `tekroo.kernel.contracts/0.1.0` package and its
  audit lineage;
- establish the contract-first v4 implementation workspace; and
- proceed with implementation in the approved bootstrap order, beginning with a
  pure deterministic kernel and deterministic in-memory test surfaces.

## Authority not inferred

This authorization does not qualify an implementation and does not authorize:

- mutation or migration of Tekroo v3 or historical MongoDB data;
- production deployment;
- OpenHands, SMA, Git-provider, model-provider, or paid-provider adoption;
- execution of the separately gated SMA or OpenHands investigations; or
- editing the accepted `0.1.0` contract release in place.

Those activities require their named conformance or principal gates.
