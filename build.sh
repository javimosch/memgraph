#!/bin/bash
# Build memgraph CLI - Default
go build -o memgraph .
ls -lh memgraph

# Build memgraph CLI - Optimized (size + performance)
go build -ldflags "-s -w" -o memgraph-release .
ls -lh memgraph-release