//go:build e2e

package harness

import (
	"github.com/stretchr/testify/suite"
)

// BaseSuite is a testify suite that owns one Harness for the suite's lifetime
// and truncates DB + mail between tests. Test files can embed it directly.
type BaseSuite struct {
	suite.Suite
	H *Harness

	// Opts is consulted by SetupSuite. Subclasses can override in their own
	// SetupSuite (calling BaseSuite.SetupSuiteWith) to inject e.g. a custom
	// GHValidator.
	Opts Options
}

// SetupSuite boots the harness with default options. Override and call
// SetupSuiteWith to customize.
func (s *BaseSuite) SetupSuite() {
	s.SetupSuiteWith(s.Opts)
}

func (s *BaseSuite) SetupSuiteWith(opts Options) {
	s.H = New(s.T(), opts)
}

func (s *BaseSuite) SetupTest() {
	s.H.TruncateDB(s.T())
	s.H.ResetMailpit(s.T())
	if s.H.GitHub != nil {
		s.H.GitHub.Reset()
	}
}

// TearDownTest dumps container logs to the artifacts dir on failure, so a
// CI-only e2e failure has something to inspect beyond the assertion message.
func (s *BaseSuite) TearDownTest() {
	if s.T().Failed() {
		s.H.DumpContainerLogs(s.T())
	}
}
