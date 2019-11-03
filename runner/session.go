package runner

type (
	testRunnerSession struct {
		Tests []testItem  "json:`tests`"
		Rules runnerRules "json:`rules`"
	}
	testItem struct {
		Fqn                         string       "json:`fqn`"
		Skip                        bool         "json:`skip`"
		RetryOnFailure              bool         "json:`retryOnFailure`"
		IncludeStatusInBuildResults bool         "json:`includeStatusInBuildResults`"
		Rules                       *runnerRules "json:`rules`"
	}
	runnerRules struct {
		FailRetries  int  "json:`failRetries`"
		PassRetries  int  "json:`passRetries`"
		ErrorRetries int  "json:`errorRetries`"
		ExitOnError  bool "json:`exitOnError`"
	}

	sessionLoader interface {
		// Load session configuration
		LoadSessionConfiguration(repository string, branch string, commit string, serviceName string) *testRunnerSession
	}

	dummySessionLoader struct{}
)

func (l *dummySessionLoader) LoadSessionConfiguration(repository string, branch string, commit string, serviceName string) *testRunnerSession {
	return &testRunnerSession{
		Tests: []testItem{
			{
				Fqn:                         "go.undefinedlabs.com/scopeagent/agent.TestFirstTest",
				Skip:                        false,
				RetryOnFailure:              true,
				IncludeStatusInBuildResults: true,
				Rules: &runnerRules{
					FailRetries:  0,
					PassRetries:  0,
					ErrorRetries: 0,
					ExitOnError:  false,
				},
			},
			{
				Fqn:                         "go.undefinedlabs.com/scopeagent/agent.TestDsnParser",
				Skip:                        false,
				RetryOnFailure:              true,
				IncludeStatusInBuildResults: true,
			},
			{
				Fqn:                         "go.undefinedlabs.com/scopeagent/agent.TestSkipped",
				Skip:                        true,
				RetryOnFailure:              true,
				IncludeStatusInBuildResults: true,
			},
			{
				Fqn:                         "go.undefinedlabs.com/scopeagent/agent.TestFlaky",
				Skip:                        false,
				RetryOnFailure:              true,
				IncludeStatusInBuildResults: true,
			},
			{
				Fqn:                         "go.undefinedlabs.com/scopeagent/agent.TestFail",
				Skip:                        false,
				RetryOnFailure:              true,
				IncludeStatusInBuildResults: true,
				Rules: &runnerRules{
					FailRetries:  4,
					PassRetries:  0,
					ErrorRetries: 0,
					ExitOnError:  false,
				},
			},
		},
		Rules: runnerRules{
			FailRetries:  3,
			PassRetries:  1,
			ErrorRetries: 1,
			ExitOnError:  true,
		},
	}
}
