# Watchfire - Development Guide

## What is Watchfire?

Watchfire orchestrates coding agents sessions based on specs (project definitions and tasks). Read `ARCHITECTURE.md` for the full design.

## Source of Truth

`ARCHITECTURE.md` is the single source of truth for:
- Component responsibilities
- Data structures
- Directory layout
- Build phases
- Tech stack

If this file and `ARCHITECTURE.md` conflict, `ARCHITECTURE.md` wins.

For all decisions, use architecture document as reference. If you need to do something different, please update atchitecture after checking with user. 

## Repository Structure

```
watchfire/
├── ARCHITECTURE.md         # Single source of truth
├── CLAUDE.md               # This file - development guide
├── assets/                 # Images, logos, brand references (shared across components)
├── proto/                  # gRPC protobuf definitions
│   └── watchfire.proto
├── daemon/                 # watchfired - Go daemon
│   ├── cmd/
│   │   └── watchfired/
│   │       └── main.go
│   ├── internal/
│   └── go.mod
├── cli/                    # watchfire - Go CLI/TUI
│   ├── cmd/
│   │   └── watchfire/
│   │       └── main.go
│   ├── internal/
│   └── go.mod
└── gui/                    # Electron GUI (future)
```
