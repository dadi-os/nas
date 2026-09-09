package main

// Shared infra / cross-cutting error and event codes.
// App modules reuse these for overlapping failures; domain codes stay local.
// HTTP responses use {error: {type, message}} where type equals the code.
const (
	CodeConfigMissing         = "config_missing"
	CodeUpstreamUnreachable   = "upstream_unreachable"
	CodeUpstreamTimeout       = "upstream_timeout"
	CodeProviderUnconfigured  = "provider_unconfigured"
	CodeModuleUnhealthy       = "module_unhealthy"
	CodeProvisionFailed       = "provision_failed"
	CodeLogQueryFailed        = "log_query_failed"
	CodeInvalidRequest        = "invalid_request"
	CodeNotFound              = "not_found"
	CodeForbidden             = "forbidden"
	CodeBusy                  = "busy"
	CodeBinaryFile            = "binary_file"
	CodeInternal              = "internal_error"
)
