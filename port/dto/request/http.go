package request

import "time"

// HTTPRequest describes an outbound HTTP call in transport terms.
type HTTPRequest struct {
	Method      string
	URL         string
	QueryParams map[string]string
	Headers     map[string]string
	Body        []byte
	Timeout     time.Duration
}
