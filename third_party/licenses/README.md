# Third-party licence texts

The texts `scripts/third-party-notices.sh` (`go run ./internal/tools/thirdparty`)
puts into `THIRD-PARTY-NOTICES.txt` besides those it reads itself (the Go
modules' licences, from the module cache, which `go.sum` verifies; MyGo's,
from `third_party/mygo`; RapiDoc's, `internal/api/docs/RAPIDOC-LICENSE.txt`).
They are committed so that a release build regenerates the notices without
the network.

- `swift/<package>/`: the engine's Swift packages, one directory per pin of
  `engine/Package.resolved`, plus mlx-audio-swift (the commit in
  `scripts/fetch-engine-deps.sh`) and the git submodules of those (mlx and
  mlx-c, in mlx-swift). `PIN` records the URL and revision; `files/` has the
  package's licence and notice files at their paths in the package (but
  those of tests, docs and examples), and `*.header.txt`, the licence
  comments of source files that carry third-party code (the list is
  `swiftExcerpts` in `internal/tools/thirdparty/tables.go`). Written by
  `scripts/third-party-notices.sh --fetch` (git and the network): run it after
  a Swift pin moves. Do not edit by hand.
- `rapidoc/`: the libraries in RapiDoc's bundle (`internal/api/docs/rapidoc-min.js`),
  which ships no licences of its own for them. `RAPIDOC-VERSION` is the
  RapiDoc they belong to; each directory has a `COMPONENT` file (name,
  version, URL, SPDX licence) and the licence file of the npm package. They
  are RapiDoc 10.1.0's `dependencies` (lit, marked, microlighter,
  github-slugger, @scalar/openapi-parser) at the newest version in its range
  when it was published (2026-10-02), from the npm tarballs; the bundle does
  not record the versions it was built with, nor the dependencies of those.
  When RapiDoc is updated (`internal/api/docs/README.md`), redo them the same
  way and update `RAPIDOC-VERSION`; the generator fails until then.
- `texts/`: canonical texts the generator uses for components that ship none:
  the Apache License 2.0 (SPDX's copy; the voice previews) and SQLite's
  public-domain dedication (MyGo's SQLite plugin), and the MIT licence of
  Hugeicons (`MIT-Hugeicons.txt`; the npm package @iconify-json/hugeicons,
  the icons' Play and Pause, states only "MIT", author Hugeicons).
