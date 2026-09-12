# Standalone Go Project Design

## Goal

Make `jsp-go` a self-contained Go project that can be copied or cloned without
the Python project and can then be built, tested, configured, run, and packaged
on supported platforms.

The standalone project must not read configuration, data files, image assets,
source files, or executables from its parent directory. Python, Pillow,
Slimeface, OpenCV, and CGO are not runtime requirements.

## Project Contents

The project root owns every file required at build time and runtime:

```text
jsp-go/
├── .github/workflows/ci.yml
├── cmd/javsp/
├── data/
│   ├── actress_alias.json
│   ├── facefinder
│   └── genre_*.csv
├── docs/
│   ├── BUILD.md
│   └── CONFIGURATION.md
├── image/
│   ├── JavSP.svg
│   ├── sub_mark.png
│   └── unc_mark.png
├── internal/
├── .gitignore
├── config.yml
├── go.mod
├── go.sum
├── LICENSE
├── Makefile
├── README.md
└── THIRD_PARTY_NOTICES.md
```

`run_test/` remains local integration-test input and is excluded from version
control and release archives.

## Runtime Paths

An explicitly supplied `-config` path is authoritative. With no `-config`
argument, the program searches for `config.yml` in this order:

1. the current working directory;
2. the executable directory;
3. the executable directory's parent, to support `bin/javsp` development builds.

The selected configuration file's directory is the resource root. `data/` and
`image/` are resolved beneath that root. This makes behavior independent of the
caller's working directory while keeping configuration and resource files
editable.

Missing required configuration or image assets produce errors containing the
resolved absolute path. Optional genre maps and actress aliases preserve their
existing fallback behavior when absent.

## Configuration

The project contains a complete, commented `config.yml` matching the Go
configuration schema. Configuration precedence remains:

1. repeated `-o key=value` command-line overrides;
2. `JAVSP_...` environment variables;
3. YAML values.

The documentation lists supported keys, override syntax, duration formats,
crawler identifiers, translation engines, and examples for common workflows.

The crop engine is configured as `engine.name: pigo`. For compatibility,
`engine.name: slimeface` is accepted as a legacy alias for the same native Go
detector. It never starts Python.

## Native Image Processing

The poster pipeline remains entirely in Go:

1. download and validate a cover candidate;
2. decode the image and honor EXIF orientation where applicable;
3. produce a poster using the configured 1.42 height-to-width ratio;
4. use native face detection for configured labels, FC2, and uncensored titles;
5. fall back to deterministic right-side cropping if no face is detected;
6. overlay subtitle and uncensored marks when configured;
7. encode output using an extension that matches the actual output format.

Pigo supplies pure Go face detection. Its `facefinder` cascade is stored under
`data/` and distributed with the project. Dependency and model attribution are
recorded in `THIRD_PARTY_NOTICES.md`.

Downloaded extra fanart is decoded before being accepted. Invalid or incomplete
images are removed and reported without aborting downloads of later images.

## Build And Release

The Makefile is the canonical developer interface:

- `make build` builds the host executable into `bin/`;
- `make run` runs it with the project configuration;
- `make fmt` formats Go source;
- `make vet` runs static analysis;
- `make test` runs all unit tests;
- `make check` runs formatting verification, vet, tests, and a host build;
- `make clean` removes generated output;
- `make dist` cross-compiles and creates release archives;
- `make checksums` writes SHA-256 checksums for release archives.

Release targets are:

- macOS: amd64 and arm64;
- Linux: amd64 and arm64;
- Windows: amd64 and arm64.

Unix releases use `.tar.gz`; Windows releases use `.zip`. Every archive has the
same top-level layout and contains the platform binary, `config.yml`, `data/`,
`image/`, `README.md`, `docs/`, `LICENSE`, and `THIRD_PARTY_NOTICES.md`.

Version, commit, and build date are injected through linker flags and displayed
by `javsp -version`. Builds use `CGO_ENABLED=0` so cross-compilation does not
require platform C toolchains.

## Documentation

`README.md` is Go-project-specific and contains prerequisites, a quick start,
core features, command-line usage, supported platforms, and links to detailed
documents. It does not instruct users to install the Python project.

`docs/BUILD.md` documents development commands, cross-compilation, release
archive contents, version injection, and CI. `docs/CONFIGURATION.md` documents
all configuration sections, precedence, environment and CLI overrides, native
face cropping, resource paths, and troubleshooting.

## CI

The standalone workflow runs against the `jsp-go` module and performs:

1. formatting verification;
2. `go vet ./...`;
3. `go test ./...`;
4. host build;
5. compile checks for each supported OS and architecture.

The workflow does not access files above the project root.

## Testing And Acceptance

Tests cover:

- default and explicit configuration discovery;
- environment and command-line override precedence;
- resource lookup from a different working directory;
- absence of parent-project dependencies;
- Pigo face selection and default-crop fallback;
- image validation, orientation, labels, and output formats;
- release archive file lists;
- host tests and all supported cross-compilation targets.

Acceptance is met when a copy of `jsp-go` placed outside the Python repository
passes `make check`, produces all release archives with `make dist`, and runs
with its local `config.yml`, `data/`, and `image/` resources only.
