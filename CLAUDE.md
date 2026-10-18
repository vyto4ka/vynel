# Notes for Claude Code

- Work on `main`. Russian for conversation, English for code, comments and commit messages.
- Every commit credits the owner as co-author. End each commit message with these trailers
  (after any other trailers the session requires):

  ```
  Co-authored-by: vyto4ka <64610654+vyto4ka@users.noreply.github.com>
  ```

- Before pushing: `go test ./...`, `golangci-lint run ./...`, `shellcheck scripts/*.sh`, and for the web UI
  `cd web && npx tsc --noEmit -p . && npm run build` (keep `web/dist/.gitkeep`).
- User-facing docs recommend only the KeqDroid client; the code keeps supporting every client.
