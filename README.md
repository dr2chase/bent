### bent

Bent automates downloading, compiling, and running Go tests and benchmarks from various Github repositories.
By default the test/benchmark is run in a Docker container to provide some safety against accidentally making
a mess on the benchmark-running machine.

Installation:
```go get github.com/dr2chase/bent```
Also depends on burntsushi/toml, and expects that Docker is installed and available on the command line.  You can avoid the need for Docker with the `-U` command line flag, if you're okay with running benchmarks outside containers.  Alternately, if you wish to only run those benchmarks that can be compiled into a container (this is platform-dependent), use the -S flag.

Initial usage (this works if you have bent checked out on GOPATH, `bent -I` expects to find files there, this needs fixing):

```
go get github.com/dr2chase/bent
mkdir scratch
cd scratch
bent -I
cp configurations-sample.toml configurations.toml
nano configurations.toml # or use your favorite editor
bent -v # will run default set of ~50 benchmarks using supplied configuration(s)
```

The output binaries are placed in subdirectory testbin, and various
benchmark results (from building, run, and others requested) are
placed in subdirectory  bench, and the binaries are also incorporated
into Docker containers if Docker is used. Each benchmark and
configuration has a shortname, and the generated binaries combine
these shortnames, for example `gonum_mat_Tip` and `gonum_mat_Go1.9`.
Benchmark files are prefixed with a run timestamp, and grouped by
configuration, with various suffixes for the various benchmarks.
Run benchmarks appears in files with suffix `.stdout`.
Others are more obviously named, with suffixes `.build`, `.benchsize`, and `.benchdwarf`.

Flags for your use:

| Flag | meaning | example |
| --- | --- | --- |
| -v | print commands as they are run | |
| -N x | benchmark/test repeat count | -N 25 |
| -B file | benchmarks file | -B benchmarks-trial.toml |
| -C file | configurations file | -C conf_1.9_and_tip.toml |
| -S | exclude unsandboxable benchmarks | |
| -U | don't sandbox benchmarks | |
| -b list | run benchmarks in comma-separated list <br> (even if normally "disabled" )| -b uuid,gonum_topo |
| -c list | use configurations from comma-separated list <br> (even if normally "disabled") | -c Tip,Go1.9 |
| -r string | skip get and build, just run. string names Docker | |
|           | image if needed, else any non-empty will do. | -r f10cecc3eaac |
| -a N | repeat builds for build benchmarking | -a 10 |
| -s k | (build) shuffle flag, k = 0,1,2,3.  | -s 2 |
|        | Randomizes build orders to reduce sensitivity to other machine load ||
| -g | get benchmarks, but do not build or run | |
| -l | list available benchmarks and configurations, then exit | |
| -T | run tests instead of benchmarks | |
| -W | print benchmark information as a markdown table | |

### Benchmark and Configuration files

Benchmarks and configurations appear in toml format, since that is
somewhat more human-friendly than JSON and in particular allows comments.
A sample benchmark entry:
```
[[Benchmarks]]
  Name = "gonum_topo"
  Repo = "gonum.org/v1/gonum/graph/topo/"
  Tests = "Test"
  Benchmarks = "Benchmark(TarjanSCCGnp_1000_half|TarjanSCCGnp_10_tenth)"
  BuildFlags = ["-tags", "purego"]
  RunWrapper = ["tmpclr"] # this benchmark leaves messes
  # NotSandboxed = true # uncomment if cannot be run in a Docker container
  # Disabled = true # uncomment to disable benchmark
```
Here, `Name` is a short name, `Repo` is where the `go get` will find the benchmark, and `Tests` and `Benchmarks` and the
regular expressions for `go test` specifying which tests or benchmarks to run.

A sample configuration entry with all the options supplied:
```
[[Configurations]]
  Name = "Go-preempt"
  Root = "$HOME/work/go/"
 # Optional flags below
  BuildFlags = ["-gccgoflags=all=-O3 -static-libgo","-tags=noasm"] # for Gollvm
  AfterBuild = ["benchsize", "benchdwarf"]
  GcFlags = "-d=ssa/insert_resched_checks/on"
  GcEnv = ["GOMAXPROCS=1","GOGC=200"]
  RunFlags = ["-test.short"]
  RunEnv = ["GOGC=1000"]
  RunWrapper = ["cpuprofile"]
  Disabled = false
```
The `Gc...` attributes apply to the test or benchmark compilation, the `Run...` attributes apply to the test or benchmark run.
A `RunWrapper` command receives the entire command line as arguments, plus the environment variable `BENT_BINARY` set to the filename
(excluding path) of the binary being run (for example, "uuid_Tip") and `BENT_I` set to the run number for this binary.  One useful example is `cpuprofile`:
```
#!/bin/bash
# Run args as command, but run cpuprofile and then pprof to capture test cpuprofile output
pf="${BENT_BINARY}_${BENT_I}.prof" 
"$@" -test.cpuprofile="$pf"
echo cpuprofile in `pwd`/"$mf"
go tool pprof -text -flat -nodecount=20 "$pf"
```

When both configuration and benchmark wrappers the comfiguration wrapper runs the benchmark wrapper runs the actual benchmark, i.e.
```
ConfigWrapper ConfigArg BenchWrapper BenchArg ActualBenchmark
```

The `Disabled` attribute for both benchmarks and configurations removes them from normal use, but leaves them accessible to explicit request with `-b` or `-c`.
