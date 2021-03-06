package testing

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/opentracing/opentracing-go"

	"go.undefinedlabs.com/scopeagent/errors"
	"go.undefinedlabs.com/scopeagent/instrumentation"
	"go.undefinedlabs.com/scopeagent/instrumentation/coverage"
	"go.undefinedlabs.com/scopeagent/instrumentation/logging"
	"go.undefinedlabs.com/scopeagent/instrumentation/testing/config"
	"go.undefinedlabs.com/scopeagent/reflection"
	"go.undefinedlabs.com/scopeagent/runner"
	"go.undefinedlabs.com/scopeagent/tags"
	"go.undefinedlabs.com/scopeagent/tracer"
)

type (
	Test struct {
		testing.TB
		ctx    context.Context
		span   opentracing.Span
		t      *testing.T
		codePC uintptr
	}

	Option func(*Test)
)

var (
	testMapMutex               sync.RWMutex
	testMap                    = map[*testing.T]*Test{}
	autoInstrumentedTestsMutex sync.RWMutex
	autoInstrumentedTests      = map[*testing.T]bool{}

	TESTING_LOG_REGEX = regexp.MustCompile(`(?m)^ {4}(?P<file>[\w\/\.]+):(?P<line>\d+): (?P<message>(.*\n {8}.*)*.*)`)
)

// Options for starting a new test
func WithContext(ctx context.Context) Option {
	return func(test *Test) {
		test.ctx = ctx
	}
}

// Starts a new test
func StartTest(t *testing.T, opts ...Option) *Test {
	pc, _, _, _ := runtime.Caller(1)
	return StartTestFromCaller(t, pc, opts...)
}

// Starts a new test with and uses the caller pc info for Name and Suite
func StartTestFromCaller(t *testing.T, pc uintptr, opts ...Option) *Test {

	// check if the test is cached
	if isTestCached(t, pc) {

		test := &Test{t: t, ctx: context.Background()}
		for _, opt := range opts {
			opt(test)
		}

		// Extracting the testing func name (by removing any possible sub-test suffix `{test_func}/{sub_test}`)
		// to search the func source code bounds and to calculate the package name.
		fullTestName := runner.GetOriginalTestName(t.Name())
		pName, _ := instrumentation.GetPackageAndName(pc)

		testTags := opentracing.Tags{
			"span.kind":      "test",
			"test.name":      fullTestName,
			"test.suite":     pName,
			"test.framework": "testing",
			"test.language":  "go",
		}
		span, _ := opentracing.StartSpanFromContextWithTracer(test.ctx, instrumentation.Tracer(), fullTestName, testTags)
		span.SetBaggageItem("trace.kind", "test")
		span.SetTag("test.status", tags.TestStatus_CACHE)
		span.Finish()
		t.SkipNow()
		return test

	} else {

		// Get or create a new Test struct
		// If we get an old struct we replace the current span and context with a new one.
		// Useful if we want to overwrite the Start call with options
		test, exist := getOrCreateTest(t)
		if exist {
			// If there is already one we want to replace it, so we clear the context
			test.ctx = context.Background()
		}
		test.codePC = pc

		for _, opt := range opts {
			opt(test)
		}

		// Extracting the testing func name (by removing any possible sub-test suffix `{test_func}/{sub_test}`)
		// to search the func source code bounds and to calculate the package name.
		fullTestName := runner.GetOriginalTestName(t.Name())
		pName, _, testCode := instrumentation.GetPackageAndNameAndBoundaries(pc)

		testTags := opentracing.Tags{
			"span.kind":      "test",
			"test.name":      fullTestName,
			"test.suite":     pName,
			"test.framework": "testing",
			"test.language":  "go",
		}

		if testCode != "" {
			testTags["test.code"] = testCode
		}

		if test.ctx == nil {
			test.ctx = context.Background()
		}

		span, ctx := opentracing.StartSpanFromContextWithTracer(test.ctx, instrumentation.Tracer(), fullTestName, testTags)
		span.SetBaggageItem("trace.kind", "test")
		test.span = span
		test.ctx = ctx

		logging.Reset()
		coverage.StartCoverage()

		return test
	}
}

// Set test code
func (test *Test) SetTestCode(pc uintptr) {
	test.codePC = pc
	if test.span == nil {
		return
	}
	pName, _, fBoundaries := instrumentation.GetPackageAndNameAndBoundaries(pc)
	test.span.SetTag("test.suite", pName)
	if fBoundaries != "" {
		test.span.SetTag("test.code", fBoundaries)
	}
}

// Ends the current test
func (test *Test) End() {
	autoInstrumentedTestsMutex.RLock()
	defer autoInstrumentedTestsMutex.RUnlock()
	// First we detect if the current test is auto-instrumented, if not we call the end method (needed in sub tests)
	if _, ok := autoInstrumentedTests[test.t]; !ok {
		test.end()
	}
}

// Gets the test context
func (test *Test) Context() context.Context {
	return test.ctx
}

// Runs an auto instrumented sub test
func (test *Test) Run(name string, f func(t *testing.T)) bool {
	if test.span == nil { // No span = not instrumented
		return test.t.Run(name, f)
	}
	pc, _, _, _ := runtime.Caller(1)
	return test.t.Run(name, func(childT *testing.T) {
		addAutoInstrumentedTest(childT)
		childTest := StartTestFromCaller(childT, pc)
		defer childTest.end()
		f(childT)
	})
}

// Ends the current test (this method is called from the auto-instrumentation)
func (test *Test) end() {
	// We check if we have a span to work with, if not span is found we exit
	if test == nil || test.span == nil {
		return
	}

	finishTime := time.Now()

	// If we have our own implementation of the span, we can set the exact start time from the test
	if ownSpan, ok := test.span.(tracer.Span); ok {
		if startTime, err := reflection.GetTestStartTime(test.t); err == nil {
			ownSpan.SetStart(startTime)
		} else {
			instrumentation.Logger().Printf("error: %v", err)
		}
	}

	// Remove the Test struct from the hash map, so a call to Start while we end this instance will create a new struct
	removeTest(test.t)
	// Stop and get records generated by loggers
	logRecords := logging.GetRecords()

	finishOptions := opentracing.FinishOptions{
		FinishTime: finishTime,
		LogRecords: logRecords,
	}

	if testing.CoverMode() != "" {
		// Checks if the current test is running parallel to extract the coverage or not
		if reflection.GetIsParallel(test.t) && parallel > 1 {
			instrumentation.Logger().Printf("CodePath in parallel test is not supported: %v\n", test.t.Name())
			coverage.RestoreCoverageCounters()
		} else if cov := coverage.EndCoverage(); cov != nil {
			if sp, ok := test.span.(tracer.Span); ok {
				sp.UnsafeSetTag(tags.Coverage, *cov)
			} else {
				test.span.SetTag(tags.Coverage, *cov)
			}
		}
	}

	if r := recover(); r != nil {
		test.span.SetTag("test.status", tags.TestStatus_FAIL)
		errors.WriteExceptionEvent(test.span, r, 1)
		test.span.FinishWithOptions(finishOptions)
		panic(r)
	}
	if test.t.Failed() {
		test.span.SetTag("test.status", tags.TestStatus_FAIL)
		test.span.SetTag("error", true)
	} else if test.t.Skipped() {
		test.span.SetTag("test.status", tags.TestStatus_SKIP)
	} else {
		test.span.SetTag("test.status", tags.TestStatus_PASS)
	}

	test.span.FinishWithOptions(finishOptions)
}

func findMatchesLogRegex(output string) [][]string {
	allMatches := TESTING_LOG_REGEX.FindAllStringSubmatch(output, -1)
	for _, matches := range allMatches {
		matches[3] = strings.Replace(matches[3], "\n        ", "\n", -1)
	}
	return allMatches
}

func extractTestOutput(t *testing.T) *[]byte {
	val := reflect.Indirect(reflect.ValueOf(t))
	member := val.FieldByName("output")
	if member.IsValid() {
		ptrToY := unsafe.Pointer(member.UnsafeAddr())
		return (*[]byte)(ptrToY)
	}
	return nil
}

// Gets or create a test struct
func getOrCreateTest(t *testing.T) (test *Test, exists bool) {
	testMapMutex.Lock()
	defer testMapMutex.Unlock()
	if testPtr, ok := testMap[t]; ok {
		test = testPtr
		exists = true
	} else {
		test = &Test{t: t}
		testMap[t] = test
		exists = false
	}
	return
}

// Removes a test struct from the map
func removeTest(t *testing.T) {
	testMapMutex.Lock()
	defer testMapMutex.Unlock()
	delete(testMap, t)
}

// Gets the Test struct from testing.T
func GetTest(t *testing.T) *Test {
	testMapMutex.RLock()
	defer testMapMutex.RUnlock()
	if test, ok := testMap[t]; ok {
		return test
	}
	return &Test{
		ctx:  context.TODO(),
		span: nil,
		t:    t,
	}
}

// Fails and write panic on running tests
// Use this only if the process is going to crash
func PanicAllRunningTests(e interface{}, skip int) {
	autoInstrumentedTestsMutex.Lock()
	defer autoInstrumentedTestsMutex.Unlock()

	// We copy the testMap because v.end() locks
	testMapMutex.RLock()
	tmp := map[*testing.T]*Test{}
	for k, v := range testMap {
		tmp[k] = v
	}
	testMapMutex.RUnlock()

	for _, v := range tmp {
		delete(autoInstrumentedTests, v.t)
		v.t.Fail()
		errors.WriteExceptionEvent(v.span, e, 1+skip)
		v.end()
	}
}

// Adds an auto instrumented test to the map
func addAutoInstrumentedTest(t *testing.T) {
	autoInstrumentedTestsMutex.Lock()
	defer autoInstrumentedTestsMutex.Unlock()
	autoInstrumentedTests[t] = true
}

// Get if the test is cached
func isTestCached(t *testing.T, pc uintptr) bool {
	pkgName, testName := instrumentation.GetPackageAndName(pc)
	fqn := fmt.Sprintf("%s.%s", pkgName, testName)
	cachedMap := config.GetCachedTestsMap()
	if _, ok := cachedMap[fqn]; ok {
		instrumentation.Logger().Printf("Test '%v' is cached.", fqn)
		fmt.Print("[SCOPE CACHED] ")
		reflection.SkipAndFinishTest(t)
		return true
	}
	instrumentation.Logger().Printf("Test '%v' is not cached.", fqn)
	return false
}
