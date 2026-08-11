# Changelog

## 1.9.0 — embedded cli-guide-spec discovery

- Added offline `memgraph guide` JSON with the memory/graph model, canonical loop,
  concepts, command groups, examples, and operational gotchas.
- Added `memgraph guide --human` Markdown rendering and `memgraph help-json` catalog.
- Added `GET /guide` and `GET /llms.txt` to the HTTP server using the same embedded guide.
- Added cold-start and HTTP route tests for guide/catalog conformance.
