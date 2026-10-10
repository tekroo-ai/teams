// Package verification bounds heavy Go verification in the daemon process.
// It does not serialize agent reasoning or grant test execution authority.
package verification

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const Policy = "two-verifiers/whole-max1/go-p2-procs2/v2"

var slots verificationQueue

// Acquire shares bounded slots across candidate preflights and native test tools.
// Queueing consumes the caller's existing deadline; cancellation never leaks it.
func Acquire(ctx context.Context) (func(), error) {
	return AcquireClass(ctx, WholeModule)
}

// GoCommand bounds package compilation and in-package parallel tests without
// changing selected packages, assertions, tags, or timeouts. Test-binary args
// after -args/-- are preserved verbatim and are never mistaken for Go flags.
func GoCommand(argv []string) ([]string, bool) {
	if len(argv) < 2 || filepath.Base(argv[0]) != "go" || (argv[1] != "test" && argv[1] != "build") {
		return argv, false
	}
	result := []string{argv[0], argv[1], "-p=2"}
	packages := 2
	parallel := 2
	for i := 2; i < len(argv); i++ {
		arg := argv[i]
		if arg == "-args" || arg == "--" {
			result = append(result, argv[i:]...)
			break
		}
		// Do not interpret a Go flag's value (e.g. -run '-p=9') as a
		// resource option. Preserve all non-resource flag/value pairs.
		if goFlagTakesValue(arg) && i+1 < len(argv) {
			result = append(result, arg, argv[i+1])
			i++
			continue
		}
		if strings.HasPrefix(arg, "-p=") || argv[1] == "test" && strings.HasPrefix(arg, "-parallel=") {
			n, err := strconv.Atoi(strings.SplitN(arg, "=", 2)[1])
			if err != nil || n < 1 {
				result = append(result, arg)
				continue
			}
			if strings.HasPrefix(arg, "-parallel=") {
				parallel = min(parallel, n)
			} else {
				packages = min(packages, n)
			}
			continue
		}
		if arg == "-p" || argv[1] == "test" && arg == "-parallel" {
			// Leave malformed flags for Go to reject, never repair a bad command.
			if i+1 == len(argv) {
				result = append(result, arg)
				break
			}
			n, err := strconv.Atoi(argv[i+1])
			if err != nil || n < 1 {
				result = append(result, arg, argv[i+1])
				i++
				continue
			}
			if arg == "-parallel" {
				parallel = min(parallel, n)
			} else {
				packages = min(packages, n)
			}
			i++
			continue
		}
		result = append(result, arg)
	}
	result[2] = "-p=" + strconv.Itoa(packages)
	if argv[1] == "test" {
		result = append(result[:3], append([]string{"-parallel=" + strconv.Itoa(parallel)}, result[3:]...)...)
	}
	return result, true
}

func goFlagTakesValue(arg string) bool {
	switch arg {
	case "-asmflags", "-buildmode", "-compiler", "-covermode", "-coverpkg", "-gcflags", "-installsuffix", "-ldflags", "-mod", "-modfile", "-overlay", "-pkgdir", "-tags", "-toolexec", "-o", "-pgo",
		"-run", "-skip", "-bench", "-benchtime", "-count", "-cpu", "-coverprofile", "-timeout", "-fuzz", "-fuzztime", "-fuzzminimizetime", "-list", "-shuffle", "-mutexprofile", "-blockprofile", "-blockprofilerate", "-mutexprofilefraction", "-cpuprofile", "-memprofile", "-memprofilerate", "-outputdir", "-trace", "-vet":
		return true
	}
	return false
}

func Environment(env []string) []string {
	result := make([]string, 0, len(env)+1)
	procs := 2
	for _, item := range env {
		if !strings.HasPrefix(item, "GOMAXPROCS=") {
			result = append(result, item)
		} else if n, err := strconv.Atoi(strings.TrimPrefix(item, "GOMAXPROCS=")); err == nil && n > 0 {
			procs = min(procs, n)
		}
	}
	return append(result, "GOMAXPROCS="+strconv.Itoa(procs))
}

// TimeoutDetector observes the entire drained stream, not just a clipped
// receipt prefix. Only Go's explicit timeout panic counts; an assertion that
// mentions a deadline remains a test failure. Supports go test -json as well.
type TimeoutDetector struct {
	line     []byte
	tooLong  bool
	timedOut bool
	marker   string
}

func (d *TimeoutDetector) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			d.inspect()
			d.line = d.line[:0]
			d.tooLong = false
			continue
		}
		if len(d.line) < 4096 {
			d.line = append(d.line, b)
		} else {
			d.tooLong = true
		}
	}
	return len(p), nil
}

func (d *TimeoutDetector) inspect() {
	if d.tooLong {
		return
	}
	line := strings.TrimSuffix(string(d.line), "\r")
	if strings.HasPrefix(line, "{") {
		var event struct {
			Action string
			Output string
		}
		if json.Unmarshal(d.line, &event) != nil || event.Action != "output" {
			return
		}
		line = strings.TrimSuffix(event.Output, "\n")
	}
	const prefix = "panic: test timed out after "
	if !strings.HasPrefix(line, prefix) {
		return
	}
	duration, err := time.ParseDuration(strings.TrimPrefix(line, prefix))
	if err == nil && duration > 0 {
		d.timedOut = true
		if d.marker == "" {
			d.marker = string(d.line)
		}
	}
}

func (d *TimeoutDetector) TimedOut() bool   { d.inspect(); return d.timedOut }
func (d *TimeoutDetector) Evidence() string { d.inspect(); return d.marker }
