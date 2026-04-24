#!/usr/bin/env bash
set -euo pipefail

repo="${1:-ITZABEEE123/convertchain}"
branch="${2:-main}"

if ! command -v gh >/dev/null 2>&1; then
  echo "GitHub CLI (gh) is required." >&2
  exit 1
fi

# Enforce PR review + CI checks + no direct pushes to main.
gh api \
  --method PUT \
  -H "Accept: application/vnd.github+json" \
  "/repos/${repo}/branches/${branch}/protection" \
  -f required_status_checks.strict=true \
  -f required_status_checks.contexts[]='Go Tests' \
  -f required_status_checks.contexts[]='Python Tests' \
  -F enforce_admins=true \
  -f required_pull_request_reviews.required_approving_review_count=1 \
  -F required_pull_request_reviews.dismiss_stale_reviews=true \
  -F required_pull_request_reviews.require_code_owner_reviews=false \
  -F restrictions= \
  -F allow_force_pushes=false \
  -F allow_deletions=false \
  -F block_creations=false \
  -F required_linear_history=true

echo "Branch protection enabled for ${repo}:${branch}"
