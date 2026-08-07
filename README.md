# mime — go-freedesktop

[![ci](https://github.com/go-freedesktop/mime/actions/workflows/ci.yml/badge.svg)](https://github.com/go-freedesktop/mime/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-freedesktop/mime.svg)](https://pkg.go.dev/github.com/go-freedesktop/mime)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26.4%2B-00ADD8)](https://go.dev/dl/)
[![Coverage](https://img.shields.io/badge/coverage-100%25-1a7f37)](#tests--coverage)

The freedesktop **[Shared MIME-info Database](https://specifications.freedesktop.org/shared-mime-info-spec/latest/)**
layer for a launcher — the piece a file manager or Spotlight-style finder needs
to answer the one question **"what type is this file?"** From a file's name, its
content, or both, it returns a canonical MIME type string. Pure Go,
**CGO-free**, with base-directory resolution reused from `adrg/xdg`.

## Scope — what this does, and what it reuses

It reads the on-disk Shared MIME-info database that lives under
`$XDG_DATA_HOME/mime` and each `$XDG_DATA_DIRS/mime` — the same database
`update-mime-database` produces and every compliant desktop tool consumes — and
resolves types against it. Base-directory lookup reuses
**[`github.com/adrg/xdg`](https://github.com/adrg/xdg)** (MIT), as the rest of
the go-freedesktop family does.

On top of that it implements the spec's matching machinery:

- **Glob matching** — filename → type with the spec's precedence: literal
  file-name matches beat `*.ext` suffix matches beat full `fnmatch` globs; the
  **longest** matching suffix wins; per-glob **weight** breaks ties; the
  `case-sensitive` flag is honoured.
- **Magic matching** — content sniffing over the magic rule tree: absolute
  offset ranges, value masks, word-size byte-swapping, nested (AND/OR) match
  rules, and **priority** selecting the best type.
- **Combined lookup** — `TypeByNameAndContent` follows the spec's recommended
  glob-then-magic order and its tiebreak (a more specific sniffed subtype wins;
  otherwise the file name is authoritative unless magic priority ≥ 80).
- **Aliases & subclasses** — `Unalias` resolves alternative type strings;
  `IsSubclassOf` answers `is-a` queries transitively, including the implicit
  rules (every non-`inode/*` type is-a `application/octet-stream`, every
  `text/*` type is-a `text/plain`).
- **Fallbacks** — an empty file is `application/x-zerosize`; otherwise unknown
  content is `text/plain` when it sniffs as text and `application/octet-stream`
  when it does not.

Two ingest paths build the database: the generated files
(`globs2`/`globs`, the binary `magic`, `aliases`, `subclasses`) and, as a
fallback when those are absent, the source `packages/*.xml`.

## Install

```sh
go get github.com/go-freedesktop/mime
```

## Quickstart

```go
package main

import (
	"fmt"
	"os"

	"github.com/go-freedesktop/mime"
)

func main() {
	// Name only — the fast path a listing view uses.
	fmt.Println(mime.TypeByName("report.pdf")) // application/pdf

	// Name + a content sniff — what "Get Info" / "Open With" should use.
	f, _ := os.Open("/tmp/download")
	head := make([]byte, 256)
	n, _ := f.Read(head)
	fmt.Println(mime.TypeByNameAndContent("download", head[:n]))

	// Relationships.
	fmt.Println(mime.Unalias("application/x-gzip"))               // application/gzip
	fmt.Println(mime.IsSubclassOf("image/svg+xml", "text/plain")) // true
}
```

The package-level helpers use a process-wide database loaded once from the
system directories. For tests, sandboxes, or a bundled database, build your own
with `Load`, `LoadDir`, or `New` + `AddXML` and call the methods on it.

## Public API

| Symbol | Purpose |
| --- | --- |
| `Load() (*Database, error)` | read & merge every system `…/mime` database (via `adrg/xdg`) |
| `LoadDir(dir) (*Database, error)` | read one `mime` directory (generated files, else `packages/*.xml`) |
| `New() *Database` | empty database to populate manually |
| `(*Database).AddXML(r) error` | merge one Shared MIME-info XML document |
| `(*Database).AddPackagesDir(dir) error` | merge every `*.xml` in a `packages/` directory |
| `(*Database).TypeByName(name) string` | best type from the file name, or `""` |
| `(*Database).TypesByName(name) []string` | all top-tier glob matches, weight-ordered |
| `(*Database).TypeByContent(data) string` | best type from a content sniff (with fallbacks) |
| `(*Database).TypeByNameAndContent(name, data) string` | combined lookup with the spec tiebreak |
| `(*Database).Unalias(t) string` / `Aliases(t) []string` | alias resolution both ways |
| `(*Database).IsSubclassOf(t, parent) bool` / `Parents(t) []string` | `is-a` queries |
| `Default() *Database` | the process-wide system database |
| `TypeByName` / `TypeByContent` / `TypeByNameAndContent` / `Unalias` / `IsSubclassOf` | package-level helpers over `Default()` |
| `OctetStream`, `PlainText`, `ZeroSize` | fallback type constants |
| `ErrBadMagic` | sentinel for a malformed binary `magic` file |

### The combined tiebreak

`TypeByNameAndContent(name, data)`:

1. No data (`nil`) → the glob result, else `application/octet-stream`.
2. Empty data (`[]byte{}`) → `application/x-zerosize`.
3. No glob match → the content result (magic, else text/binary fallback).
4. No magic match → the glob result.
5. Both match → the glob type if they agree; the **more specific** of the two
   when one is-a the other; otherwise the file name wins unless magic priority
   is ≥ 80.

## wasmdesk / Spotlight integration

This library is the "what type is this?" resolver in the wasmdesk file-manager
and Spotlight-style flow. Paired with its go-freedesktop siblings it drives the
whole **Open With** experience:

- **mime** (this repo) resolves a selected file → a canonical type.
- **desktopentry** + a `mimeapps.list` handler map that type → the application
  entries that declare it in their `MimeType=` list, yielding the ranked
  **Open With** menu and the default handler.
- **icontheme** turns the type (and the chosen app) into an icon.
- **[go-thumbnail](https://github.com/go-thumbnail/thumbnail)** uses the sniffed
  type to decide how to render a preview.

`TypeByNameAndContent` is the call the finder makes on selection; the returned
canonical string is the key every other stage keys off.

## Tests & coverage

`CGO_ENABLED=0 go test ./...` — **100% statement coverage**, including every
error branch, driven by fixtures under `testdata/` (a generated-file database
and a `packages/*.xml`-only database, plus a hand-built binary `magic`). CI
additionally runs the suite on the six supported 64-bit targets
(amd64/arm64 natively, riscv64/loong64/ppc64le/s390x under qemu-user). The
`-race` coverage gate is the only step that enables cgo; the arch matrix proves
the library itself is CGO-free.

## License

BSD-3-Clause. Copyright (c) the go-freedesktop/mime authors.

---

> **Note:** the `go-freedesktop` org landing page and MkDocs site are deferred
> to the Wave-2 documentation sweep; this repo ships the README and `.github`
> workflow for now.
