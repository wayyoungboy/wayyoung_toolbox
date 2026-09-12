# Standalone JavSP-Go Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `jsp-go` into a parent-independent, pure Go, multi-platform project that implements and verifies every currently implemented JavSP workflow, including native face-aware poster cropping and a real ATID-687 scrape with Google translation through the configured SOCKS5 proxy.

**Architecture:** The selected `config.yml` directory is the runtime resource root for `data/` and `image/`. Network consumers share one configured HTTP transport. Image downloads are decoded, normalized, and re-encoded before acceptance; Pigo provides native face detection with deterministic crop fallback. Project packaging copies editable configuration and assets beside each release binary.

**Tech Stack:** Go 1.22, `github.com/esimov/pigo/core`, `github.com/disintegration/imaging`, `golang.org/x/image/webp`, GNU Make-compatible make, shell integration harness.

## Global Constraints

- The project must build, test, and run after `jsp-go` is copied outside the Python repository.
- Runtime code must not invoke or require Python, Pillow, Slimeface, OpenCV, or CGO.
- Supported releases are darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, windows/amd64, and windows/arm64.
- `run_test_back/` is the immutable integration-test source; `run_test/` is restored from it before each destructive test.
- Google integration tests use `socks5://127.0.0.1:7897` through an override, never as a committed default.
- Existing YAML, `JAVSP_...`, and repeated `-o key=value` configuration precedence remains compatible.
- New behavior follows red-green-refactor and every completion claim requires fresh full verification.

---

### Task 1: Parent-Independent Runtime Paths And Build Metadata

**Files:**
- Create: `internal/projectpath/projectpath.go`
- Create: `internal/projectpath/projectpath_test.go`
- Modify: `cmd/javsp/main.go`
- Modify: `cmd/javsp/main_test.go`

**Interfaces:**
- Produces: `projectpath.ResolveConfig(explicit, cwd, executable string) (string, error)`
- Produces: `projectpath.ResourceRoot(configPath string) (string, error)`
- Produces: linker variables `version`, `commit`, and `buildDate` in `cmd/javsp`

- [ ] **Step 1: Write failing path and version tests**

Add table tests proving explicit config wins, default discovery checks cwd/executable/executable-parent in order, missing config reports searched absolute paths, and resource root equals the selected config directory. Update the version test to expect name, version, commit, and build date fields.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/projectpath ./cmd/javsp`

Expected: package/function/linker fields are missing and tests fail for that reason.

- [ ] **Step 3: Implement path resolution and use it in main**

Implement a side-effect-free resolver that validates regular files with `os.Stat`, canonicalizes successful paths with `filepath.Abs`, and returns a joined error for failed candidates. Change `-config` default from `../config.yml` to empty discovery. Resolve `data/` and `image/` from `ResourceRoot(configPath)` and remove `findDataRoot`/`findImageRoot` parent fallbacks.

- [ ] **Step 4: Implement build metadata output**

Declare:

```go
var (
    version   = "dev"
    commit    = "unknown"
    buildDate = "unknown"
)
```

Print `JavSP-Go <version> (commit <commit>, built <buildDate>)` for `-version`; Makefile linker flags will override these values.

- [ ] **Step 5: Run focused and full tests**

Run: `go test ./internal/projectpath ./cmd/javsp && go test ./...`

Expected: all packages pass without accessing `../config.yml`, `../data`, or `../image`.

- [ ] **Step 6: Commit**

```bash
git add jsp-go/internal/projectpath jsp-go/cmd/javsp
git commit -m "feat(go): resolve standalone project resources"
```

### Task 2: Shared Proxy-Aware Translation Transport

**Files:**
- Modify: `internal/httpclient/httpclient.go`
- Modify: `internal/httpclient/httpclient_test.go`
- Modify: `cmd/javsp/main.go`
- Modify: `cmd/javsp/main_test.go`

**Interfaces:**
- Produces: `func (c *Client) HTTPClient() *http.Client`
- Consumes: `translate.NewEngineFromConfig(raw any, client *http.Client)`

- [ ] **Step 1: Write a failing shared-client test**

Create an `httpclient.Client` with a proxy URL and assert `HTTPClient()` returns the exact configured transport/client used by requests. In `cmd/javsp`, test that translator construction receives the network client's `*http.Client`, rather than nil/default transport.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/httpclient ./cmd/javsp`

Expected: `HTTPClient` and the translator builder seam do not exist.

- [ ] **Step 3: Expose and inject the configured client**

Return the internal `*http.Client` from `HTTPClient()` without exposing mutable headers/cookies. Change main to call:

```go
translator, err := translate.NewEngineFromConfig(
    cfg.Translator.Engine,
    client.HTTPClient(),
)
```

This makes Google, Bing, Baidu, Claude, and OpenAI use the same timeout and SOCKS5/HTTP proxy as crawlers.

- [ ] **Step 4: Verify GREEN and regression suite**

Run: `go test ./internal/httpclient ./internal/translate ./cmd/javsp && go test ./...`

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add jsp-go/internal/httpclient jsp-go/cmd/javsp
git commit -m "fix(go): route translators through configured proxy"
```

### Task 3: Pure Go Face Detection And Robust Image Pipeline

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Rewrite: `internal/poster/poster.go`
- Modify: `internal/poster/poster_test.go`
- Modify: `cmd/javsp/main.go`
- Modify: `cmd/javsp/main_test.go`
- Create: `data/facefinder`
- Create: `THIRD_PARTY_NOTICES.md`

**Interfaces:**
- Produces: `poster.NewPigoDetector(cascadePath string) (*PigoDetector, error)`
- Produces: `poster.Normalize(sourcePath, destinationPath string) error`
- Produces: `poster.Validate(path string) error`
- Consumes: `poster.Options.FaceDetector`

- [ ] **Step 1: Write failing native detector/config tests**

Test that config accepts `pigo` and legacy `slimeface`, rejects any other crop engine, and that poster source contains no `os/exec` behavior. Add detector tests using a small fixture image and injected cascade bytes so the highest-score face controls crop position; add no-face and invalid-cascade fallback/error cases.

- [ ] **Step 2: Write failing format/orientation tests**

Add tests for JPEG, PNG, GIF, and WebP decode; JPEG EXIF orientations 3, 6, and 8; alpha marks; invalid/truncated images; and normalized JPEG/PNG output whose bytes match its extension.

- [ ] **Step 3: Verify RED**

Run: `go test ./internal/config ./internal/poster ./cmd/javsp`

Expected: Pigo constructor, validation, normalization, WebP, and EXIF behavior are absent.

- [ ] **Step 4: Add pure Go dependencies and cascade**

Add pinned module versions, copy Pigo's MIT-licensed `facefinder` cascade into `data/`, and record package/model copyright, source URL, version, and license in `THIRD_PARTY_NOTICES.md`.

- [ ] **Step 5: Replace the Python subprocess detector**

Remove `SlimefaceDetector`, `os/exec`, and embedded Python source. Convert decoded images to grayscale pixels, call Pigo `RunCascade`, cluster detections, select the highest score, and convert its row/column/scale into an `image.Rectangle`. `slimeface` remains only a configuration alias to the Pigo implementation.

- [ ] **Step 6: Normalize all accepted images**

Use pure Go decoders plus auto-orientation. Download cover candidates to a temporary file, validate/decode them, write fanart in its declared format, then create poster. Decode and re-encode each extra fanart as PNG; delete rejected temporary/invalid files and continue with later previews.

- [ ] **Step 7: Verify GREEN and absence of Python runtime calls**

Run: `go test ./internal/config ./internal/poster ./cmd/javsp && rg -n 'os/exec|python3|slimeface import|Pillow' --glob '*.go' .`

Expected: tests pass; `rg` returns no Go runtime invocation matches.

- [ ] **Step 8: Commit**

```bash
git add jsp-go/go.mod jsp-go/go.sum jsp-go/internal/config jsp-go/internal/poster jsp-go/cmd/javsp jsp-go/data/facefinder jsp-go/THIRD_PARTY_NOTICES.md
git commit -m "feat(go): add native face-aware image processing"
```

### Task 4: Self-Contained Configuration, Data, Assets, And Documentation

**Files:**
- Create: `.gitignore`
- Create: `config.yml`
- Create: `data/actress_alias.json`
- Create: `data/genre_avsox.csv`
- Create: `data/genre_javbus.csv`
- Create: `data/genre_javdb.csv`
- Create: `data/genre_javlib.csv`
- Create: `image/JavSP.svg`
- Create: `image/sub_mark.png`
- Create: `image/unc_mark.png`
- Create: `LICENSE`
- Create: `README.md`
- Create: `docs/BUILD.md`
- Create: `docs/CONFIGURATION.md`
- Create: `docs/PARITY.md`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consumes: the complete config schema and runtime root from Tasks 1-3
- Produces: editable standalone runtime files and documented user contract

- [ ] **Step 1: Add a failing standalone fixture test**

Load `../../config.yml` from inside `internal/config`, verify every crawler group, naming field, translator field, crop engine, duration, and optional value parses, and verify `data/`/`image/` required files exist beneath the same root.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/config`

Expected: local standalone config/assets are missing.

- [ ] **Step 3: Copy and adapt owned runtime files**

Mechanically copy the parent project's license, genre maps, actress aliases, logo, and marks into `jsp-go`. Create a fully commented Go-specific config with `input_directory: null`, `proxy_server: null`, `engine: null`, and native Pigo examples. Exclude `run_test/`, `run_test_back/`, `bin/`, and `dist/` in `.gitignore`.

- [ ] **Step 4: Write standalone documentation**

Document quick start, CLI flags, every config section, precedence examples, supported crawlers/translators, proxy example `-o network.proxy_server=socks5://127.0.0.1:7897`, Pigo crop configuration, resource lookup, build/release commands, and recovery-safe integration testing. `docs/PARITY.md` maps each implemented Python feature to its Go package and automated/integration evidence; Python README TODO features are explicitly outside parity because they are not implemented behavior.

- [ ] **Step 5: Verify standalone files**

Run: `go test ./internal/config && rg -n '\.\./(config\.yml|data|image)' --glob '*.go' .`

Expected: tests pass and no parent runtime paths remain.

- [ ] **Step 6: Commit**

```bash
git add jsp-go/.gitignore jsp-go/config.yml jsp-go/data jsp-go/image jsp-go/LICENSE jsp-go/README.md jsp-go/docs jsp-go/internal/config
git commit -m "docs(go): make runtime configuration self contained"
```

### Task 5: Makefile, Release Archives, And CI

**Files:**
- Create: `Makefile`
- Create: `scripts/package.sh`
- Create: `scripts/verify-release.sh`
- Create: `.github/workflows/ci.yml`
- Modify: `docs/BUILD.md`

**Interfaces:**
- Produces: `make build|run|fmt|vet|test|check|dist|checksums|integration-test|clean`
- Produces: `dist/javsp-go_<version>_<goos>_<goarch>.{tar.gz,zip}`

- [ ] **Step 1: Write the release verifier first**

Implement `scripts/verify-release.sh` to fail unless each archive contains one platform executable plus `config.yml`, `data/facefinder`, all genre/alias files, marks/logo, README, docs, license, and third-party notices. Run it before archives exist and confirm failure.

- [ ] **Step 2: Implement deterministic build targets**

Use `CGO_ENABLED=0`, `-trimpath`, and linker values derived from `VERSION`, `git rev-parse --short HEAD`, and UTC build time. Build all six target tuples into staging directories, copy runtime files, archive Unix targets as tar.gz and Windows targets as zip, then generate SHA-256 checksums with platform-available tools.

- [ ] **Step 3: Add CI using only jsp-go paths**

Run formatting verification, vet, unit tests, host build, and six compile checks from the project root. Do not reference parent paths or real network integration fixtures.

- [ ] **Step 4: Verify build and archive contracts**

Run: `make clean check dist checksums && scripts/verify-release.sh dist`

Expected: all commands exit zero and six archives plus checksum file exist.

- [ ] **Step 5: Commit**

```bash
git add jsp-go/Makefile jsp-go/scripts jsp-go/.github jsp-go/docs/BUILD.md
git commit -m "build(go): add multi-platform standalone releases"
```

### Task 6: Feature-Parity Regression Audit

**Files:**
- Modify: `docs/PARITY.md`
- Modify: `cmd/javsp/main_test.go`
- Modify: `cmd/javsp/main.go`

**Interfaces:**
- Produces: a checked parity matrix with one evidence command/test per implemented Python feature

- [ ] **Step 1: Audit every implemented Python workflow**

Map ID/CID detection, minimum-size scan, folder/NFO skips, multipart ordering, manual ID correction, all 19 crawler IDs, retries/parallelism, CID fallback, required metadata, merge priority, JavDB cover policy, aliases/genres, all five translators, naming/defaults/path limits, NFO fields/tags/genres, cover/poster/marks/extrafanart, move/hardlink, update check/apply, proxy-free discovery, and Chromium cookies to Go code and tests.

- [ ] **Step 2: Add failing regressions for remaining semantic differences**

Add `TestShouldUsePosterFaceCropMatchesOnlyAtLabelStart` to prove a Python
`re.match` pattern such as `ARA` does not match `XARA`. Add
`TestShouldUsePosterFaceCropIgnoresInvalidPattern` to preserve non-fatal invalid
pattern handling. Tasks 1-3 already own translator proxy propagation, invalid
preview rejection, and parent-path removal.

- [ ] **Step 3: Implement start-anchored crop pattern matching**

Compile each configured pattern as `^(?:<pattern>)`, retain invalid-pattern
skip behavior, and rerun `go test ./cmd/javsp`. Record the exact test names for
all parity rows in `docs/PARITY.md`.

- [ ] **Step 4: Run complete non-network verification**

Run: `make check && go test -race ./...`

Expected: all tests pass with no race reports.

- [ ] **Step 5: Commit**

```bash
git add jsp-go
git commit -m "test(go): close javsp feature parity gaps"
```

### Task 7: Recovery-Safe Real Video Integration Test

**Files:**
- Create: `scripts/integration-test.sh`
- Modify: `Makefile`
- Modify: `docs/BUILD.md`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: `run_test_back/ATID-687-FHD.mp4`
- Produces: restored `run_test/`, integration logs, NFO, fanart, poster, and optional extra fanart

- [ ] **Step 1: Implement restore-before-run invariants**

The script must refuse to run if `run_test_back/ATID-687-FHD.mp4` is absent, replace `run_test/` from the backup using APFS clone-copy when available with normal copy fallback, compare source/working size and SHA-256 before launch, and install a trap that restores `run_test/` on failure or interruption.

- [ ] **Step 2: Run a non-destructive scan smoke test**

Run the built binary with `scanner.minimum_size=1MiB`, `scanner.manual=false`, `summarizer.move_files=false`, update checks disabled, and translator disabled. Assert ATID-687 is recognized and NFO/poster/fanart are created without modifying the backup.

- [ ] **Step 3: Run Google translation through SOCKS5**

Run with:

```bash
-o network.proxy_server=socks5://127.0.0.1:7897 \
-o translator.engine.name=google \
-o translator.fields.title=true \
-o translator.fields.plot=true
```

Assert the generated NFO has non-empty translated title, `originaltitle`, plot, cover, premiered date, and actor/genre metadata.

- [ ] **Step 4: Run destructive organizer test on restored working data**

Enable `summarizer.move_files=true` with output constrained beneath an ignored integration-output directory. Assert the video is moved or hard-linked according to configuration, filenames follow patterns, all artifacts are readable images/XML, then restore `run_test` from `run_test_back` and prove the backup hash is unchanged.

- [ ] **Step 5: Commit the harness only**

```bash
git add jsp-go/scripts/integration-test.sh jsp-go/Makefile jsp-go/docs/BUILD.md jsp-go/.gitignore
git commit -m "test(go): add recovery-safe real scrape harness"
```

### Task 8: Standalone Copy And Completion Audit

**Files:**
- Modify only when audit reveals a failing requirement.

**Interfaces:**
- Produces: authoritative evidence for every design and user requirement

- [ ] **Step 1: Copy the tracked project to an unrelated temporary directory**

Use `git archive HEAD:jsp-go | tar -x -C "$tmp"` so no parent files, untracked fixtures, or local build output are present.

- [ ] **Step 2: Verify the isolated project**

Run inside the temporary copy: `make check dist checksums` and `scripts/verify-release.sh dist`.

Expected: all unit/static/build/release checks pass using only copied files.

- [ ] **Step 3: Compile every supported target directly**

Run six `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build ./cmd/javsp` commands with unique outputs.

Expected: all exit zero, including Windows browser-cookie DPAPI builds.

- [ ] **Step 4: Run the real integration target in the working project**

Run: `JAVSP_TEST_PROXY=socks5://127.0.0.1:7897 make integration-test`

Expected: scrape, translation, image, NFO, organizer, and restore assertions pass.

- [ ] **Step 5: Re-read the design and parity matrix line by line**

For each named artifact, target, feature, platform, and invariant, cite a current file plus fresh command output. Any missing or indirect evidence reopens the owning task.

- [ ] **Step 6: Commit final audit corrections**

```bash
git add jsp-go
git commit -m "chore(go): complete standalone parity verification"
```
