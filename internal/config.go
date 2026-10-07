package internal

import (
	"cmp"
	"os"
	"reflect"
	"regexp"
	"strings"

	root "github.com/apitally/apitally-go"
)

const (
	defaultEnv          = "dev"
	defaultOTLPEndpoint = "https://otlp.apitally.io"
)

var writeTokenFormat = regexp.MustCompile(`^apt_[a-zA-Z0-9]{24}$`)

// settings is a Config resolved once at registration. The compiled pattern
// lists hold user patterns only; the components using them add the defaults.
type settings struct {
	config          root.Config
	enabled         bool
	otlpEndpoint    string
	maskQueryParams []*regexp.Regexp
	maskHeaders     []*regexp.Regexp
	maskBodyFields  []*regexp.Regexp
	excludePaths    []*regexp.Regexp
	configErrors    []string
}

func resolveSettings(cfg *root.Config) *settings {
	if cfg == nil {
		cfg = root.NewConfig()
	}
	c := *cfg
	s := &settings{otlpEndpoint: strings.TrimRight(cmp.Or(envValue("APITALLY_OTLP_ENDPOINT"), defaultOTLPEndpoint), "/")}
	c.WriteToken = cmp.Or(strings.TrimSpace(c.WriteToken), envValue("APITALLY_WRITE_TOKEN"))
	c.Env = cmp.Or(strings.TrimSpace(c.Env), envValue("APITALLY_ENV"), defaultEnv)
	c.Disabled = c.Disabled || isTruthy(envValue("APITALLY_DISABLED")) || isTruthy(envValue("OTEL_SDK_DISABLED"))
	// An invalid rate resolves to capturing everything, so no data is lost.
	if !(c.SampleRate >= 0 && c.SampleRate <= 1) {
		c.SampleRate = 1
	}
	c.MaskQueryParams, s.maskQueryParams = s.compilePatterns("MaskQueryParams", c.MaskQueryParams)
	c.MaskHeaders, s.maskHeaders = s.compilePatterns("MaskHeaders", c.MaskHeaders)
	c.MaskBodyFields, s.maskBodyFields = s.compilePatterns("MaskBodyFields", c.MaskBodyFields)
	c.ExcludePaths, s.excludePaths = s.compilePatterns("ExcludePaths", c.ExcludePaths)
	s.config = c
	if c.Disabled {
		return s
	}
	switch {
	case c.WriteToken == "":
		s.configErrors = append(s.configErrors, "Apitally write token is missing (set Config.WriteToken or APITALLY_WRITE_TOKEN), Apitally is disabled")
	case !writeTokenFormat.MatchString(c.WriteToken):
		// The write token is a credential and never appears unmasked in logs.
		s.configErrors = append(s.configErrors, "Apitally write token has an invalid format ("+truncateString(c.WriteToken, 8)+"...), Apitally is disabled")
	default:
		s.enabled = true
	}
	return s
}

// isEquivalent compares resolved configurations. Callbacks are equal when both
// are set or both are unset, because funcs are not comparable and application
// factories create them inline.
func (s *settings) isEquivalent(other *settings) bool {
	a, b := s.config, other.config
	return callbacksSet(a) == callbacksSet(b) && reflect.DeepEqual(withoutCallbacks(a), withoutCallbacks(b))
}

// compilePatterns compiles user patterns case-insensitively, as (?i) followed
// by the pattern, so inline flags can opt out. Invalid patterns are dropped.
func (s *settings) compilePatterns(option string, patterns []string) ([]string, []*regexp.Regexp) {
	var valid []string
	var compiled []*regexp.Regexp
	for _, pattern := range patterns {
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			s.configErrors = append(s.configErrors, "Invalid regular expression in "+option+" ignored: "+err.Error())
			continue
		}
		valid = append(valid, re.String())
		compiled = append(compiled, re)
	}
	return valid, compiled
}

func callbacksSet(c root.Config) [5]bool {
	return [5]bool{
		c.SampleOnRequest != nil,
		c.SampleOnResponse != nil,
		c.MaskRequestBody != nil,
		c.MaskResponseBody != nil,
		c.MaskLogRecord != nil,
	}
}

func withoutCallbacks(c root.Config) root.Config {
	c.SampleOnRequest, c.SampleOnResponse = nil, nil
	c.MaskRequestBody, c.MaskResponseBody = nil, nil
	c.MaskLogRecord = nil
	return c
}

func matchesAny(patterns []*regexp.Regexp, value string) bool {
	for _, re := range patterns {
		if re.MatchString(value) {
			return true
		}
	}
	return false
}

func envValue(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}

func isTruthy(value string) bool {
	switch strings.ToLower(value) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// truncateString cuts s to at most n runes.
func truncateString(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
