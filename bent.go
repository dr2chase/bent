package main

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/BurntSushi/toml"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

type Configuration struct {
	Name       string   // Short name used for binary names, mention on command line
	Root       string   // Specific Go root to use for this trial
	GcFlags    string   // GcFlags supplied to 'go test -c' for building
	GcEnv      []string // Environment variables supplied to 'go test -c' for building
	RunFlags   []string // Extra flags passed to the test binary
	RunEnv     []string // Extra environment variables passed to the test binary
	RunWrapper []string // Command and args to precede whatever the operation is; may fail in the sandbox.
	Disabled   bool     // True if this configuration is temporarily disabled
	output     bytes.Buffer
}

type Benchmark struct {
	Name         string // Short name for benchmark/test
	Contact      string // Contact not used, but may be present in description
	Repo         string // Repo + subdir where test resides, used for "go get -t -d ..."
	Tests        string // Tests to run (regex for -test.run= )
	Benchmarks   string // Benchmarks to run (regex for -test.bench= )
	NotSandboxed bool   // True if this benchmark cannot or should not be run in a container.
	Disabled     bool   // True if this benchmark is temporarily disabled.
}

type Todo struct {
	Benchmarks     []Benchmark
	Configurations []Configuration
}

var verbose int

func main() {
	benchFile := "benchmarks.toml"            // default list of benchmarks
	confFile := "configurations.toml"         // default list of configurations
	testBinDir := "testbin"                   // destination for generated binaries and benchmark outputs
	srcPath := "src/github.com/dr2chase/bent" // Used to find configuration files.
	container := ""
	N := 1
	list := false
	init := false
	test := false
	noSandbox := false
	requireSandbox := false
	getOnly := false
	runContainer := ""
	wikiTable := false // emit the tests in a form usable in a wiki table

	var benchmarksString, configurationsString string

	flag.IntVar(&N, "N", N, "benchmark/test repeat count")

	flag.StringVar(&benchmarksString, "b", "", "comma-separated list of test/benchmark names (default is all)")
	flag.StringVar(&benchFile, "B", benchFile, "name of file describing benchmarks")

	flag.StringVar(&configurationsString, "c", "", "comma-separated list of test/benchmark configurations (default is all)")
	flag.StringVar(&confFile, "C", confFile, "name of file describing configurations")

	flag.BoolVar(&noSandbox, "U", noSandbox, "run all commands unsandboxed")
	flag.BoolVar(&requireSandbox, "S", requireSandbox, "exclude unsandboxable tests/benchmarks")

	flag.BoolVar(&getOnly, "g", getOnly, "get tests/benchmarks and dependencies, do not build or run")
	flag.StringVar(&runContainer, "r", runContainer, "skip get and build, go directly to run, using specified container (any non-empty string will do for unsandboxed execution)")

	flag.BoolVar(&list, "l", list, "list available benchmarks and configurations, then exit")
	flag.BoolVar(&init, "I", init, "initialize a directory for running tests ((re)creates Dockerfile, (re)copies in benchmark and configuration files)")
	flag.BoolVar(&test, "T", test, "run tests instead of benchmarks")

	flag.BoolVar(&wikiTable, "W", wikiTable, "print benchmark info for a wiki table")

	flag.Var((*count)(&verbose), "v", "print commands and other information (more -v = print more details)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr,
			`
%s obtains the benchmarks/tests listed in %s and compiles and runs
them according to the flags and environment variables supplied in %s.
Both of these files can be changed with the -B and -C flags; the full
suite of benchmarks is somewhat time-consuming.

Running with the -l flag will list all the available tests and benchmarks.

By default the compiled tests are run in a docker container to reduce
the chances for accidents and mischief. -U requests running tests
unsandboxed, and -S limits the tests run to those that can be sandboxed
(some cannot be because of cross-compilation issues; this may imply no
change on platforms where the Docker container is not cross-compiled)

By default benchmarks are run, not tests.  -T runs tests instead

This command expects to be run in a directory that does not contain
subdirectories "pkg" and "bin", because those subdirectories may be
created (and deleted) in the process of compiling the benchmarks.
It will also extensively modify subdirectory "src".

All the test binaries and test output will appear in the subdirectory
'testbin'.  The test output is grouped by configuration to allow easy
benchmark comparisons with benchstat.
`, os.Args[0], benchFile, confFile)
	}

	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Printf("Could not get current working directory\n", err)
		os.Exit(1)
		return
	}
	gopath := cwd + "/gopath"
	err = os.Mkdir(gopath, 0775)

	// To avoid bad surprises, look for pkg and bin, if they exist, refuse to run
	_, derr := os.Stat("Dockerfile")
	_, perr := os.Stat("pkg")
	_, berr := os.Stat("bin")
	_, serr := os.Stat("src") // existence of src prevents initialization of Dockerfile

	if perr == nil || berr == nil {
		fmt.Printf("Building/running tests will trash pkg and bin, please remove, rename or run in another directory.\n")
		os.Exit(1)
	}
	if derr != nil && !init {
		// Missing Dockerfile
		fmt.Printf("Missing 'Dockerfile', please rerun with -I (init) flag if you intend to use this directory.\n")
		os.Exit(1)
	}

	// Create a Dockerfile
	if init {
		anyerr := false
		if serr == nil {
			fmt.Printf("Building/running tests will modify src, please remove, rename or initialize a different directory.\n")
			anyerr = true
		}
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			fmt.Printf("Need a GOPATH to locate configuration files in $GOPATH/src/%s.\n", srcPath)
			anyerr = true
		}
		if anyerr {
			os.Exit(1)
		}
		copyFile(gopath+"/"+srcPath, "benchmarks.toml")
		copyFile(gopath+"/"+srcPath, "benchmarks-50.toml")
		copyFile(gopath+"/"+srcPath, "benchmarks-trial.toml")
		copyFile(gopath+"/"+srcPath, "configurations-sample.toml")

		err := ioutil.WriteFile("Dockerfile",
			[]byte(`
FROM ubuntu
ADD . /
`), 0664)
		if err != nil {
			fmt.Printf("There was an error creating %s: %v\n", "Dockerfile", err)
			os.Exit(1)
			return
		}
		fmt.Printf("Created Dockerfile\n")
		return
	}

	todo := &Todo{}
	blobB, err := ioutil.ReadFile(benchFile)
	if err != nil {
		fmt.Printf("There was an error opening or reading file %s: %v\n", benchFile, err)
		os.Exit(1)
		return
	}
	blobC, err := ioutil.ReadFile(confFile)
	if err != nil {
		fmt.Printf("There was an error opening or reading file %s: %v\n", confFile, err)
		os.Exit(1)
		return
	}
	blob := append(blobB, blobC...)
	err = toml.Unmarshal(blob, todo)
	if err != nil {
		fmt.Printf("There was an error unmarshalling %s: %v\n", string(blob), err)
		os.Exit(1)
		return
	}

	var moreArgs []string
	if flag.NArg() > 0 {
		for i, arg := range flag.Args() {
			if i == 0 && (arg == "-" || arg == "--") {
				continue
			}
			moreArgs = append(moreArgs, arg)
		}
	}

	benchmarks := csToSet(benchmarksString)
	configurations := csToSet(configurationsString)

	if wikiTable {
		for _, bench := range todo.Benchmarks {
			s := bench.Benchmarks
			s = strings.Replace(s, "|", "\\|", -1)
			fmt.Printf(" | %s | | `%s` | `%s` | |\n", bench.Name, bench.Repo, s)
		}
		return
	}

	// Normalize configuration gorooot names by ensuring they end in '/'
	// Process command-line-specified configurations
	for i, trial := range todo.Configurations {
		todo.Configurations[i].Disabled = todo.Configurations[i].Disabled
		if configurations != nil {
			_, present := configurations[trial.Name]
			todo.Configurations[i].Disabled = !present
			if present {
				configurations[trial.Name] = false
			}
		}
		root := trial.Root
		if root != "" {
			root = os.ExpandEnv(root)
			if '/' != root[len(root)-1] {
				root = root + "/"
			}
			todo.Configurations[i].Root = root
		}
	}
	for b, v := range configurations {
		if v {
			fmt.Printf("Configuration %s listed after -c does not appear in %s\n", b, confFile)
			os.Exit(1)
		}
	}

	// Normalize benchmark names by removing any trailing '/'.
	// Normalize Test and Benchmark specs by replacing missing value with something that won't match anything.
	// Process command-line-specified benchmarks
	for i, bench := range todo.Benchmarks {
		todo.Benchmarks[i].Disabled = todo.Benchmarks[i].Disabled
		if benchmarks != nil {
			_, present := benchmarks[bench.Name]
			todo.Benchmarks[i].Disabled = !present
			if present {
				benchmarks[bench.Name] = false
			}
		}
		// Trim possible trailing slash, do not want
		if '/' == bench.Repo[len(bench.Repo)-1] {
			bench.Repo = bench.Repo[:len(bench.Repo)-1]
			todo.Benchmarks[i].Repo = bench.Repo
		}
		if "" == bench.Tests || !test {
			todo.Benchmarks[i].Tests = "none"
		}
		if "" == bench.Benchmarks || test {
			todo.Benchmarks[i].Benchmarks = "none"
		}
		if noSandbox {
			todo.Benchmarks[i].NotSandboxed = true
		}
		if requireSandbox && todo.Benchmarks[i].NotSandboxed {
			if runtime.GOOS == "linux" {
				fmt.Printf("Removing sandbox for %s\n", bench.Name)
				todo.Benchmarks[i].NotSandboxed = false
			} else {
				fmt.Printf("Disabling %s because it requires sandbox\n", bench.Name)
				todo.Benchmarks[i].Disabled = true
			}
		}
	}
	for b, v := range benchmarks {
		if v {
			fmt.Printf("Benchmark %s listed after -b does not appear in %s\n", b, benchFile)
			os.Exit(1)
		}
	}

	// If more verbose, print the normalized configuration.
	if verbose > 1 {
		buf := new(bytes.Buffer)
		if err := toml.NewEncoder(buf).Encode(todo); err != nil {
			fmt.Printf("There was an error encoding %v: %v\n", todo, err)
			os.Exit(1)
		}
		fmt.Println(buf.String())
	}

	if list {
		fmt.Println("Benchmarks:")
		for _, x := range todo.Benchmarks {
			s := x.Name + " (repo=" + x.Repo + ")"
			if x.Disabled {
				s += " (disabled)"
			}
			fmt.Printf("   %s\n", s)
		}
		fmt.Println("Configurations:")
		for _, x := range todo.Configurations {
			s := x.Name
			if x.Root != "" {
				s += " (goroot=" + x.Root + ")"
			}
			if x.Disabled {
				s += " (disabled)"
			}
			fmt.Printf("   %s\n", s)
		}
		return
	}

	var defaultEnv []string
	defaultEnv = inheritEnv(defaultEnv, "PATH")
	defaultEnv = inheritEnv(defaultEnv, "USER")
	defaultEnv = inheritEnv(defaultEnv, "HOME")
	defaultEnv = inheritEnv(defaultEnv, "SHELL")
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GO") {
			defaultEnv = append(defaultEnv, e)
		}
	}
	defaultEnv = replaceEnv(defaultEnv, "GOPATH", gopath)

	var needSandbox bool // true if any benchmark needs a sandbox

	if runContainer == "" {
		if verbose == 0 {
			fmt.Print("Go getting")
		}

		// Obtain (go get -d -t -v bench.Repo) all benchmarks, once, populating src
		for _, bench := range todo.Benchmarks {
			if bench.Disabled {
				continue
			}
			cmd := exec.Command("go", "get", "-d", "-t", "-v", bench.Repo)
			cmd.Env = defaultEnv
			if !bench.NotSandboxed { // Do this so that OS-dependent dependencies are done correctly.
				cmd.Env = replaceEnv(cmd.Env, "GOOS", "linux")
			}
			if verbose > 0 {
				fmt.Println(asCommandLine(cwd, cmd))
			} else {
				fmt.Print(".")
			}
			_, err := cmd.Output()
			if err != nil {
				ee := err.(*exec.ExitError)
				fmt.Printf("There was an error running 'go get', stderr = %s\n", ee.Stderr)
				os.Exit(2)
				return
			}
		}
		if verbose == 0 {
			fmt.Println()
		}

		if getOnly {
			return
		}

		// Compile tests and move to ./testbin/Bench_Config.
		// If any test needs sandboxing, then one docker container will be created
		// (that contains all the tests).

		err = os.Mkdir(testBinDir, 0775)
		// Ignore the error -- TODO note the difference between exists already and other errors.
		if verbose == 0 {
			fmt.Print("Compiling")
		}
		for _, config := range todo.Configurations {
			if config.Disabled {
				continue
			}

			root := config.Root

			for _, bench := range todo.Benchmarks {
				if bench.Disabled {
					continue
				}
				gocmd := "go"
				if root != "" {
					gocmd = root + "bin/" + gocmd
				}
				cmd := exec.Command(gocmd, "test", "-a", "-c")
				if config.GcFlags != "" {
					cmd.Args = append(cmd.Args, "-gcflags="+config.GcFlags)
				}
				cmd.Args = append(cmd.Args, ".")
				cmd.Dir = gopath + "/src/" + bench.Repo
				cmd.Env = defaultEnv
				if !bench.NotSandboxed {
					cmd.Env = replaceEnv(cmd.Env, "GOOS", "linux")
					needSandbox = true
				}
				if root != "" {
					cmd.Env = replaceEnv(cmd.Env, "GOROOT", root)
				}
				cmd.Env = append(cmd.Env, config.GcEnv...)

				if verbose > 0 {
					fmt.Println(asCommandLine(cwd, cmd))
				} else {
					fmt.Print(".")
				}
				output, err := cmd.CombinedOutput()
				if err != nil {
					fmt.Println(string(output))
					switch e := err.(type) {
					case *exec.ExitError:
						fmt.Printf("There was an error running 'go test', stderr = %s\n", e.Stderr)
					default:
						fmt.Printf("There was an error running 'go test', %v\n", e)
					}
					os.Exit(3)
					return
				}
				if verbose > 0 {
					fmt.Print(string(output))
				}
				// Move generated binary to well known place.
				from := cmd.Dir + "/" + bench.testBinaryName()
				to := testBinDir + "/" + bench.Name + "_" + config.Name
				err = os.Rename(from, to)
				if err != nil {
					fmt.Printf("There was an error renaming %s to %s, %v\n", from, to, err)
					os.Exit(1)
				}
			}
			os.RemoveAll("pkg")
			os.RemoveAll("bin")
		}
		if verbose == 0 {
			fmt.Println()
		}

		// As needed, create the sandbox.
		if needSandbox {
			if verbose == 0 {
				fmt.Print("Making sandbox")
			}
			cmd := exec.Command("docker", "build", "-q", ".")
			if verbose > 0 {
				fmt.Println(asCommandLine(cwd, cmd))
			}
			// capture standard output to get container name
			output, err := cmd.Output()
			if err != nil {
				ee := err.(*exec.ExitError)
				fmt.Printf("There was an error running 'docker build', stderr = %s\n", ee.Stderr)
				os.Exit(4)
				return
			}
			container = strings.TrimSpace(string(output))
			if verbose == 0 {
				fmt.Println()
			}
			fmt.Printf("Container for sandboxed bench/test runs is %s\n", container)
		}
	} else {
		container = runContainer
	}

	// If there's an error running one of the benchmarks, report what we've got, please.
	defer func(t *Todo) {
		for _, config := range todo.Configurations {
			ioutil.WriteFile(testBinDir+"/"+config.Name+".stdout", config.output.Bytes(), os.ModePerm)
		}
		if needSandbox {
			// Print this twice so it doesn't get missed.
			fmt.Printf("Container for sandboxed bench/test runs is %s\n", container)
		}
	}(todo)

	for i := 0; i < N; i++ {
		// For each configuration, run all the benchmarks.
		for j, config := range todo.Configurations {
			if config.Disabled {
				continue
			}
			root := config.Root
			configWrapper := ""
			if len(config.RunWrapper) > 0 {
				// Prepend slash, for now it runs from root of container or cwd + configWrapper if not sandboxed.
				configWrapper = "/" + config.RunWrapper[0]
			}

			for _, b := range todo.Benchmarks {
				if b.Disabled {
					continue
				}
				testBinaryName := b.Name + "_" + config.Name
				if b.NotSandboxed {
					testdir := gopath + "/src/" + b.Repo
					bin := cwd + "/" + testBinDir + "/" + testBinaryName
					var cmd *exec.Cmd
					if configWrapper == "" {
						cmd = exec.Command(bin, "-test.run="+b.Tests, "-test.bench="+b.Benchmarks)
					} else {
						cmd = exec.Command(cwd+configWrapper, config.RunWrapper[1:]...)
						cmd.Args = append(cmd.Args, bin, "-test.run="+b.Tests, "-test.bench="+b.Benchmarks)
					}
					cmd.Dir = testdir
					cmd.Env = defaultEnv
					if root != "" {
						cmd.Env = replaceEnv(cmd.Env, "GOROOT", root)
					}
					cmd.Env = append(cmd.Env, config.RunEnv...)
					cmd.Env = append(cmd.Env)
					cmd.Args = append(cmd.Args, config.RunFlags...)
					cmd.Args = append(cmd.Args, moreArgs...)
					todo.Configurations[j].runBinary(cwd, cmd)
				} else {
					// docker run --net=none -e GOROOT=... -w /src/github.com/minio/minio/cmd $D /testbin/cmd_Config.test -test.short -test.run=Nope -test.v -test.bench=Benchmark'(Get|Put|List)'
					testdir := "/gopath/src/" + b.Repo
					cmd := exec.Command("docker", "run", "--net=none",
						"-w", testdir)
					for _, e := range config.RunEnv {
						cmd.Args = append(cmd.Args, "-e", e)
					}
					cmd.Args = append(cmd.Args, "-e", "BENT_BINARY="+testBinaryName)
					cmd.Args = append(cmd.Args, container)
					if configWrapper != "" {
						cmd.Args = append(cmd.Args, configWrapper)
						cmd.Args = append(cmd.Args, config.RunWrapper[1:]...)
					}
					cmd.Args = append(cmd.Args, "/"+testBinDir+"/"+testBinaryName, "-test.run="+b.Tests, "-test.bench="+b.Benchmarks)
					cmd.Args = append(cmd.Args, config.RunFlags...)
					cmd.Args = append(cmd.Args, moreArgs...)
					todo.Configurations[j].runBinary(cwd, cmd)
				}
			}
		}
	}
}

func escape(s string) string {
	s = strings.Replace(s, "\\", "\\\\", -1)
	s = strings.Replace(s, "'", "\\'", -1)
	// Conservative guess at characters that will force quoting
	if strings.ContainsAny(s, "\\ ;#*&$~?!|[]()<>{}`") {
		s = " '" + s + "'"
	} else {
		s = " " + s
	}
	return s
}

// asCommandLine renders cmd as something that could be copy-and-pasted into a command line
func asCommandLine(cwd string, cmd *exec.Cmd) string {
	s := "("
	if cmd.Dir != "" && cmd.Dir != cwd {
		s += "cd" + escape(cmd.Dir) + ";"
	}
	for _, e := range cmd.Env {
		if !strings.HasPrefix(e, "PATH=") &&
			!strings.HasPrefix(e, "HOME=") &&
			!strings.HasPrefix(e, "USER=") &&
			!strings.HasPrefix(e, "SHELL=") {
			s += escape(e)
		}
	}
	for _, a := range cmd.Args {
		s += escape(a)
	}
	s += " )"
	return s
}

func copyFile(fromDir, file string) {
	bytes, err := ioutil.ReadFile(fromDir + "/" + file)
	if err != nil {
		fmt.Printf("Error reading %s\n", fromDir+"/"+file)
		os.Exit(1)
	}
	err = ioutil.WriteFile(file, bytes, 0664)
	if err != nil {
		fmt.Printf("Error writing %s\n", file)
		os.Exit(1)
	}
	fmt.Printf("Copied %s to current directory\n", fromDir+"/"+file)
}

// runBinary runs cmd and displays the output.
// If the command returns an error, runBinary calls os.Exit(5)
func (c *Configuration) runBinary(cwd string, cmd *exec.Cmd) {
	line := asCommandLine(cwd, cmd)
	if verbose > 0 {
		fmt.Println(line)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Printf("There was an error [stdoutpipe] running '%s', %v\n", line, err)
		os.Exit(5)
	}

	bytes := make([]byte, 4096)
	err = cmd.Start()
	for {
		n, err := stdout.Read(bytes)
		if n > 0 {
			c.output.Write(bytes[0:n])
			fmt.Print(string(bytes[0:n]))
		}
		if err == io.EOF || n == 0 {
			break
		}
		if err != nil {
			fmt.Printf("There was an error [read stdout] running '%s', %v\n", line, err)
			os.Exit(5)
		}
	}

	err = cmd.Wait()

	if err != nil {
		switch e := err.(type) {
		case *exec.ExitError:
			fmt.Printf("There was an error running '%s', stderr = %s\n", line, e.Stderr)
		default:
			fmt.Printf("There was an error running '%s', %v\n", line, e)

		}
		os.Exit(5)
	}
}

// testBinaryName returns the name of the binary produced by "go test -c"
func (b *Benchmark) testBinaryName() string {
	return b.Repo[strings.LastIndex(b.Repo, "/")+1:] + ".test"
}

// inheritEnv extracts ev from the os environment and adds
// returns env extended with that new environment variable.
// Does not check if ev already exists in env.
func inheritEnv(env []string, ev string) []string {
	evv := os.Getenv(ev)
	if evv != "" {
		env = append(env, ev+"="+evv)
	}
	return env
}

// replaceEnv returns a new environment derived from env
// by removing any existing definition of ev and adding ev=evv.
func replaceEnv(env []string, ev string, evv string) []string {
	evplus := ev + "="
	var found bool
	for i, v := range env {
		if strings.HasPrefix(v, evplus) {
			found = true
			env[i] = evplus + evv
		}
	}
	if !found {
		env = append(env, evplus+evv)
	}
	return env
}

// csToset converts a commo-separated string into the set of strings between the commas.
func csToSet(s string) map[string]bool {
	if s == "" {
		return nil
	}
	m := make(map[string]bool)
	ss := strings.Split(s, ",")
	for _, sss := range ss {
		m[sss] = true
	}
	return m
}

// count is a flag.Value that is like a flag.Bool and a flag.Int.
// If used as -name, it increments the count, but -name=x sets the count.
// Used for verbose flag -v.
type count int

func (c *count) String() string {
	return fmt.Sprint(int(*c))
}

func (c *count) Set(s string) error {
	switch s {
	case "true":
		*c++
	case "false":
		*c = 0
	default:
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid count %q", s)
		}
		*c = count(n)
	}
	return nil
}

func (c *count) IsBoolFlag() bool {
	return true
}
