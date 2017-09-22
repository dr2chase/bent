# bent

Bent automates downloading, compiling, and running Go tests and benchmarks from various Github repositories.
By default the test/benchmark is run in a Docker container to provide some safety against accidentally making
a mess on the benchmark-running machine.

Installation:
```go get github.com/dr2chase/bent```
Also depends on burntsushi/toml, and expects that Docker is installed and available on the command line.  You can avoid the need for Docker with the `-U` command line flag, if you're okay with running benchmarks outside containers.  Alternately, if you wish to only run those benchmarks that can be compiled into a container (this is platform-dependent), use the -S flag.

Initial usage:

```
go get github.com/dr2chase/bent
mkdir scratch
cd scratch
bent -I
cp configurations-sample.toml configurations.toml
nano configurations.toml # or use your favorite editor
bent -v # will run default set of ~50 benchmarks using supplied configuration(s)
```

The output, both binaries and the benchmark results, is all placed
in testbin, and the binaries are also incorporated into Docker containers.
Each benchmark and configuration has a shortname, and the generated binaries
combine these shortnames, for example `gonum_mat_Tip` and `gonum_mat_Go1.9`.
Benchmark output is grouped by configuration and has `.stdout` suffix, for
example `Tip.stdout` and `Go1.9.stdout`.

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
|           | image if need, else any non-empty will do. | -r f10cecc3eaac |
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
  # Disabled = true # uncomment to disable benchmark
```
Here, `Name` is a short name, `Repo` is where the `go get` will find the benchmark, and `Tests` and `Benchmarks` and the
regular expressions for `go test` specifying which tests or benchmarks to run.

A sample configuration entry with all the options supplied:
```
[[Configurations]]
  Name = "Resched"
  Root = "$HOME/GoogleDrive/work/go/"
# Optional flags below
  GcFlags = "-d=ssa/insert_resched_checks/off"
  GcEnv = ["GOMAXPROCS=1","GOGC=200"]
  RunEnv = ["GOGC=100"]
  RunFlags = ["-test.short"]
  RunWrapper = ["foo"]
  Disabled = false
```
The `Gc...` attributes apply to the test or benchmark compilation, the `Run...` attributes apply to the test or benchmark run.
A `RunWrapper` command receives the entire command line as arguments, plus the environment variable `BENT_BINARY` set to the filename
(excluding path) of the binary being run (for example, "uuid_Tip").  `foo` is supplied as a sample.

The `Disabled` attribute for both benchmarks and configurations disables them from normally use, but leaves them accessible to explicit request with `-b` or `-c`.