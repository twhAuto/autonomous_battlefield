// Writes evaluation results as JSON and JUnit reports for local review and CI consumers.

package eval

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
)

func WriteJSON(path string, report Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o600)
}

type testSuites struct {
	XMLName xml.Name    `xml:"testsuites"`
	Suites  []testSuite `xml:"testsuite"`
}
type testSuite struct {
	Name     string     `xml:"name,attr"`
	Tests    int        `xml:"tests,attr"`
	Failures int        `xml:"failures,attr"`
	Cases    []testCase `xml:"testcase"`
}
type testCase struct {
	Name      string   `xml:"name,attr"`
	Classname string   `xml:"classname,attr"`
	Time      string   `xml:"time,attr"`
	Failure   *failure `xml:"failure,omitempty"`
}
type failure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

func WriteJUnit(path string, report Report) error {
	suite := testSuite{Name: ReportSchema, Tests: len(report.Cases)}
	for _, result := range report.Cases {
		item := testCase{Name: result.ID, Classname: result.Category, Time: fmt.Sprintf("%.3f", float64(result.DurationMS)/1000)}
		if !result.Passed {
			suite.Failures++
			item.Failure = &failure{Message: "evaluation failed", Body: fmt.Sprint(result.SafetyFailures)}
		}
		suite.Cases = append(suite.Cases, item)
	}
	payload, err := xml.MarshalIndent(testSuites{Suites: []testSuite{suite}}, "", "  ")
	if err != nil {
		return err
	}
	payload = append([]byte(xml.Header), payload...)
	payload = append(payload, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}
