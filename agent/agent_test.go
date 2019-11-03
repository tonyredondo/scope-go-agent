package agent

import (
	"fmt"
	"go.undefinedlabs.com/scopeagent/runner"
	"math/rand"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(runner.Run(m, "repo", "br", "cmmt", "default"))
}

func TestDsnParser(t *testing.T) {
	dsnValues := [][]string{
		{"https://4432432432432423@shared.scope.dev", "4432432432432423", "https://shared.scope.dev"},
		{"http://4432432432432423@shared.scope.dev", "4432432432432423", "http://shared.scope.dev"},
		{"https://4432432432432423:ignored@shared.scope.dev", "4432432432432423", "https://shared.scope.dev"},
		{"https://4432432432432423:ignored@shared.scope.dev/custom/path", "4432432432432423", "https://shared.scope.dev/custom/path"},
		{"https://4432432432432423:ignored@scope.dev", "4432432432432423", "https://scope.dev"},

		{"4432432432432423@shared.scope.dev", "", "4432432432432423@shared.scope.dev"},
		{"noise", "", "noise"},
	}

	for i := 0; i < len(dsnValues); i++ {
		dsnValue := dsnValues[i]
		t.Run(dsnValue[0], func(st *testing.T) {
			apiKey, apiEndpoint, err := parseDSN(dsnValue[0])
			if apiKey != dsnValue[1] || apiEndpoint != dsnValue[2] {
				if err != nil {
					st.Error(err)
				} else {
					fmt.Println(dsnValue, apiKey, apiEndpoint)
				}
				st.FailNow()
			}
		})
	}
}

func TestSkipped(t *testing.T) {
}

func TestFirstTest(t *testing.T) {
}

func TestFail(t *testing.T) {
	t.FailNow()
}

func TestFlaky(t *testing.T) {
	value := rand.Intn(8)
	t.Log("Value", value)
	if value <= 5 {
		t.FailNow()
	}
}

func TestError(t *testing.T) {
	a := 0
	b := 5 / a
	_ = b
	t.FailNow()
}
