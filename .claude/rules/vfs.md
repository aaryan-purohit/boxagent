---
paths:
  - "internal/vfs/**"
  - "internal/tools/vfs.go"
  - "internal/tools/commit.go"
---

# VFS rules

- **Path validation is security-critical** and lives only in `vfs.ParsePath`. It cleans the path, then rejects: absolute paths, any `..` segment, empty paths, NUL bytes, backslashes, and paths longer than 512 bytes.
- Every public `Store` method takes a `vfs.Path` (`List` takes a `vfs.Prefix`, validated the same way except that empty means everything), so unvalidated strings can't reach storage.
- Blobs live at `<data-dir>/blobs/<first-2-hex>/<sha256>`. Write to a temp file in the same directory, then rename. Identical content is stored once.
- Versions start at 1 per path and are assigned inside a transaction.
- Writing content identical to the latest version is a no-op.
- Reject single files over 5 MB, and paths that would be both a file and a directory.
- Add or remove blobs only while holding the database write lock.
- Binary content is stored normally.
- `Store` takes a `clock func() time.Time` in its constructor. Timestamps are RFC3339 UTC.
- The schema is in `schema.sql` (`go:embed`). See `docs/ARCHITECTURE.md` for the table definition.
