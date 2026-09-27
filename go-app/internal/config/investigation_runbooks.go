package config

// Default limits for runbook injection (PHASE-6B). Non-positive configured
// values fall back to these.
const (
	DefaultRunbooksPath        = "/etc/amp/runbooks"
	DefaultRunbooksMaxRunbooks = 3
	DefaultRunbooksMaxChars    = 4000
)

// InvestigationRunbooksConfig configures the runbook engine (PHASE-6B):
// markdown runbooks matched by alert labels and injected into the agentic
// investigation prompt. Requires llm.agent_mode=true.
type InvestigationRunbooksConfig struct {
	Enabled     bool   `mapstructure:"enabled"      yaml:"enabled"`
	Path        string `mapstructure:"path"         yaml:"path"`
	MaxRunbooks int    `mapstructure:"max_runbooks" yaml:"max_runbooks"`
	MaxChars    int    `mapstructure:"max_chars"    yaml:"max_chars"`
}

// EffectiveMaxRunbooks returns MaxRunbooks, or the default when non-positive.
func (c InvestigationRunbooksConfig) EffectiveMaxRunbooks() int {
	if c.MaxRunbooks <= 0 {
		return DefaultRunbooksMaxRunbooks
	}
	return c.MaxRunbooks
}

// EffectiveMaxChars returns MaxChars, or the default when non-positive.
func (c InvestigationRunbooksConfig) EffectiveMaxChars() int {
	if c.MaxChars <= 0 {
		return DefaultRunbooksMaxChars
	}
	return c.MaxChars
}
