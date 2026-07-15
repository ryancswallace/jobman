package policy

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"
)

func TestEvaluateTimeConditions(t *testing.T) {
	t.Parallel()

	target := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{"before", target.Add(-time.Nanosecond), false},
		{"equal", target, true},
		{"after", target.Add(time.Nanosecond), true},
	} {
		t.Run("until "+test.name, func(t *testing.T) {
			t.Parallel()

			result, err := EvaluateUntil(test.now, target)
			if err != nil {
				t.Fatalf("EvaluateUntil() error = %v", err)
			}
			if result.Satisfied != test.want {
				t.Fatalf("EvaluateUntil() satisfied = %t, want %t", result.Satisfied, test.want)
			}
		})
	}

	acceptedAt := target
	for _, test := range []struct {
		name  string
		now   time.Time
		delay time.Duration
		want  bool
	}{
		{"zero", acceptedAt, 0, true},
		{"before", acceptedAt.Add(9 * time.Second), 10 * time.Second, false},
		{"equal", acceptedAt.Add(10 * time.Second), 10 * time.Second, true},
		{"after", acceptedAt.Add(11 * time.Second), 10 * time.Second, true},
	} {
		t.Run("delay "+test.name, func(t *testing.T) {
			t.Parallel()

			result, err := EvaluateDelay(test.now, acceptedAt, test.delay)
			if err != nil {
				t.Fatalf("EvaluateDelay() error = %v", err)
			}
			if result.Satisfied != test.want {
				t.Fatalf("EvaluateDelay() satisfied = %t, want %t", result.Satisfied, test.want)
			}
		})
	}

	if _, err := EvaluateUntil(time.Time{}, target); err == nil {
		t.Error("EvaluateUntil(zero now) unexpectedly succeeded")
	}
	if _, err := EvaluateDelay(acceptedAt.Add(-time.Second), acceptedAt, time.Second); err == nil {
		t.Error("EvaluateDelay(before acceptance) unexpectedly succeeded")
	}
	if _, err := EvaluateDelay(acceptedAt, acceptedAt, -1); err == nil {
		t.Error("EvaluateDelay(negative) unexpectedly succeeded")
	}
}

func TestCombineWaitConditions(t *testing.T) {
	t.Parallel()

	nonfatalErr := errors.New("temporary")
	fatalErr := errors.New("fatal")
	tests := []struct {
		name    string
		mode    WaitMode
		results []ConditionResult
		want    WaitDecision
	}{
		{"zero all", WaitModeAll, nil, WaitDecision{Satisfied: true}},
		{"zero any", WaitModeAny, nil, WaitDecision{Satisfied: true}},
		{"default all", "", []ConditionResult{{Satisfied: true}, {Satisfied: true}}, WaitDecision{Satisfied: true}},
		{"all pending", WaitModeAll, []ConditionResult{{Satisfied: true}, {}}, WaitDecision{}},
		{"any satisfied", WaitModeAny, []ConditionResult{{}, {Satisfied: true}}, WaitDecision{Satisfied: true}},
		{
			"any retains nonfatal error",
			WaitModeAny,
			[]ConditionResult{{Err: nonfatalErr}, {Satisfied: true}},
			WaitDecision{Satisfied: true, Err: nonfatalErr},
		},
		{
			"fatal overrides satisfaction",
			WaitModeAny,
			[]ConditionResult{{Fatal: true, Err: fatalErr}, {Satisfied: true}},
			WaitDecision{Fatal: true, Err: fatalErr},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := CombineWaitConditions(test.mode, test.results)
			if err != nil {
				t.Fatalf("CombineWaitConditions() error = %v", err)
			}
			if got.Satisfied != test.want.Satisfied || got.Fatal != test.want.Fatal ||
				!errors.Is(got.Err, test.want.Err) {
				t.Fatalf("CombineWaitConditions() = %+v, want %+v", got, test.want)
			}
		})
	}

	invalid := []struct {
		mode    WaitMode
		results []ConditionResult
	}{
		{mode: "xor"},
		{mode: WaitModeAll, results: []ConditionResult{{Satisfied: true, Err: errors.New("error")}}},
		{mode: WaitModeAll, results: []ConditionResult{{Fatal: true}}},
	}
	for _, test := range invalid {
		if _, err := CombineWaitConditions(test.mode, test.results); err == nil {
			t.Errorf("CombineWaitConditions(%q, %+v) unexpectedly succeeded", test.mode, test.results)
		}
	}
}

func TestWaitCombinationBooleanProperty(t *testing.T) {
	t.Parallel()

	for count := 1; count <= 8; count++ {
		for mask := range 1 << count {
			results := make([]ConditionResult, count)
			all := true
			anySatisfied := false
			for index := range count {
				results[index].Satisfied = mask&(1<<index) != 0
				all = all && results[index].Satisfied
				anySatisfied = anySatisfied || results[index].Satisfied
			}

			allDecision, err := CombineWaitConditions(WaitModeAll, results)
			if err != nil {
				t.Fatalf("CombineWaitConditions(all,count=%d,mask=%d) error = %v", count, mask, err)
			}
			anyDecision, err := CombineWaitConditions(WaitModeAny, results)
			if err != nil {
				t.Fatalf("CombineWaitConditions(any,count=%d,mask=%d) error = %v", count, mask, err)
			}
			if allDecision.Satisfied != all || anyDecision.Satisfied != anySatisfied {
				t.Fatalf(
					"count=%d mask=%d got (all=%t,any=%t), want (all=%t,any=%t)",
					count,
					mask,
					allDecision.Satisfied,
					anyDecision.Satisfied,
					all,
					anySatisfied,
				)
			}
		}
	}
}

type fakeFileInfo struct {
	mode fs.FileMode
}

func (fakeFileInfo) Name() string                  { return "fixture" }
func (fakeFileInfo) Size() int64                   { return 0 }
func (information fakeFileInfo) Mode() fs.FileMode { return information.mode }
func (fakeFileInfo) ModTime() time.Time            { return time.Time{} }
func (information fakeFileInfo) IsDir() bool       { return information.mode.IsDir() }
func (fakeFileInfo) Sys() any                      { return nil }

type fakeFileInspector struct {
	information fs.FileInfo
	err         error
}

func (inspector fakeFileInspector) Lstat(string) (fs.FileInfo, error) {
	return inspector.information, inspector.err
}

func TestEvaluateFileExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		inspector FileInspector
		kind      FileKind
		fatal     bool
		want      ConditionResult
	}{
		{"any regular", fakeFileInspector{information: fakeFileInfo{}}, FileKindAny, false, ConditionResult{Satisfied: true}},
		{
			"regular",
			fakeFileInspector{information: fakeFileInfo{}},
			FileKindRegular,
			false,
			ConditionResult{Satisfied: true},
		},
		{
			"directory",
			fakeFileInspector{information: fakeFileInfo{mode: fs.ModeDir}},
			FileKindDirectory,
			false,
			ConditionResult{Satisfied: true},
		},
		{
			"symlink",
			fakeFileInspector{information: fakeFileInfo{mode: fs.ModeSymlink}},
			FileKindSymlink,
			false,
			ConditionResult{Satisfied: true},
		},
		{
			"type mismatch",
			fakeFileInspector{information: fakeFileInfo{mode: fs.ModeDir}},
			FileKindRegular,
			false,
			ConditionResult{},
		},
		{"missing", fakeFileInspector{err: fs.ErrNotExist}, FileKindAny, false, ConditionResult{}},
		{
			"nonfatal error",
			fakeFileInspector{err: fs.ErrPermission},
			FileKindAny,
			false,
			ConditionResult{Err: fs.ErrPermission},
		},
		{
			"fatal error",
			fakeFileInspector{err: fs.ErrPermission},
			FileKindAny,
			true,
			ConditionResult{Fatal: true, Err: fs.ErrPermission},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := EvaluateFileExists(test.inspector, "/fixture", test.kind, test.fatal)
			if got.Satisfied != test.want.Satisfied || got.Fatal != test.want.Fatal ||
				!errors.Is(got.Err, test.want.Err) {
				t.Fatalf("EvaluateFileExists() = %+v, want %+v", got, test.want)
			}
		})
	}

	for _, result := range []ConditionResult{
		EvaluateFileExists(nil, "/fixture", FileKindAny, false),
		EvaluateFileExists(fakeFileInspector{}, "", FileKindAny, false),
		EvaluateFileExists(fakeFileInspector{}, "/fixture", "socket", false),
	} {
		if !result.Fatal || result.Err == nil {
			t.Errorf("invalid file condition result = %+v, want fatal error", result)
		}
	}
}

type fakeProbeRunner struct {
	result ProbeResult
	err    error
	run    func(ProbeSpec)
}

func (runner fakeProbeRunner) RunProbe(_ context.Context, specification ProbeSpec) (ProbeResult, error) {
	if runner.run != nil {
		runner.run(specification)
	}

	return runner.result, runner.err
}

func TestEvaluateProbe(t *testing.T) {
	t.Parallel()

	specification := ProbeSpec{
		Executable:  "/probe",
		Arguments:   []string{"literal argument"},
		Timeout:     time.Second,
		OutputLimit: 16,
	}
	tests := []struct {
		name   string
		runner ProbeRunner
		fatal  bool
		want   ConditionResult
	}{
		{"zero exit", fakeProbeRunner{result: ProbeResult{}}, false, ConditionResult{Satisfied: true}},
		{"nonzero exit", fakeProbeRunner{result: ProbeResult{ExitCode: 1}}, false, ConditionResult{}},
		{
			"bounded output",
			fakeProbeRunner{result: ProbeResult{Output: []byte("output")}},
			false,
			ConditionResult{Satisfied: true},
		},
		{"nonfatal error", fakeProbeRunner{err: fs.ErrPermission}, false, ConditionResult{Err: fs.ErrPermission}},
		{
			"fatal error",
			fakeProbeRunner{err: fs.ErrPermission},
			true,
			ConditionResult{Fatal: true, Err: fs.ErrPermission},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			configured := specification
			configured.FatalOnError = test.fatal
			got := EvaluateProbe(t.Context(), test.runner, configured)
			if got.Satisfied != test.want.Satisfied || got.Fatal != test.want.Fatal ||
				!errors.Is(got.Err, test.want.Err) {
				t.Fatalf("EvaluateProbe() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestEvaluateProbeDefensiveCopyAndRunnerContract(t *testing.T) {
	t.Parallel()

	arguments := []string{"original"}
	specification := ProbeSpec{
		Executable:  "/probe",
		Arguments:   arguments,
		Timeout:     time.Second,
		OutputLimit: 4,
	}
	runner := fakeProbeRunner{
		run: func(received ProbeSpec) {
			received.Arguments[0] = "mutated"
		},
	}
	result := EvaluateProbe(t.Context(), runner, specification)
	if !result.Satisfied || result.Err != nil {
		t.Fatalf("EvaluateProbe() = %+v, want satisfied", result)
	}
	if arguments[0] != "original" {
		t.Fatal("EvaluateProbe() allowed runner to mutate caller arguments")
	}

	negative := EvaluateProbe(t.Context(), fakeProbeRunner{result: ProbeResult{ExitCode: -1}}, specification)
	if !negative.Fatal || negative.Err == nil {
		t.Fatalf("negative exit result = %+v, want fatal contract error", negative)
	}
	overflow := EvaluateProbe(
		t.Context(),
		fakeProbeRunner{result: ProbeResult{Output: []byte("too long")}},
		specification,
	)
	if !overflow.Fatal || overflow.Err == nil {
		t.Fatalf("output overflow result = %+v, want fatal contract error", overflow)
	}
}

func TestProbeValidation(t *testing.T) {
	t.Parallel()

	valid := ProbeSpec{Executable: "/probe", Timeout: time.Second, OutputLimit: 1}
	if err := valid.Validate(); err != nil {
		t.Fatalf("ProbeSpec.Validate() error = %v", err)
	}

	invalid := []ProbeSpec{
		{Timeout: time.Second, OutputLimit: 1},
		{Executable: "bad\x00path", Timeout: time.Second, OutputLimit: 1},
		{Executable: "/probe", Arguments: []string{"bad\x00arg"}, Timeout: time.Second, OutputLimit: 1},
		{Executable: "/probe", OutputLimit: 1},
		{Executable: "/probe", Timeout: time.Second},
	}
	for _, specification := range invalid {
		if err := specification.Validate(); err == nil {
			t.Errorf("ProbeSpec%+v.Validate() unexpectedly succeeded", specification)
		}
	}

	//nolint:staticcheck // Deliberately verifies defensive rejection of a nil context.
	if result := EvaluateProbe(nil, fakeProbeRunner{}, valid); !result.Fatal {
		t.Fatal("EvaluateProbe(nil context) did not return fatal error")
	}
	if result := EvaluateProbe(t.Context(), nil, valid); !result.Fatal {
		t.Fatal("EvaluateProbe(nil runner) did not return fatal error")
	}
}

func TestWouldStartAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	abortAt := now.Add(time.Minute)
	tests := []struct {
		name  string
		now   time.Time
		delay time.Duration
		want  bool
	}{
		{"before", now, 59 * time.Second, false},
		{"exact", now, time.Minute, false},
		{"after delay", now, time.Minute + 1, true},
		{"current time after", abortAt.Add(1), 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := WouldStartAfter(test.now, test.delay, abortAt)
			if err != nil {
				t.Fatalf("WouldStartAfter() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("WouldStartAfter() = %t, want %t", got, test.want)
			}
		})
	}

	for _, input := range []struct {
		now, abortAt time.Time
		delay        time.Duration
	}{
		{abortAt: abortAt},
		{now: now},
		{now: now, abortAt: abortAt, delay: -1},
	} {
		if _, err := WouldStartAfter(input.now, input.delay, input.abortAt); err == nil ||
			strings.TrimSpace(err.Error()) == "" {
			t.Errorf("WouldStartAfter(%v,%v,%v) error = %v", input.now, input.delay, input.abortAt, err)
		}
	}
}
