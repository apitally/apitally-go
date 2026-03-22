package apitally

import (
	"github.com/apitally/apitally-go/common"
)

type Consumer = common.Consumer
type Config = common.Config
type RequestLoggingConfig = common.RequestLoggingConfig
type Request = common.Request
type Response = common.Response

// NewConfig creates a new Apitally configuration with sensible defaults.
//
// See reference: https://docs.apitally.io/reference/go
var NewConfig = common.NewConfig

type ApitallyConsumer = Consumer
type ApitallyConfig = Config
