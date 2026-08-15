package response

// HTTPResponse captures the transport-level response from an outbound HTTP call.
type HTTPResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}
