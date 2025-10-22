# NIST Statistical Test Suite integration (CGO)

This document explains how to integrate the NIST Statistical Test Suite (https://github.com/terrillmoore/NIST-Statistical-Test-Suite) with this repository via CGO.

High-level approach
- Add the NIST repository as a submodule under `third_party/NIST-Statistical-Test-Suite`.
- Build a static library (libnist.a) from the NIST C sources.
- Build this Go project with CGO enabled so `internal/nist` links against the static library.

Steps

1. Add the NIST repository as a submodule

```pwsh
git submodule add https://github.com/terrillmoore/NIST-Statistical-Test-Suite third_party/NIST-Statistical-Test-Suite
git submodule update --init --recursive
```

2. Build a static library

Follow the build instructions in the NIST repository. There is no single canonical build script, but a common flow is:

On Linux/macOS (example):

```sh
cd third_party/NIST-Statistical-Test-Suite
make clean
make
# produce libnist.a in the repo root or a lib/ subdir
```

On Windows (MSYS/MinGW) use an environment that provides make and a compatible toolchain and produce a static .a library.

Important: ensure `libnist.a` is available at `third_party/NIST-Statistical-Test-Suite/libnist.a` and public headers are reachable under `third_party/NIST-Statistical-Test-Suite` (the cgo directives in `internal/nist/nist.go` expect that layout).

3. Build the Go project with CGO enabled

Set `CGO_ENABLED=1` and ensure a C compiler is present in PATH. Example (Linux/macOS):

```sh
export CGO_ENABLED=1
go build ./...
```

On Windows (PowerShell with mingw):

```pwsh
$env:CGO_ENABLED = "1"
go build ./...
```

4. Running the example

Generate or provide a raw bit/byte file and run:

```pwsh
# build the example tool
go build -o tools/run_nist.exe ./tools

# run it
tools\run_nist.exe path\to\random_bytes.bin
```

Notes and caveats
- The current `internal/nist/nist.go` contains a placeholder cgo preamble and a placeholder function call. After you build the NIST library, update the preamble to include the correct header files and replace the placeholder call with the actual test function signatures.
- CGO complicates cross-compilation and CI. For CI, consider building the NIST library in the CI image or using a container that has a C toolchain installed.
- As a safe default, the repository includes `internal/nist/nist_stub.go` which builds when CGO isn't enabled and returns a helpful error at runtime.

Next steps
- I can update `internal/nist/nist.go` to match specific NIST header names and function signatures once you have the library and headers available locally, or I can try to auto-detect common function names and wire one test as a proof-of-concept. Tell me which test(s) you'd like wired first (for example: monobit, runs, frequency, etc.).
