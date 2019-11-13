/*
Copyright 2018 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"google.golang.org/grpc/grpclog"
)

// InitGRPCLogger restores the GRPC logger if any of the related environment variables
// are set
func InitGRPCLogger() {
	const (
		envSeverityLevel  = "GRPC_GO_LOG_SEVERITY_LEVEL"
		envVerbosityLevel = "GRPC_GO_LOG_VERBOSITY_LEVEL"
	)
	severityLevel := os.Getenv(envSeverityLevel)
	verbosityLevel := os.Getenv(envVerbosityLevel)

	if severityLevel == "" && verbosityLevel == "" {
		// Nothing to do
		return
	}

	errorW := ioutil.Discard
	warningW := ioutil.Discard
	infoW := ioutil.Discard

	switch strings.ToLower(severityLevel) {
	case "", "error": // If env is unset, set level to `error`.
		errorW = os.Stderr
	case "warning":
		warningW = os.Stderr
	case "info":
		infoW = os.Stderr
	}

	var verbosity int
	if verbosityOverride, err := strconv.Atoi(verbosityLevel); err == nil {
		verbosity = verbosityOverride
	}
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(infoW, warningW, errorW, verbosity))
}
