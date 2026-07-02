package api

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"testing"
	"time"

	"runtime.link/api/test"
	"runtime.link/api/xray"
)

type literal string

type Documentation func(context.Context) (Examples, error)

func (fn Documentation) Example(ctx context.Context, name string) (Example, bool) {
	return fn.run(ctx, name, false)
}

func (fn Documentation) run(ctx context.Context, name string, test bool) (Example, bool) {
	if fn == nil {
		return Example{}, false
	}
	isolated, err := fn(ctx)
	if err != nil {
		return Example{
			Error: err,
		}, true
	}
	exampleName := name
	if strings.Contains(name, ":") {
		parts := strings.SplitN(name, ":", 2)
		exampleName = parts[1]
	}
	method := reflect.ValueOf(isolated).MethodByName(exampleName)
	if !method.IsValid() {
		return Example{}, false
	}
	var (
		rtype  = reflect.TypeOf(isolated)
		rvalue = reflect.ValueOf(isolated)
	)
	example := isolated.example()
	// setup API capture
	for i := range rtype.Elem().NumField() {
		field := rtype.Elem().Field(i)
		if !field.IsExported() {
			continue
		}
		prefix := field.Tag.Get("rest")
		spec := StructureOf(rvalue.Elem().Field(i).Addr().Interface())
		spec.link([]string{field.Name})
		example.trace(spec, prefix)
	}
	example.Title = name
	writer, ok := method.Interface().(func(context.Context) error)
	if !ok {
		return Example{}, false
	}
	// Examples run against a fresh background context so documentation
	// rendering never inherits request-scoped state. Test runs, however,
	// must run against the caller's context so the xray recorder installed
	// by Test() captures internal API calls made by the implementation.
	runCtx := context.Background()
	if test {
		runCtx = ctx
	}
	func() {
		defer func() {
			if err := recover(); err != nil {
				example.Error = fmt.Errorf("panic %v %s", err, string(debug.Stack()))
				example.Panic = true
			}
		}()
		if err := writer(runCtx); err != nil {
			example.Error = err
		}
	}()
	return *example, true
}

// methods enumerates the func(context.Context) error methods of the
// documentation template, grouped by the file they are declared in, keeping
// only those methods for which keep returns true.
func (fn Documentation) methods(ctx context.Context, keep func(name string) bool) (map[string][]string, error) {
	if fn == nil {
		return nil, nil
	}
	template, err := fn(ctx)
	if err != nil {
		return nil, err
	}

	categories := make(map[string][]string)
	var rtype = reflect.TypeOf(template)
	var value = reflect.ValueOf(template)
	for i := range rtype.NumMethod() {
		method := rtype.Method(i)
		if _, ok := value.Method(i).Interface().(func(context.Context) error); !ok {
			continue
		}
		if keep != nil && !keep(method.Name) {
			continue
		}

		filename := extractFilenameFromMethod(method)
		categories[filename] = append(categories[filename], method.Name)
	}

	return categories, nil
}

// isTest reports whether a method name denotes a test rather than an example.
// Tests are distinguished by a "Test" prefix; everything else is an example.
func isTest(name string) bool {
	return strings.HasPrefix(name, "Test")
}

// Examples enumerates the example methods, grouped by the file they are
// declared in. Test methods (those prefixed with "Test") are excluded; use
// [Documentation.Tests] to enumerate those.
func (fn Documentation) Examples(ctx context.Context) (map[string][]string, error) {
	return fn.methods(ctx, func(name string) bool { return !isTest(name) })
}

// Tests enumerates the runnable methods, grouped by the file they are
// declared in. Every method is runnable: both examples (rendered as
// documentation) and dedicated test methods (prefixed with "Test"). The
// difference from [Documentation.Examples] is that Tests additionally
// includes the Test-prefixed methods, so the /testruns pages exercise the
// full suite.
func (fn Documentation) Tests(ctx context.Context) (map[string][]string, error) {
	return fn.methods(ctx, nil)
}

// Test runs the named test method and returns a [test.Execution] describing
// what happened: the story, the wall-clock duration, the ordered trace of
// calls (with their arguments and return values) and any error or panic.
// The boolean is false if no method with the given name exists.
func (fn Documentation) Test(ctx context.Context, name string) (test.Execution, bool) {
	start := time.Now()
	ctx = xray.NewContext(ctx)
	example, ok := fn.run(ctx, name, true)
	if !ok {
		return test.Execution{}, false
	}
	exec := test.Execution{
		Title: example.Title,
		Story: example.Story,
		Speed: time.Since(start),
		Panic: example.Panic,
	}
	if example.Error != nil {
		exec.Error = example.Error.Error()
	}
	// Collect top-level steps and internal (xray-recorded) calls into a
	// single flat list, each tagged with the monotonic sequence stamped at
	// its call-start, then order by that sequence so internal calls appear
	// inline between the top-level calls that made them.
	type ordered struct {
		seq   uint64
		event test.Event
	}
	var events []ordered
	for _, step := range example.Steps {
		event := test.Event{Note: step.Note}
		if step.Call != nil {
			event.Call = step.Call.Name
			event.Args = encodeValues(step.Args)
			event.Vals = encodeValues(step.Vals)
		}
		events = append(events, ordered{seq: step.Seq, event: event})
	}
	for xray.ContextHas[xray.Call](ctx) {
		call := xray.ContextGet[xray.Call](ctx)
		events = append(events, ordered{seq: call.Seq, event: test.Event{
			Call: call.Name,
			Args: encodeValues(call.Args),
			Vals: encodeValues(call.Vals),
		}})
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].seq < events[j].seq
	})
	for _, e := range events {
		exec.Trace = append(exec.Trace, e.event)
	}
	return exec, true
}

// History returns the [test.History] assigned to the underlying test
// implementation, or nil if the implementation does not embed a
// [TestingFramework] with an assigned History. The rest host uses this to
// decide whether the builtin /testrun/* endpoints should be served.
func (fn Documentation) History(ctx context.Context) test.History {
	if fn == nil {
		return nil
	}
	isolated, err := fn(ctx)
	if err != nil {
		return nil
	}
	with, ok := isolated.(WithHistory)
	if !ok {
		return nil
	}
	return with.history()
}

// encodeValues renders a slice of reflected call arguments (or return values)
// as a JSON array, falling back to a string rendering for values that cannot
// be marshalled directly (channels, funcs, etc). A nil result is returned for
// an empty slice so the field is omitted from the record.
func encodeValues(values []reflect.Value) json.RawMessage {
	if len(values) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(values))
	for _, v := range values {
		if !v.IsValid() {
			out = append(out, json.RawMessage("null"))
			continue
		}
		data, err := json.Marshal(v.Interface())
		if err != nil {
			data, _ = json.Marshal(fmt.Sprintf("%v", v.Interface()))
		}
		out = append(out, data)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return data
}

type Example struct {
	Title string
	Tests string
	Story string
	Steps []Step
	Error error
	Panic bool

	depth uint
	setup bool
}

type Step struct {
	Note string
	Call *Function
	Args []reflect.Value
	Vals []reflect.Value
	Time time.Time
	Seq  uint64

	Error  error
	Depth  uint
	Setup  bool
	Prefix string
}

type TestingFramework struct {
	eg Example

	History test.History
}

var _ WithExamples = (*Documentation)(nil)

type WithExamples interface {
	Example(context.Context, string) (Example, bool)
	Examples(context.Context) (map[string][]string, error)
}

type WithTests interface {
	Test(context.Context, string) (test.Execution, bool)
	Tests(context.Context) (map[string][]string, error)
	History(context.Context) test.History
}

type Examples interface {
	example() *Example
}

// WithHistory is implemented by test [Examples] implementations that carry an
// assigned [test.History]. The rest host uses it to discover whether the
// builtin /testrun/* endpoints should be served for a given implementation.
type WithHistory interface {
	history() test.History
}

func (tdd *TestingFramework) example() *Example         { return &tdd.eg }
func (tdd *TestingFramework) history() test.History     { return tdd.History }
func (tdd *TestingFramework) Story(description literal) { tdd.eg.Story = string(description) }
func (tdd *TestingFramework) Tests(description literal) { tdd.eg.Tests = string(description) }
func (tdd *TestingFramework) Setup(ctx context.Context, fn func(ctx context.Context) error) error {
	tdd.eg.setup = true
	defer func() {
		tdd.eg.setup = false
	}()
	if err := fn(ctx); err != nil {
		return err
	}
	return nil
}

func (tdd *TestingFramework) Guide(description literal) {
	now := time.Now()
	seq := xray.Sequence()
	// A note is meant to precede and merge with the following API call, so
	// reuse a trailing placeholder that has neither a note nor a recorded
	// call yet. Crucially, do NOT reuse a step that already holds a call
	// (e.g. one recorded during Setup), as that would clobber its ordering.
	if n := len(tdd.eg.Steps); n > 0 {
		last := &tdd.eg.Steps[n-1]
		if last.Note == "" && last.Call == nil {
			last.Note = string(description)
			last.Time = now
			last.Seq = seq
			return
		}
	}
	tdd.eg.Steps = append(tdd.eg.Steps, Step{Note: string(description), Time: now, Seq: seq})
}

func (eg *Example) trace(spec Structure, prefix ...string) {
	var pfx string
	if len(prefix) > 0 {
		pfx = prefix[0]
	}
	for i, old := range spec.Functions {
		old := old.Copy()
		fn := &spec.Functions[i]
		fn.Make(func(ctx context.Context, args []reflect.Value) (results []reflect.Value, err error) {
			eg.depth++
			defer func() {
				eg.depth--
			}()
			// Stamp a monotonic sequence before invoking so a flat trace
			// orders this top-level call ahead of any internal calls it
			// makes (which xray stamps at their own start).
			seq := xray.Sequence()
			start := time.Now()
			results, err = old.Call(ctx, args)
			if len(eg.Steps) == 0 {
				eg.Steps = append(eg.Steps, Step{})
			}
			step := &eg.Steps[len(eg.Steps)-1]
			if step.Call != nil {
				eg.Steps = append(eg.Steps, Step{})
				step = &eg.Steps[len(eg.Steps)-1]
			}
			step.Time = start
			step.Seq = seq
			step.Call = fn
			step.Args = args
			step.Vals = results
			step.Error = err
			step.Depth = eg.depth
			step.Setup = eg.setup
			step.Prefix = pfx
			return
		})
	}
	for _, section := range spec.Namespace {
		eg.trace(section, prefix...)
	}
}

func extractFilenameFromMethod(method reflect.Method) string {
	if method.Func.IsValid() {
		pc := method.Func.Pointer()
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			file, _ := fn.FileLine(pc)
			filename := filepath.Base(file)
			if strings.HasSuffix(filename, ".go") {
				filename = filename[:len(filename)-3]
			}
			return filename
		}
	}
	return "uncategorized"
}

func Test(t *testing.T, impl Documentation) {
	examples, err := impl.Examples(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, categoryExamples := range examples {
		for _, exampleName := range categoryExamples {
			t.Run(exampleName, func(t *testing.T) {
				example, _ := impl.Example(t.Context(), exampleName)
				if example.Error != nil {
					t.Errorf("example %s failed %v", exampleName, example.Error)
				}
			})
		}
	}
}
