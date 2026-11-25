# Security policy

BoxAgent runs code written by a language model, so isolation problems matter.

## Reporting a vulnerability

Please **don't open a public issue**. Use GitHub's private reporting: on the repository's **Security** tab, choose **Report a vulnerability**. Include the version or commit, your platform and Docker setup, and steps to reproduce.

This is a small project without guaranteed response times, but reports will be read and taken seriously. Please allow time for a fix before publishing details.

## Supported versions

Only the latest commit on `main` is supported.

## In scope

- Escaping the sandbox through BoxAgent's container configuration
- Reading or writing host files outside the data directory, for example through path validation
- Leaking the API key into logs, errors, saved files or the sandbox
- Network access from the sandbox without `--network`
- Output from a run that can control the user's terminal
- Containers left running after a normal exit or an interrupt

## Known limits

These are documented trade-offs rather than vulnerabilities:

- Containers share a kernel with the Docker host. Escapes through the kernel or Docker itself should go to those projects.
- Disk use is checked about once a second rather than enforced by a quota, so a fast command can overshoot the 4 GiB limit briefly. Volumes declared by a custom image are not counted.
- With `--network`, the sandbox may reach services on your machine or local network.
- The task, the files the agent reads and its command output are sent to the Gemini API.
- Saved code is untrusted. Review it before running it outside the sandbox.
- `vfs export` writes whatever names the agent chose, including hidden files such as `.git/config`, though never outside the target directory or over existing files.
