# GitHub repository hardening

This file documents repository settings that are intentionally enforced outside the source tree.

The recommended manual settings are listed in the project README and should be reviewed whenever repository ownership, GitHub plan, or release policy changes.

Repository-side safeguards committed in this repository include:

- minimal GitHub Actions permissions in each workflow
- CodeQL scanning
- Dependabot for Go modules and GitHub Actions
- CODEOWNERS for all files and security-sensitive paths
- pull request security checklist
- no workflow using `pull_request_target`
- no workflow exposing repository secrets to pull requests
- beta container publishing from `main`

Branch rules, repository Actions defaults, secret scanning, Dependabot alert toggles, and merge policy are GitHub settings and cannot be guaranteed by committed files alone.
