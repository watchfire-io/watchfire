# Contributing to Watchfire

Thank you for your interest in contributing to Watchfire! This guide will help
you get started.

## Getting Started

1. **Fork the repository** and clone your fork locally
2. **Create a branch** for your changes: `git checkout -b my-feature`
3. **Make your changes** and commit with clear messages
4. **Push** to your fork and open a Pull Request

## Development Setup

### Prerequisites

- Go 1.23+
- Make
- Protocol Buffers compiler (`protoc`)

### Building

```bash
make build
```

This produces `build/watchfired` (daemon) and `build/watchfire` (CLI/TUI).

### Running Tests

```bash
make test
```

### Linting

```bash
make lint
```

## Code Guidelines

- Follow standard Go conventions and idioms
- Run `gofmt` before committing
- Keep functions focused and small
- Write meaningful commit messages
- Add tests for new functionality

## Project Structure

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full project design and
component responsibilities.

## Pull Request Process

1. Ensure your code builds and passes all tests
2. Update documentation if your change affects public APIs or behavior
3. Fill out the pull request template
4. Request review from a maintainer
5. Address any feedback

## Reporting Issues

- Use GitHub Issues to report bugs or request features
- Search existing issues before creating a new one
- Include steps to reproduce for bug reports
- Be as specific as possible

## License

By contributing to Watchfire, you agree that your contributions will be licensed
under the [Apache License 2.0](LICENSE).
