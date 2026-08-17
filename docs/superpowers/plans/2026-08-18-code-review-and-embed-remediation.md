# Implementation Plan: Code Review & Embedded Files Remediation

## Context
Code review identified duplicate embedded files, missing documentation sync, and loose type assertions in embedded UI template loading. This plan addresses those review findings.

## Tasks
1. Clean up embedded static assets and templates.
2. Synchronize memory, test harness docs, and swagger docs.
3. Validate strict zero-alloc patterns in JSON serialization.
4. Verify all tests pass with race detector.
