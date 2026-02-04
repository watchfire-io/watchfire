# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] Ember - 2026-02-04

### Added

#### Development Environment
- Go module initialization (`github.com/watchfire-io/watchfire`)
- Makefile with commands: `dev-daemon`, `dev-tui`, `build`, `test`, `lint`, `proto`, `clean`
- golangci-lint configuration (`.golangci.yml`)
- Air hot reload configuration for daemon (`.air.toml`)
- EditorConfig for consistent formatting (`.editorconfig`)
- Version tracking (`version.json`) with version 0.1.0 codename "Ember"

#### Daemon (`watchfired`)
- gRPC server with dynamic port allocation
- Daemon discovery via `~/.watchfire/daemon.yaml`
- Project manager with CRUD operations
- Task manager with CRUD and soft delete/restore
- File watcher with debouncing for real-time updates
- Graceful shutdown on SIGINT/SIGTERM

#### CLI (`watchfire`)
- `watchfire version` - Display version information
- `watchfire init` - Initialize new project (git init, .watchfire/ structure, .gitignore)
- `watchfire task list` - List tasks grouped by status (Draft, Ready, Done)
- `watchfire task list-deleted` - List soft-deleted tasks
- `watchfire task add` - Create new task (interactive prompts)
- `watchfire task <number>` - Edit existing task
- `watchfire task delete <number>` - Soft delete task
- `watchfire task restore <number>` - Restore soft-deleted task

#### Data Models
- Project configuration (`project.yaml`) with settings for auto-merge, auto-delete, auto-start
- Task files with status workflow (draft → ready → done)
- Global projects index (`~/.watchfire/projects.yaml`)
- Global settings (`~/.watchfire/settings.yaml`)

#### Proto Definitions
- `ProjectService` - Project CRUD operations
- `TaskService` - Task CRUD and bulk operations
- `DaemonService` - Daemon status and shutdown
