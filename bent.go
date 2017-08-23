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
	"strconv"
	"strings"
)

type Configuration struct {
	Name     string   // Short name used for binary names, mention on command line
	Root     string   // Specific Go root to use for this trial
	GcFlags  string   // GcFlags supplied to 'go test -c' for building
	GcEnv    []string // Environment variables supplied to 'go test -c' for building
	RunFlags []string // Extra flags passed to the test binary
	RunEnv   []string // Extra environment variables passed to the test binary
	Disabled bool     // True if this configuration is temporarily disabled
	output   bytes.Buffer
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

func main() {
	benchfile := "benchmarks.toml"    // default list of benchmarks
	conffile := "configurations.toml" // default list of configurations
	testBinDir := "testbin"           // destination for generated binaries and benchmark outputs

	container := ""
	N := 1
	list := false
	init := false
	test := false
	nosandbox := false

	var benchmarksString, configurationsString string

	flag.IntVar(&N, "N", N, "benchmark/test repeat count")

	flag.StringVar(&benchmarksString, "b", "", "comma-separated list of test/benchmark names")
	flag.StringVar(&benchfile, "B", benchfile, "name of file describing benchmarks")

	flag.StringVar(&configurationsString, "c", "", "comma-separated list of test/benchmark configurations")
	flag.StringVar(&conffile, "C", conffile, "name of file describing configurations")

	flag.BoolVar(&nosandbox, "s", nosandbox, "don't run commands in a docker sandbox")
	flag.BoolVar(&list, "l", list, "list available benchmarks and configurations, then exit")
	flag.BoolVar(&init, "i", init, "initialize a directory for running tests (creates Dockerfile)")
	flag.BoolVar(&test, "t", test, "run tests instead of benchmarks")

	flag.Var((*count)(&verbose), "v", "print commands and other information (more -v = print more details)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr,
			`
%s obtains the benchmarks/tests listed in %s and compiles and runs
them according to the flags and environment variables supplied in %s.
By default the compiled tests are run in a docker container to reduce
the chances for accidents and mischief.

By default benchmarks are run, not tests.

This command expects to be run in a directory that does not contain
subdirectories "pkg" and "bin", because those subdirectories may be
created (and deleted) in the process of compiling the benchmarks.
It will also extensively modify subdirectory "src".

All the test binaries and test output will appear in the subdirectory
'testbin'.  The test output is grouped by configuration to allow easy
benchmark comparisons with benchstat.
`, os.Args[0], benchfile, conffile)
	}

	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Printf("Could not get current working directory\n", err)
		os.Exit(1)
		return
	}

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
		fmt.Printf("Missing 'Dockerfile', please rerun with -i (init) flag if you intend to use this directory.\n")
		os.Exit(1)
	}

	// Create a Dockerfile
	if init {
		if serr == nil {
			fmt.Printf("Building/running tests will modify src, please remove, rename or initialize another directory.\n")
			os.Exit(1)
		}
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

	}

	todo := &Todo{}
	blobB, err := ioutil.ReadFile(benchfile)
	if err != nil {
		fmt.Printf("There was an error opening or reading file %s: %v\n", benchfile, err)
		os.Exit(1)
		return
	}
	blobC, err := ioutil.ReadFile(conffile)
	if err != nil {
		fmt.Printf("There was an error opening or reading file %s: %v\n", conffile, err)
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
			fmt.Printf("Configuration %s listed after -c does not appear in %s\n", b, conffile)
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
		if nosandbox {
			todo.Benchmarks[i].NotSandboxed = true
		}
	}
	for b, v := range benchmarks {
		if v {
			fmt.Printf("Benchmark %s listed after -b does not appear in %s\n", b, benchfile)
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
	defaultEnv = replaceEnv(defaultEnv, "GOPATH", cwd)

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
		fmt.Printf("%v\n", cmd.Args)
		_, err := cmd.Output()
		if err != nil {
			ee := err.(*exec.ExitError)
			fmt.Printf("There was an error running 'go get', stderr = %s\n", ee.Stderr)
			os.Exit(2)
			return
		}
	}

	// Compile tests and move to ./testbin/Bench_Config.
	// If any test needs sandboxing, then one docker container will be created
	// (that contains all the tests).

	err = os.Mkdir(testBinDir, 0775)
	// Ignore the error -- TODO note the difference between exists already and other errors.
	var needSandbox bool // true if any benchmark needs a sandbox
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
			cmd.Dir = cwd + "/src/" + bench.Repo
			cmd.Env = defaultEnv
			if !bench.NotSandboxed {
				cmd.Env = replaceEnv(cmd.Env, "GOOS", "linux")
			}
			if root != "" {
				cmd.Env = replaceEnv(cmd.Env, "GOROOT", root)
			}
			cmd.Env = append(cmd.Env, config.GcEnv...)

			sandboxed := ""
			if !bench.NotSandboxed {
				needSandbox = true
				sandboxed = " (sandboxed)"
			}

			fmt.Printf("Compiling %s for %s%s\n", bench.Name, config.Name, sandboxed)
			if verbose > 0 {
				fmt.Printf("(dir=%v, env=%s) %v\n", cmd.Dir, cmd.Env, cmd.Args)
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
			fmt.Print(string(output))
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

	// As needed, create the sandbox.
	if needSandbox {
		cmd := exec.Command("docker", "build", "-q", ".")
		// capture standard output to get name
		if verbose > 0 {
			fmt.Printf("(dir=%v, env=%s) %v\n", cmd.Dir, cmd.Env, cmd.Args)
		}
		output, err := cmd.Output()
		if err != nil {
			ee := err.(*exec.ExitError)
			fmt.Printf("There was an error running 'docker build', stderr = %s\n", ee.Stderr)
			os.Exit(4)
			return
		}
		container = strings.TrimSpace(string(output))
		fmt.Printf("Container for sandboxed bench/test runs is %s\n", container)
	}

	// If there's an error running one of the benchmarks, report what we've got, please.
	defer func(t *Todo) {
		for _, config := range todo.Configurations {
			ioutil.WriteFile(testBinDir+"/"+config.Name+".stdout", config.output.Bytes(), os.ModePerm)
		}
	}(todo)

	for i := 0; i < N; i++ {
		// For each configuration, run all the benchmarks.
		for j, config := range todo.Configurations {
			if config.Disabled {
				continue
			}
			root := config.Root

			for _, b := range todo.Benchmarks {
				if b.Disabled {
					continue
				}
				testBinaryName := b.Name + "_" + config.Name
				if b.NotSandboxed {
					testdir := cwd + "/src/" + b.Repo
					bin := cwd + "/" + testBinDir + "/" + testBinaryName
					cmd := exec.Command(bin, "-test.run="+b.Tests, "-test.bench="+b.Benchmarks)
					cmd.Dir = testdir
					cmd.Env = defaultEnv
					if root != "" {
						cmd.Env = replaceEnv(cmd.Env, "GOROOT", root)
					}
					cmd.Env = append(cmd.Env, config.RunEnv...)
					cmd.Args = append(cmd.Args, config.RunFlags...)
					cmd.Args = append(cmd.Args, moreArgs...)
					todo.Configurations[j].runBinary("cd '"+testdir+"';", cmd)
				} else {
					// docker run --net=none -e GOROOT=... -w /src/github.com/minio/minio/cmd $D /testbin/cmd_Config.test -test.short -test.run=Nope -test.v -test.bench=Benchmark'(Get|Put|List)'
					testdir := "/src/" + b.Repo
					cmd := exec.Command("docker", "run", "--net=none",
						"-w", testdir)
					for _, e := range config.RunEnv {
						cmd.Args = append(cmd.Args, "-e", e)
					}
					cmd.Args = append(cmd.Args, container, "/"+testBinDir+"/"+testBinaryName, "-test.run="+b.Tests, "-test.bench="+b.Benchmarks)
					cmd.Args = append(cmd.Args, config.RunFlags...)
					cmd.Args = append(cmd.Args, moreArgs...)
					todo.Configurations[j].runBinary("", cmd)
				}
			}
		}
	}
}

// runBinary runs cmd and displays the output.
// If the command returns an error, runBinary calls os.Exit(5)
func (c *Configuration) runBinary(line string, cmd *exec.Cmd) {
	for _, s := range cmd.Args {
		s = strings.Replace(s, "\\", "\\\\", -1)
		s = strings.Replace(s, "'", "\\'", -1)
		if line != "" {
			line += " "
		}
		line += "'" + s + "'"
	}
	if verbose > 1 {
		fmt.Print("(dir=%v, env=%s) ", cmd.Dir, cmd.Env)
	}
	fmt.Println(line)
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

var verbose int

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
