package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (a *App) handleAPI(args []string) bool {
	if len(args) < 2 {
		writeEnvelope(false, nil, apiError("E_INVALID_JSON", "missing method or path", nil), nil)
		return false
	}
	method := strings.ToUpper(args[0])
	path := args[1]
	body, err := readBody(method, args[2:])
	if err != nil {
		writeEnvelope(false, nil, apiError("E_INVALID_JSON", err.Error(), nil), nil)
		return false
	}

	switch {
	case method == "GET" && path == "/v1/capabilities":
		if !rejectUnexpectedBody(body) {
			return false
		}
		data, warnings := a.capabilities()
		return a.respondOK(data, warnings...)
	case method == "GET" && path == "/v1/status":
		if !rejectUnexpectedBody(body) {
			return false
		}
		data, warnings := a.status()
		return a.respondOK(data, warnings...)
	case method == "GET" && path == "/v1/apps":
		if !rejectUnexpectedBody(body) {
			return false
		}
		data, warnings := a.appsResult()
		return a.respondOK(data, warnings...)
	case method == "POST" && path == "/v1/apps/force-swap":
		return a.forceSwapAPI(body)
	case method == "GET" && path == "/v1/profile":
		if !rejectUnexpectedBody(body) {
			return false
		}
		profile, warnings := a.loadProfile()
		return a.respondOK(profile, warnings...)
	case method == "PUT" && path == "/v1/profile":
		return a.putProfile(body)
	case method == "POST" && path == "/v1/profile/validate":
		return a.validateProfileAPI(body)
	case method == "POST" && path == "/v1/apply":
		return a.applyAPI(body)
	case method == "POST" && path == "/v1/boot/apply":
		return a.bootApplyAPI(body)
	case method == "GET" && path == "/v1/boot/status":
		if !rejectUnexpectedBody(body) {
			return false
		}
		return a.respondOK(a.loadBootStatus())
	case method == "POST" && path == "/v1/diagnostics/export":
		if !rejectUnexpectedBody(body) {
			return false
		}
		return a.diagnosticsAPI()
	default:
		writeEnvelope(false, nil, apiError("E_UNSUPPORTED_FIELD",
			"unsupported API method or path", map[string]any{
				"method": method,
				"path":   path,
			}), nil)
		return false
	}
}

func rejectUnexpectedBody(body []byte) bool {
	if len(strings.TrimSpace(string(body))) == 0 {
		return true
	}
	writeEnvelope(false, nil, apiError("E_INVALID_JSON", "request body is not allowed", nil), nil)
	return false
}

func (a *App) respondOK(data any, warnings ...Warning) bool {
	writeEnvelope(true, data, nil, warnings)
	return true
}

func writeEnvelope(ok bool, data any, err *APIError, warnings []Warning) {
	if warnings == nil {
		warnings = []Warning{}
	}
	env := Envelope{
		OK:       ok,
		Schema:   apiSchema,
		Data:     data,
		Error:    err,
		Warnings: warnings,
	}
	out, marshalErr := json.Marshal(env)
	if marshalErr != nil {
		fallback := `{"ok":false,"schema":"chswap.api.v1","error":` +
			`{"code":"E_INTERNAL","message":"failed to encode response"},` +
			`"warnings":[]}`
		fmt.Println(fallback)
		return
	}
	fmt.Println(string(out))
}

func apiError(code string, message string, details map[string]any) *APIError {
	return &APIError{Code: code, Message: message, Details: details}
}

func unsupportedWarnings(missing []string) []Warning {
	if len(missing) == 0 {
		return nil
	}
	return []Warning{{
		Code:    "W_KERNEL_NODE_MISSING",
		Message: unsupportedMessage,
		Details: map[string]any{
			"missing_nodes": missing,
		},
	}}
}

func respondUnsupported(missing []string) bool {
	return respondUnsupportedWithWarnings(missing, nil)
}

func respondUnsupportedWithWarnings(missing []string, extra []Warning) bool {
	warnings := unsupportedWarnings(missing)
	warnings = append(warnings, extra...)
	writeEnvelope(false, nil, apiError(unsupportedCode,
		unsupportedMessage, map[string]any{
			"missing_nodes": missing,
		}), warnings)
	return false
}

// gateFor decides the whole-UI gate in two tiers. A missing management node
// means Crystal Hybridswap is absent and wins (unsupported kernel). Otherwise a
// present surface that still lacks required stat fields means the kernel is too
// old; that gate names the missing APIs. The WebUI targets the current ABI and
// keeps no fallback path for older kernels.
func gateFor(missing []string, apiMissing []string) FrontendGate {
	if len(missing) > 0 {
		return FrontendGate{
			Allowed:    false,
			ReasonCode: unsupportedCode,
			Message:    unsupportedMessage,
		}
	}
	if len(apiMissing) > 0 {
		return FrontendGate{
			Allowed:     false,
			ReasonCode:  apiOutdatedCode,
			Message:     apiOutdatedMessage,
			MissingApis: apiMissing,
		}
	}
	return FrontendGate{Allowed: true}
}
