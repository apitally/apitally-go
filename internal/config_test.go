package internal

import (
	"log/slog"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	root "github.com/apitally/apitally-go"
	"github.com/apitally/apitally-go/internal/testutils"
)

func TestExplicitOptionsTakePrecedenceOverEnvironmentVariables(t *testing.T) {
	testutils.ClearEnv(t)
	t.Setenv("APITALLY_WRITE_TOKEN", "apt_zzzzzzzzzzzzzzzzzzzzzzzz")
	t.Setenv("APITALLY_ENV", "staging")
	cfg := root.NewConfig()
	cfg.WriteToken = testutils.WriteToken
	cfg.Env = "prod"

	s := resolveSettings(cfg)

	assert.Equal(t, testutils.WriteToken, s.config.WriteToken)
	assert.Equal(t, "prod", s.config.Env)
	assert.True(t, s.isEnabled)
	assert.Empty(t, s.configErrors)
}

func TestEnvironmentVariablesApplyWhenOptionsAreEmpty(t *testing.T) {
	testutils.ClearEnv(t)
	t.Setenv("APITALLY_WRITE_TOKEN", testutils.WriteToken)

	s := resolveSettings(nil)

	assert.Equal(t, testutils.WriteToken, s.config.WriteToken)
	assert.Equal(t, "dev", s.config.Env)
	assert.True(t, s.isEnabled)

	t.Setenv("APITALLY_ENV", "staging")
	assert.Equal(t, "staging", resolveSettings(nil).config.Env)
}

func TestDisableControlsAreAdditive(t *testing.T) {
	for _, name := range []string{"APITALLY_DISABLED", "OTEL_SDK_DISABLED"} {
		t.Run(name, func(t *testing.T) {
			testutils.ClearEnv(t)
			t.Setenv(name, " Yes ")
			cfg := root.NewConfig()
			cfg.WriteToken = testutils.WriteToken
			cfg.Disabled = false

			s := resolveSettings(cfg)

			assert.True(t, s.config.Disabled)
			assert.False(t, s.isEnabled)
		})
	}
}

func TestInvalidWriteTokenDisablesWithMaskedError(t *testing.T) {
	testutils.ClearEnv(t)
	cfg := root.NewConfig()
	cfg.WriteToken = "apt_secretvalue"

	s := resolveSettings(cfg)

	assert.False(t, s.isEnabled)
	require.Len(t, s.configErrors, 1)
	assert.Contains(t, s.configErrors[0], "apt_secr...")
	assert.NotContains(t, s.configErrors[0], "apt_secretvalue")
}

func TestMissingWriteTokenDisables(t *testing.T) {
	testutils.ClearEnv(t)

	s := resolveSettings(nil)

	assert.False(t, s.isEnabled)
	assert.Len(t, s.configErrors, 1)
}

func TestInvalidSampleRateResolvesToOne(t *testing.T) {
	testutils.ClearEnv(t)
	for _, rate := range []float64{-0.1, 1.5, math.NaN()} {
		cfg := root.NewConfig()
		cfg.SampleRate = rate
		assert.Equal(t, 1.0, resolveSettings(cfg).config.SampleRate)
	}
}

func TestUserPatternsAreCaseInsensitiveAndInvalidPatternsDropped(t *testing.T) {
	testutils.ClearEnv(t)
	cfg := root.NewConfig()
	cfg.WriteToken = testutils.WriteToken
	cfg.MaskHeaders = []string{"x-custom", "(", "(?-i:Exact)"}

	s := resolveSettings(cfg)

	assert.Equal(t, []string{"(?i)x-custom", "(?i)(?-i:Exact)"}, s.config.MaskHeaders)
	require.Len(t, s.maskHeaders, 2)
	assert.True(t, s.maskHeaders[0].MatchString("X-Custom"))
	assert.False(t, s.maskHeaders[1].MatchString("exact"))
	assert.Len(t, s.configErrors, 1)
	assert.True(t, s.isEnabled)
}

func TestLaterRegistrationWithDifferentConfigurationWarnsAndIsIgnored(t *testing.T) {
	testutils.ClearEnv(t)
	SetUpTest(t)
	logs := testutils.RecordSlog(t)
	newConfig := func() *root.Config {
		cfg := root.NewConfig()
		cfg.WriteToken = testutils.WriteToken
		cfg.MaskHeaders = []string{"x-secret"}
		cfg.MaskLogRecord = func(*root.LogRecord) bool { return true }
		return cfg
	}

	Register(newConfig(), testFramework, nil)
	Register(newConfig(), testFramework, nil)
	assert.Empty(t, logs.Messages(slog.LevelWarn))

	different := newConfig()
	different.SampleRate = 0.5
	Register(different, testFramework, nil)

	assert.Len(t, logs.Messages(slog.LevelWarn), 1)
	assert.Equal(t, 1.0, currentRuntime.Load().settings.config.SampleRate)
}
